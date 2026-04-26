package dispatch

import (
	"context"
	"fmt"

	"github.com/flowc-labs/flowc/internal/flowc/index"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/xds/cache"
	"github.com/flowc-labs/flowc/internal/flowc/xds/translator"
	"github.com/flowc-labs/flowc/pkg/logger"
)

// DeploymentTranslator handles surgical, per-deployment xDS updates.
// On Put it translates the single deployment, merges its
// clusters/endpoints/routes into the snapshot via cache.DeployAPI, and
// records the resulting names in the indexer's ownership map. On Delete
// it reads the recorded names and removes them via cache.UnDeployAPI.
//
// Listeners are never touched here — they're rebuilt by GatewayTranslator
// in response to Listener events.
type DeploymentTranslator struct {
	indexer *index.Indexer
	cache   *cache.ConfigManager
	parsers *ir.ParserRegistry
	options *translator.TranslatorOptions
	log     *logger.EnvoyLogger
}

// NewDeploymentTranslator constructs the translator with all
// dependencies injected. Default translator options are used; pass
// nil parsers only in tests where SpecContent is never set.
func NewDeploymentTranslator(
	idx *index.Indexer,
	cm *cache.ConfigManager,
	parsers *ir.ParserRegistry,
	log *logger.EnvoyLogger,
) *DeploymentTranslator {
	return &DeploymentTranslator{
		indexer: idx,
		cache:   cm,
		parsers: parsers,
		options: translator.DefaultTranslatorOptions(),
		log:     log,
	}
}

// Kind returns the dispatch kind name.
func (t *DeploymentTranslator) Kind() string { return "Deployment" }

// Translate routes Put/Delete tasks to the appropriate handler.
func (t *DeploymentTranslator) Translate(ctx context.Context, task index.AffectedTask) error {
	if task.Deletion {
		return t.handleDelete(ctx, task)
	}
	return t.handlePut(ctx, task)
}

// handlePut translates a single deployment and pushes its resources to
// the cache. If translation fails (e.g. a dependency was removed during
// the debounce window), the error is returned to the dispatcher which
// logs it; the deployment will be re-attempted on the next event that
// affects it.
func (t *DeploymentTranslator) handlePut(ctx context.Context, task index.AffectedTask) error {
	dep, ok := t.indexer.GetDeployment(task.Name)
	if !ok {
		// Removed from indexer between Apply and dispatch — Delete
		// task will follow; nothing to do here.
		return nil
	}

	xds, err := translateOne(ctx, dep, t.indexer, t.parsers, t.options, t.log)
	if err != nil {
		return fmt.Errorf("translate deployment %q: %w", task.Name, err)
	}

	gw, ok := t.indexer.GetGateway(dep.Spec.Gateway.Name)
	if !ok {
		return fmt.Errorf("gateway %q not in indexer for deployment %q", dep.Spec.Gateway.Name, task.Name)
	}
	nodeID := gw.Spec.NodeID

	cd := &cache.APIDeployment{
		Clusters:  xds.Clusters,
		Endpoints: xds.Endpoints,
		Routes:    xds.Routes,
		// Listeners deliberately omitted — gateway-translator owns them.
	}
	if err := t.cache.DeployAPI(nodeID, cd); err != nil {
		return fmt.Errorf("deploy %q to xDS cache: %w", task.Name, err)
	}

	t.indexer.RecordOwnership(nodeID, task.Name, resourceNamesFromXDS(xds))
	return nil
}

// handleDelete removes the deployment's previously-published resources
// using the names recorded at last successful deploy. If no ownership is
// recorded (deployment never deployed, or cleanup already happened via
// gateway delete), this is a no-op.
func (t *DeploymentTranslator) handleDelete(_ context.Context, task index.AffectedTask) error {
	nodeID, names, ok := t.indexer.OwnershipForDeployment(task.Name)
	if !ok {
		return nil
	}
	if err := t.cache.UnDeployAPI(nodeID, names); err != nil {
		return fmt.Errorf("undeploy %q from xDS cache: %w", task.Name, err)
	}
	t.indexer.ClearOwnership(nodeID, task.Name)
	return nil
}
