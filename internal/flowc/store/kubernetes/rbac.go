package kubernetes

// The K8s-backed Store runs informers over every flowc CRD kind (read path
// for REST handlers and xDS). The REST API can also create, update, and
// delete any of them. These markers are picked up by controller-gen and
// folded into config/rbac/role.yaml.

// +kubebuilder:rbac:groups=flowc.io,resources=gateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=flowc.io,resources=listeners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=flowc.io,resources=apis,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=flowc.io,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=flowc.io,resources=gatewaypolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=flowc.io,resources=apipolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=flowc.io,resources=backendpolicies,verbs=get;list;watch;create;update;patch;delete

// Status subresources — required because Put routes status writes through
// Status().Update().
// +kubebuilder:rbac:groups=flowc.io,resources=gateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=listeners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=apis/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=deployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=gatewaypolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=apipolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=flowc.io,resources=backendpolicies/status,verbs=get;update;patch

// Leader election resources for the controller-runtime lease.
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
