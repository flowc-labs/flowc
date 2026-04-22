package server

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"

	discoveryv3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/flowc-labs/flowc/internal/flowc/xds/resources/listener"
	"github.com/flowc-labs/flowc/pkg/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// XDSServer represents the XDS control plane server
type XDSServer struct {
	grpcServer *grpc.Server
	cache      cachev3.SnapshotCache
	server     serverv3.Server
	logger     *logger.EnvoyLogger
	port       int
}

// NewXDSServer creates a new XDS server instance
func NewXDSServer(port int, keepaliveTime, keepaliveTimeout, keepaliveMinTime time.Duration, keepalivePermitWithoutStream bool, envoyLogger *logger.EnvoyLogger) *XDSServer {
	// Create a snapshot cache
	snapshotCache := cachev3.NewSnapshotCache(true, cachev3.IDHash{}, envoyLogger)

	// Create the XDS server
	xdsServer := serverv3.NewServer(context.Background(), snapshotCache, nil)

	// Configure gRPC server with keepalive settings
	grpcServer := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    keepaliveTime,
			Timeout: keepaliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveMinTime,
			PermitWithoutStream: keepalivePermitWithoutStream,
		}),
	)

	return &XDSServer{
		grpcServer: grpcServer,
		cache:      snapshotCache,
		server:     xdsServer,
		logger:     envoyLogger,
		port:       port,
	}
}

// RegisterServices registers all XDS services with the gRPC server
func (s *XDSServer) RegisterServices() {
	// Register the XDS services
	discoveryv3.RegisterAggregatedDiscoveryServiceServer(s.grpcServer, s.server)
}

// Start starts the XDS server
func (s *XDSServer) Start() error {
	s.RegisterServices()

	// Create listener
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.port, err)
	}

	s.logger.WithFields(map[string]any{"port": s.port}).Info("Starting XDS server")

	// Start serving
	if err := s.grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

// Stop gracefully stops the XDS server
func (s *XDSServer) Stop() {
	s.logger.Info("Stopping XDS server")
	s.grpcServer.GracefulStop()
}

// GetCache returns the snapshot cache for external configuration updates
func (s *XDSServer) GetCache() cachev3.SnapshotCache {
	return s.cache
}

// GetLogger returns the logger instance
func (s *XDSServer) GetLogger() *logger.EnvoyLogger {
	return s.logger
}

// InitializeDefaultListener creates the initial snapshot with a default listener
// This should be called once for each node ID before any deployments
func (s *XDSServer) InitializeDefaultListener(nodeID string, listenerPort uint32) error {
	s.logger.WithFields(map[string]any{
		"nodeID":       nodeID,
		"listenerPort": listenerPort,
	}).Info("Initializing default listener for node")

	// Create the default shared listener
	defaultListener := listener.CreateListener("flowc_default_listener", "flowc_default_route", listenerPort)

	// Create initial snapshot with the listener
	initialSnapshot, err := cachev3.NewSnapshot(
		"v0", // Initial version
		map[resourcev3.Type][]types.Resource{
			resourcev3.ListenerType: {defaultListener},
			resourcev3.ClusterType:  {}, // Empty, will be added per deployment
			resourcev3.RouteType:    {}, // Empty, will be added per deployment
			resourcev3.EndpointType: {}, // Empty, not needed for LOGICAL_DNS
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create initial snapshot: %w", err)
	}

	// Set the initial snapshot in the cache
	if err := s.cache.SetSnapshot(context.Background(), nodeID, initialSnapshot); err != nil {
		return fmt.Errorf("failed to set initial snapshot: %w", err)
	}

	s.logger.WithFields(map[string]any{
		"nodeID":       nodeID,
		"listenerPort": listenerPort,
		"routeName":    "flowc_default_route",
	}).Info("Default listener initialized successfully")

	return nil
}
