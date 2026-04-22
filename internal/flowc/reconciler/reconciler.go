package reconciler

import (
	"context"
	"sync"
	"time"

	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/resource/store"
	"github.com/flowc-labs/flowc/internal/flowc/xds/cache"
	"github.com/flowc-labs/flowc/pkg/logger"
)

const debounceWindow = 100 * time.Millisecond

// reconcileLevel indicates the scope of reconciliation needed for a gateway.
type reconcileLevel int

const (
	// levelFullGateway means re-translate all deployments and replace the
	// entire xDS snapshot. Used for Gateway/Listener changes.
	levelFullGateway reconcileLevel = iota

	// levelSingleDeployment means only one deployment changed; upsert or
	// remove just that deployment's resources.
	levelSingleDeployment
)

// pendingWork describes what reconciliation is needed for a single gateway.
type pendingWork struct {
	level          reconcileLevel
	deploymentName string               // only for levelSingleDeployment
	eventType      store.WatchEventType // only for levelSingleDeployment
}

// Reconciler watches the resource store for changes and reconciles
// the xDS configuration for affected gateways.
type Reconciler struct {
	store          store.Store
	configManager  *cache.ConfigManager
	parserRegistry *ir.ParserRegistry
	logger         *logger.EnvoyLogger

	mu      sync.Mutex
	pending map[string]*pendingWork // keyed by gateway name
}

// NewReconciler creates a new reconciler.
func NewReconciler(
	s store.Store,
	configManager *cache.ConfigManager,
	parserRegistry *ir.ParserRegistry,
	log *logger.EnvoyLogger,
) *Reconciler {
	return &Reconciler{
		store:          s,
		configManager:  configManager,
		parserRegistry: parserRegistry,
		logger:         log,
		pending:        make(map[string]*pendingWork),
	}
}

// Start begins the reconciliation loop. It blocks until ctx is cancelled.
func (r *Reconciler) Start(ctx context.Context) error {
	r.logger.Info("Reconciler starting")

	// Full reconcile on startup
	if err := r.fullReconcile(ctx); err != nil {
		r.logger.WithError(err).Error("Initial full reconcile failed")
	}

	// Watch for changes
	ch, err := r.store.Watch(ctx, store.WatchFilter{})
	if err != nil {
		return err
	}

	r.logger.Info("Reconciler watching for changes")

	var debounceTimer *time.Timer
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("Reconciler stopping")
			return nil

		case event, ok := <-ch:
			if !ok {
				r.logger.Info("Watch channel closed, reconciler stopping")
				return nil
			}
			r.enqueueFromEvent(event)

			// Debounce: wait for more events before reconciling
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(debounceWindow, func() {
				r.flushPending(ctx)
			})
		}
	}
}

// isStatusOnlyUpdate returns true when an update changed only the status
// (StatusJSON) and not the spec (SpecJSON). Status-only updates are produced
// by the reconciler itself and must not re-trigger reconciliation.
func isStatusOnlyUpdate(event store.WatchEvent) bool {
	if event.OldResource == nil {
		return false // create, not an update
	}
	// bytes.Equal on json.RawMessage (which is []byte)
	return string(event.Resource.SpecJSON) == string(event.OldResource.SpecJSON)
}

// enqueueFromEvent determines which gateway(s) are affected by a store event
// and records the appropriate reconciliation level.
func (r *Reconciler) enqueueFromEvent(event store.WatchEvent) {
	res := event.Resource
	if res == nil {
		return
	}

	// Ignore status-only updates to avoid feedback loops — the reconciler
	// writes status after each reconciliation, which would otherwise
	// re-trigger itself indefinitely.
	if event.Type == store.WatchEventPut && isStatusOnlyUpdate(event) {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	switch res.Meta.Kind {
	case "Gateway":
		// Gateway change always triggers full rebuild
		r.mergeWork(res.Meta.Name, &pendingWork{level: levelFullGateway})

	case "Listener":
		var spec struct {
			GatewayRef string `json:"gatewayRef"`
		}
		if err := unmarshalJSON(res.SpecJSON, &spec); err == nil && spec.GatewayRef != "" {
			r.mergeWork(spec.GatewayRef, &pendingWork{level: levelFullGateway})
		}

	case "API":
		// API changes are inert — they don't trigger reconciliation.
		// The user must explicitly create/update a Deployment to deploy
		// API changes to a specific gateway.

	case "Deployment":
		var spec struct {
			Gateway struct {
				Name string `json:"name"`
			} `json:"gateway"`
		}
		if err := unmarshalJSON(res.SpecJSON, &spec); err == nil && spec.Gateway.Name != "" {
			r.mergeWork(spec.Gateway.Name, &pendingWork{
				level:          levelSingleDeployment,
				deploymentName: res.Meta.Name,
				eventType:      event.Type,
			})
		}
	}
}

// mergeWork merges new work into any existing pending work for the same gateway.
//
// Merge rules:
//   - Full gateway always wins over single deployment.
//   - Two different deployments changing -> escalate to full gateway.
//   - Same deployment changing multiple times -> take the latest event type.
func (r *Reconciler) mergeWork(gatewayName string, incoming *pendingWork) {
	existing, ok := r.pending[gatewayName]
	if !ok {
		// Nothing pending yet — just store the new work.
		r.pending[gatewayName] = incoming
		return
	}

	// Full gateway always wins.
	if incoming.level == levelFullGateway || existing.level == levelFullGateway {
		r.pending[gatewayName] = &pendingWork{level: levelFullGateway}
		return
	}

	// Both are single-deployment level.
	if existing.deploymentName != incoming.deploymentName {
		// Two different deployments changed — escalate to full rebuild.
		r.pending[gatewayName] = &pendingWork{level: levelFullGateway}
		return
	}

	// Same deployment changed again — take the latest event type.
	existing.eventType = incoming.eventType
}

// flushPending reconciles all pending gateways.
func (r *Reconciler) flushPending(ctx context.Context) {
	r.mu.Lock()
	pending := r.pending
	r.pending = make(map[string]*pendingWork)
	r.mu.Unlock()

	if len(pending) == 0 {
		return
	}

	for gwName, work := range pending {
		var err error
		switch work.level {
		case levelFullGateway:
			err = r.reconcileGateway(ctx, gwName)
		case levelSingleDeployment:
			err = r.reconcileSingleDeployment(ctx, gwName, work)
		}

		if err != nil {
			r.logger.WithFields(map[string]any{
				"gateway": gwName,
				"level":   work.level,
				"error":   err.Error(),
			}).Error("Gateway reconciliation failed")
		}
	}
}

// reconcileSingleDeployment dispatches to the appropriate method based on
// whether the deployment was created/updated or deleted.
func (r *Reconciler) reconcileSingleDeployment(ctx context.Context, gatewayName string, work *pendingWork) error {
	switch work.eventType {
	case store.WatchEventPut:
		return r.upsertDeploymentResources(ctx, gatewayName, work.deploymentName)
	case store.WatchEventDelete:
		return r.removeDeploymentResources(ctx, gatewayName)
	default:
		// Unknown event type — fall back to full rebuild
		return r.reconcileGateway(ctx, gatewayName)
	}
}

// fullReconcile reconciles all gateways.
func (r *Reconciler) fullReconcile(ctx context.Context) error {
	stored, err := r.store.List(ctx, store.ListFilter{Kind: "Gateway"})
	if err != nil {
		return err
	}

	r.logger.WithFields(map[string]any{
		"gateway_count": len(stored),
	}).Info("Starting full reconcile")

	for _, gw := range stored {
		if err := r.reconcileGatewayFromStored(ctx, gw); err != nil {
			r.logger.WithFields(map[string]any{
				"gateway": gw.Meta.Name,
				"error":   err.Error(),
			}).Error("Gateway reconciliation failed during full reconcile")
		}
	}
	return nil
}

// reconcileGateway loads a gateway by name and reconciles it.
func (r *Reconciler) reconcileGateway(ctx context.Context, gatewayName string) error {
	stored, err := r.store.Get(ctx, store.ResourceKey{Kind: "Gateway", Name: gatewayName})
	if err != nil {
		return err
	}
	return r.reconcileGatewayFromStored(ctx, stored)
}
