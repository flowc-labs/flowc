package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Register in-tree auth plugins (GCP, Azure, OIDC, exec) for kubeconfig support.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrlzap "sigs.k8s.io/controller-runtime/pkg/log/zap"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/flowc-labs/flowc/internal/flowc/config"
	"github.com/flowc-labs/flowc/internal/flowc/httpsrv"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	k8sprovider "github.com/flowc-labs/flowc/internal/flowc/providers/kubernetes"
	"github.com/flowc-labs/flowc/internal/flowc/reconciler"
	"github.com/flowc-labs/flowc/internal/flowc/store"
	k8sstore "github.com/flowc-labs/flowc/internal/flowc/store/kubernetes"
	"github.com/flowc-labs/flowc/internal/flowc/xds/cache"
	"github.com/flowc-labs/flowc/internal/flowc/xds/server"
	"github.com/flowc-labs/flowc/pkg/logger"
)

func main() {
	// Create logger
	log := logger.NewDefaultEnvoyLogger()
	log.Info("Starting FlowC XDS Control Plane...")

	// Load configuration
	log.Info("Loading configuration...")
	cfg, err := config.Load("")
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	// Log configuration details
	log.WithFields(map[string]any{
		"api_port":              cfg.Server.APIPort,
		"xds_port":              cfg.Server.XDSPort,
		"default_listener_port": cfg.XDS.DefaultListenerPort,
		"default_node_id":       cfg.XDS.DefaultNodeID,
		"log_level":             cfg.Logging.Level,
	}).Info("Configuration loaded successfully")

	// Set up graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Create resource store (declarative desired-state store). The backend
	// is selected by config: memory for Mode 1, kubernetes for Mode 3.
	log.WithFields(map[string]any{
		"backend": cfg.Store.Backend,
	}).Info("Creating resource store")
	resourceStore, storeCleanup, err := buildStore(ctx, cfg, log)
	if err != nil {
		log.WithError(err).Fatal("Failed to create resource store")
	}
	defer storeCleanup()

	// Create XDS server with configuration
	log.WithFields(map[string]any{
		"port": cfg.Server.XDSPort,
	}).Info("Creating XDS server")

	xdsServer := server.NewXDSServer(
		cfg.Server.XDSPort,
		cfg.GetKeepaliveTime(),
		cfg.GetKeepaliveTimeout(),
		cfg.GetKeepaliveMinTime(),
		cfg.XDS.GRPC.KeepalivePermitWithoutStream,
		log,
	)

	// Create configuration manager
	log.Info("Creating configuration manager")
	configManager := cache.NewConfigManager(xdsServer.GetCache(), xdsServer.GetLogger())

	// Create reconciler (watches store, drives xDS translation)
	log.Info("Creating reconciler")
	rec := reconciler.NewReconciler(resourceStore, configManager, ir.DefaultParserRegistry(), log)

	go func() {
		<-sigChan
		log.Info("Received shutdown signal")
		cancel()
		xdsServer.Stop()
	}()

	// Create REST API server with resource store
	log.WithFields(map[string]any{
		"port": cfg.Server.APIPort,
	}).Info("Creating REST API server")

	restAPIServer := httpsrv.NewServer(
		cfg.Server.APIPort,
		cfg.Server.XDSPort,
		cfg.GetServerReadTimeout(),
		cfg.GetServerWriteTimeout(),
		cfg.GetServerIdleTimeout(),
		resourceStore,
		log,
	)

	// Start the XDS server in a goroutine
	log.Info("Starting XDS server...")
	go func() {
		if err := xdsServer.Start(); err != nil {
			log.WithError(err).Fatal("Failed to start XDS server")
		}
	}()

	// Start the reconciler in a goroutine
	log.Info("Starting reconciler...")
	go func() {
		if err := rec.Start(ctx); err != nil {
			log.WithError(err).Error("Reconciler stopped with error")
		}
	}()

	// Start the REST API server in a goroutine
	log.Info("Starting REST API server...")
	go func() {
		if err := restAPIServer.Start(); err != nil {
			log.WithError(err).Fatal("Failed to start REST API server")
		}
	}()

	// Give the servers a moment to start
	time.Sleep(100 * time.Millisecond)

	log.WithFields(map[string]any{
		"xds_port":     cfg.Server.XDSPort,
		"api_port":     cfg.Server.APIPort,
		"node_id":      cfg.XDS.DefaultNodeID,
		"api_endpoint": fmt.Sprintf("http://localhost:%d", cfg.Server.APIPort),
	}).Info("FlowC Control Plane started successfully")
	log.Info("Use Ctrl+C to stop the servers")

	// Keep the main goroutine alive
	<-ctx.Done()

	// Graceful shutdown
	shutdownTimeout := cfg.GetShutdownTimeout()
	log.WithFields(map[string]any{
		"timeout": shutdownTimeout.String(),
	}).Info("Initiating graceful shutdown")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	if err := restAPIServer.Stop(shutdownCtx); err != nil {
		log.WithError(err).Error("Failed to gracefully stop REST API server")
	}

	log.Info("Servers shutdown complete")
}

// buildStore selects and constructs the store backend named in cfg.Store.
// The cleanup function is a no-op for memory; for kubernetes it stops the
// controller-runtime manager (which owns the informer cache).
func buildStore(ctx context.Context, cfg *config.Config, log *logger.EnvoyLogger) (store.Store, func(), error) {
	switch cfg.Store.Backend {
	case config.StoreBackendMemory, "":
		return store.NewMemoryStore(), func() {}, nil
	case config.StoreBackendKubernetes:
		return buildK8sStore(ctx, cfg, log)
	default:
		return nil, nil, fmt.Errorf("unknown store backend: %q", cfg.Store.Backend)
	}
}

// buildK8sStore stands up a ctrl.Manager (which owns the informer cache),
// wires the K8sStore to it, optionally registers CRD controllers, and starts
// the manager. Returns after the cache has performed its initial list-watch.
func buildK8sStore(ctx context.Context, cfg *config.Config, log *logger.EnvoyLogger) (store.Store, func(), error) {
	// controller-runtime requires a logger be set on the package-level
	// ctrl.Log; otherwise it aborts with a "log.SetLogger(...) was never called"
	// panic on first use. Wire it into a zap dev logger so controller output
	// goes through the same machinery as the rest of the binary.
	ctrl.SetLogger(ctrlzap.New(ctrlzap.UseDevMode(true)))

	log.WithFields(map[string]any{
		"namespace":         cfg.Store.Kubernetes.Namespace,
		"kubeconfig":        cfg.Store.Kubernetes.Kubeconfig,
		"controllerEnabled": cfg.Controller.Enabled,
		"leaderElection":    cfg.Controller.LeaderElection.Enabled,
	}).Info("Building controller-runtime manager")

	mgr, err := k8sprovider.NewManager(k8sprovider.FromConfig(cfg))
	if err != nil {
		return nil, nil, fmt.Errorf("create manager: %w", err)
	}

	s, err := k8sstore.NewFromManager(ctx, mgr, cfg.Store.Kubernetes.Namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("create k8s store: %w", err)
	}

	if cfg.Controller.Enabled {
		if err := k8sprovider.SetupAll(mgr, cfg); err != nil {
			return nil, nil, fmt.Errorf("setup controllers: %w", err)
		}
		log.Info("Registered CRD controllers (GatewayReconciler, APIReconciler, ListenerReconciler, DeploymentReconciler)")
	}

	mgrCtx, mgrCancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := mgr.Start(mgrCtx); err != nil {
			log.WithError(err).Error("controller-runtime manager stopped with error")
		}
	}()

	syncCtx, syncCancel := context.WithTimeout(mgrCtx, 30*time.Second)
	defer syncCancel()
	if !mgr.GetCache().WaitForCacheSync(syncCtx) {
		mgrCancel()
		<-done
		return nil, nil, fmt.Errorf("timed out waiting for K8s informer cache to sync")
	}
	log.Info("Kubernetes store ready (informer cache synced)")

	cleanup := func() {
		mgrCancel()
		<-done
	}
	return s, cleanup, nil
}
