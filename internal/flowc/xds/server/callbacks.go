package server

import (
	"context"
	"sync"

	discoveryv3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"

	"github.com/flowc-labs/flowc/pkg/logger"
)

// seedEmptyOnConnect returns Callbacks that install an empty snapshot the
// first time a node opens a stream — but only if the reconciler hasn't
// already published a real one. This breaks the chicken-and-egg where an
// Envoy proxy can't go Ready (Envoy waits the full ~15s ADS init-fetch
// timeout per resource type before /ready returns 200) without an xDS
// response, while the K8s store's projectability gate keeps the Gateway
// out of the reconciler's view until its replicas are Ready.
//
// LoadOrStore on the seeded set guarantees we attempt seeding at most once
// per node — every subsequent OnStreamRequest (which fires per-ack/nack)
// is a no-op. There is a microsecond TOCTOU between GetSnapshot and
// SetSnapshot where the reconciler could publish a real snapshot we then
// overwrite; the reconciler's next Watch event re-publishes if so. In
// the chicken-and-egg case we are actually solving here, the reconciler
// publishes nothing concurrently, so the race is moot.
func seedEmptyOnConnect(cache cachev3.SnapshotCache, log *logger.EnvoyLogger) serverv3.Callbacks {
	var seeded sync.Map
	seed := func(nodeID string) {
		if nodeID == "" {
			return
		}
		if _, loaded := seeded.LoadOrStore(nodeID, struct{}{}); loaded {
			return
		}
		if _, err := cache.GetSnapshot(nodeID); err == nil {
			return
		}
		snap, err := cachev3.NewSnapshot("0", map[resourcev3.Type][]types.Resource{
			resourcev3.ClusterType:  {},
			resourcev3.EndpointType: {},
			resourcev3.ListenerType: {},
			resourcev3.RouteType:    {},
		})
		if err != nil {
			log.WithFields(map[string]any{"node": nodeID, "error": err.Error()}).Error("Failed to build empty snapshot for seed")
			return
		}
		if err := cache.SetSnapshot(context.Background(), nodeID, snap); err != nil {
			log.WithFields(map[string]any{"node": nodeID, "error": err.Error()}).Error("Failed to seed empty snapshot")
			return
		}
		log.WithFields(map[string]any{"node": nodeID}).Info("Seeded empty snapshot for new node")
	}
	return serverv3.CallbackFuncs{
		StreamRequestFunc: func(_ int64, req *discoveryv3.DiscoveryRequest) error {
			seed(req.GetNode().GetId())
			return nil
		},
		StreamDeltaRequestFunc: func(_ int64, req *discoveryv3.DeltaDiscoveryRequest) error {
			seed(req.GetNode().GetId())
			return nil
		},
	}
}
