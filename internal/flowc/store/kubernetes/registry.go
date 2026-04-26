package kubernetes

import (
	"sort"

	flowcv1alpha1 "github.com/flowc-labs/flowc/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// kindEntry describes a CRD kind managed by the K8s-backed store: how to
// allocate a fresh typed object and its list counterpart.
type kindEntry struct {
	Object func() client.Object
	List   func() client.ObjectList
}

// kindRegistry maps the Store's string Kind (matching REST handler naming) to
// the corresponding v1alpha1 types.
var kindRegistry = map[string]kindEntry{
	"Gateway": {
		Object: func() client.Object { return &flowcv1alpha1.Gateway{} },
		List:   func() client.ObjectList { return &flowcv1alpha1.GatewayList{} },
	},
	"Listener": {
		Object: func() client.Object { return &flowcv1alpha1.Listener{} },
		List:   func() client.ObjectList { return &flowcv1alpha1.ListenerList{} },
	},
	"API": {
		Object: func() client.Object { return &flowcv1alpha1.API{} },
		List:   func() client.ObjectList { return &flowcv1alpha1.APIList{} },
	},
	"Deployment": {
		Object: func() client.Object { return &flowcv1alpha1.Deployment{} },
		List:   func() client.ObjectList { return &flowcv1alpha1.DeploymentList{} },
	},
	"GatewayPolicy": {
		Object: func() client.Object { return &flowcv1alpha1.GatewayPolicy{} },
		List:   func() client.ObjectList { return &flowcv1alpha1.GatewayPolicyList{} },
	},
	"APIPolicy": {
		Object: func() client.Object { return &flowcv1alpha1.APIPolicy{} },
		List:   func() client.ObjectList { return &flowcv1alpha1.APIPolicyList{} },
	},
	"BackendPolicy": {
		Object: func() client.Object { return &flowcv1alpha1.BackendPolicy{} },
		List:   func() client.ObjectList { return &flowcv1alpha1.BackendPolicyList{} },
	},
}

// supportedKinds returns the set of kinds the K8s store understands, in
// deterministic order.
func supportedKinds() []string {
	kinds := make([]string, 0, len(kindRegistry))
	for k := range kindRegistry {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}
