// Package reconciler is the event loop that drives xDS translation. It
// watches the Store for projectability transitions, mirrors them into an
// in-memory indexer, and dispatches affected translation tasks to
// per-kind translators (GatewayTranslator, DeploymentTranslator) in the
// dispatch package.
//
// The reconciler itself is a pure consumer of the Store.Watch stream —
// it never writes to the store, never reads status conditions (the store
// backend already filters on Ready), and never owns xDS state. All
// translation work happens in internal/flowc/dispatch/ against the
// in-memory index.
package reconciler

import (
	"context"
	"fmt"

	"github.com/flowc-labs/flowc/internal/flowc/dispatch"
	"github.com/flowc-labs/flowc/internal/flowc/index"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/store"
	"github.com/flowc-labs/flowc/internal/flowc/xds/cache"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// Reconciler watches the resource store for changes and drives xDS
// translation through the dispatch package.
type Reconciler struct {
	store      store.Store
	indexer    *index.Indexer
	dispatcher *dispatch.Dispatcher
	log        *logger.EnvoyLogger
}

// NewReconciler wires the indexer, dispatcher, and per-kind translators.
// The returned reconciler is ready to Start; nothing has run yet.
func NewReconciler(
	s store.Store,
	cm *cache.ConfigManager,
	parsers *ir.ParserRegistry,
	log *logger.EnvoyLogger,
) *Reconciler {
	idx := index.New(log)
	disp := dispatch.New(dispatch.DefaultDebounce, log)
	disp.Register(dispatch.NewGatewayTranslator(idx, cm, parsers, log))
	disp.Register(dispatch.NewDeploymentTranslator(idx, cm, parsers, log))
	return &Reconciler{
		store:      s,
		indexer:    idx,
		dispatcher: disp,
		log:        log,
	}
}

// Start runs the reconciler loop: bootstrap the indexer from the store,
// do a full rebuild for every known gateway, then enter the watch loop.
// Blocks until ctx is cancelled or the watch channel closes.
func (r *Reconciler) Start(ctx context.Context) error {
	r.log.Info("Reconciler starting")

	if err := r.indexer.Bootstrap(ctx, r.store); err != nil {
		return fmt.Errorf("bootstrap indexer: %w", err)
	}
	r.log.WithFields(map[string]any{
		"gateways": len(r.indexer.Gateways()),
	}).Info("Indexer bootstrapped")

	// Startup full rebuild: enqueue a Gateway task per known gateway and
	// flush immediately so xDS snapshots are served on the first Envoy
	// connect rather than after the debounce window.
	startupTasks := make([]index.AffectedTask, 0)
	for _, gw := range r.indexer.Gateways() {
		startupTasks = append(startupTasks, index.AffectedTask{
			Kind: "Gateway",
			Name: gw.Name,
		})
	}
	if len(startupTasks) > 0 {
		r.dispatcher.Enqueue(ctx, startupTasks)
		r.dispatcher.Flush(ctx)
		r.log.WithFields(map[string]any{
			"gateways": len(startupTasks),
		}).Info("Startup full rebuild complete")
	}

	ch, err := r.store.Watch(ctx, store.WatchFilter{})
	if err != nil {
		return fmt.Errorf("store watch: %w", err)
	}
	r.log.Info("Reconciler watching for changes")

	for {
		select {
		case <-ctx.Done():
			r.log.Info("Reconciler stopping")
			return nil
		case event, ok := <-ch:
			if !ok {
				r.log.Info("Watch channel closed; reconciler stopping")
				return nil
			}
			tasks := r.indexer.Apply(event)
			r.dispatcher.Enqueue(ctx, tasks)
		}
	}
}
