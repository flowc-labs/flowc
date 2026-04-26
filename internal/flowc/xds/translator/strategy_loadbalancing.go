package translator

import (
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	"github.com/flowc-labs/flowc/internal/flowc/models"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// =============================================================================
// LOAD BALANCING STRATEGIES
// =============================================================================

// RoundRobinLoadBalancingStrategy uses round-robin load balancing
type RoundRobinLoadBalancingStrategy struct{}

func NewRoundRobinLoadBalancingStrategy() *RoundRobinLoadBalancingStrategy {
	return &RoundRobinLoadBalancingStrategy{}
}

func (s *RoundRobinLoadBalancingStrategy) ConfigureCluster(cluster *clusterv3.Cluster, deployment *models.APIDeployment) error {
	cluster.LbPolicy = clusterv3.Cluster_ROUND_ROBIN
	return nil
}

func (s *RoundRobinLoadBalancingStrategy) Name() string {
	return "round-robin"
}

// LeastRequestLoadBalancingStrategy uses least-request load balancing
type LeastRequestLoadBalancingStrategy struct {
	choiceCount uint32
}

func NewLeastRequestLoadBalancingStrategy(choiceCount uint32) *LeastRequestLoadBalancingStrategy {
	if choiceCount == 0 {
		choiceCount = 2 // Default
	}
	return &LeastRequestLoadBalancingStrategy{
		choiceCount: choiceCount,
	}
}

func (s *LeastRequestLoadBalancingStrategy) ConfigureCluster(cluster *clusterv3.Cluster, deployment *models.APIDeployment) error {
	cluster.LbPolicy = clusterv3.Cluster_LEAST_REQUEST

	// Configure choice count
	cluster.LbConfig = &clusterv3.Cluster_LeastRequestLbConfig_{
		LeastRequestLbConfig: &clusterv3.Cluster_LeastRequestLbConfig{
			ChoiceCount: wrapperspb.UInt32(s.choiceCount),
		},
	}

	return nil
}

func (s *LeastRequestLoadBalancingStrategy) Name() string {
	return "least-request"
}

// RandomLoadBalancingStrategy uses random load balancing
type RandomLoadBalancingStrategy struct{}

func NewRandomLoadBalancingStrategy() *RandomLoadBalancingStrategy {
	return &RandomLoadBalancingStrategy{}
}

func (s *RandomLoadBalancingStrategy) ConfigureCluster(cluster *clusterv3.Cluster, deployment *models.APIDeployment) error {
	cluster.LbPolicy = clusterv3.Cluster_RANDOM
	return nil
}

func (s *RandomLoadBalancingStrategy) Name() string {
	return "random"
}

// ConsistentHashLoadBalancingStrategy uses consistent hashing for session affinity
type ConsistentHashLoadBalancingStrategy struct {
	hashOn     string // header, cookie, source-ip
	headerName string
	cookieName string
}

func NewConsistentHashLoadBalancingStrategy(hashOn, headerName, cookieName string) *ConsistentHashLoadBalancingStrategy {
	if hashOn == "" {
		hashOn = "header"
	}
	return &ConsistentHashLoadBalancingStrategy{
		hashOn:     hashOn,
		headerName: headerName,
		cookieName: cookieName,
	}
}

func (s *ConsistentHashLoadBalancingStrategy) ConfigureCluster(cluster *clusterv3.Cluster, deployment *models.APIDeployment) error {
	cluster.LbPolicy = clusterv3.Cluster_RING_HASH

	// Configure ring hash with basic settings
	cluster.LbConfig = &clusterv3.Cluster_RingHashLbConfig_{
		RingHashLbConfig: &clusterv3.Cluster_RingHashLbConfig{
			HashFunction:    clusterv3.Cluster_RingHashLbConfig_XX_HASH,
			MinimumRingSize: wrapperspb.UInt64(1024),
		},
	}

	// Note: Full hash policy configuration requires additional Envoy API setup
	// For now, this provides basic ring hash load balancing
	// In production, you'd configure route-level hash policies

	return nil
}

func (s *ConsistentHashLoadBalancingStrategy) Name() string {
	return "consistent-hash"
}

// LocalityAwareLoadBalancingStrategy prioritizes local endpoints
type LocalityAwareLoadBalancingStrategy struct {
	baseStrategy LoadBalancingStrategy
}

func NewLocalityAwareLoadBalancingStrategy(baseStrategy LoadBalancingStrategy) *LocalityAwareLoadBalancingStrategy {
	if baseStrategy == nil {
		baseStrategy = NewRoundRobinLoadBalancingStrategy()
	}
	return &LocalityAwareLoadBalancingStrategy{
		baseStrategy: baseStrategy,
	}
}

func (s *LocalityAwareLoadBalancingStrategy) ConfigureCluster(cluster *clusterv3.Cluster, deployment *models.APIDeployment) error {
	// Apply base strategy first
	if err := s.baseStrategy.ConfigureCluster(cluster, deployment); err != nil {
		return err
	}

	// Enable locality-aware load balancing
	cluster.CommonLbConfig = &clusterv3.Cluster_CommonLbConfig{
		LocalityConfigSpecifier: &clusterv3.Cluster_CommonLbConfig_LocalityWeightedLbConfig_{
			LocalityWeightedLbConfig: &clusterv3.Cluster_CommonLbConfig_LocalityWeightedLbConfig{},
		},
	}

	return nil
}

func (s *LocalityAwareLoadBalancingStrategy) Name() string {
	return "locality-aware"
}
