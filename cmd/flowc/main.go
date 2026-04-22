package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/flowc-labs/flowc/internal/flowc/config"
	"github.com/flowc-labs/flowc/internal/flowc/ir"
	"github.com/flowc-labs/flowc/internal/flowc/reconciler"
	"github.com/flowc-labs/flowc/internal/flowc/resource/store"
	apiServer "github.com/flowc-labs/flowc/internal/flowc/server"
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

	// Create resource store (declarative desired-state store)
	log.Info("Creating resource store")
	resourceStore := store.NewMemoryStore()

	// Create reconciler (watches store, drives xDS translation)
	log.Info("Creating reconciler")
	rec := reconciler.NewReconciler(resourceStore, configManager, ir.DefaultParserRegistry(), log)

	// Set up graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

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

	restAPIServer := apiServer.NewAPIServer(
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
