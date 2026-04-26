// Package controller holds the in-process Kubernetes controllers that run
// alongside the flowc REST API and xDS server when store.backend=kubernetes.
//
// The controllers are leader-elected by default in Mode 4 (HA); in Mode 3
// they run unconditionally on the single replica. The manager's informer
// cache is shared with the K8s-backed Store so there is exactly one set of
// informers per process.
package kubernetes

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	flowcv1alpha1 "github.com/flowc-labs/flowc/api/v1alpha1"
	"github.com/flowc-labs/flowc/internal/flowc/config"
)

// ManagerOpts bundles everything NewManager needs from config.
type ManagerOpts struct {
	// WatchNamespace scopes the manager's informers. Empty means cluster-wide.
	WatchNamespace string

	// Kubeconfig is an explicit path; empty falls through to
	// in-cluster → $KUBECONFIG → ~/.kube/config discovery.
	Kubeconfig string

	// LeaderElection enables leader election for runnables that opt in (the
	// default for controller-runtime Reconcilers).
	LeaderElection          bool
	LeaderElectionID        string
	LeaderElectionNamespace string
	LeaseDuration           time.Duration
	RenewDeadline           time.Duration
	RetryPeriod             time.Duration

	// MetricsBindAddress is the :port for the metrics endpoint. "0" disables.
	MetricsBindAddress string

	// HealthProbeBindAddress is the :port for /healthz and /readyz. Empty disables.
	HealthProbeBindAddress string
}

// FromConfig maps the pieces of *config.Config this package needs into
// ManagerOpts.
func FromConfig(cfg *config.Config) ManagerOpts {
	return ManagerOpts{
		WatchNamespace:          cfg.Store.Kubernetes.Namespace,
		Kubeconfig:              cfg.Store.Kubernetes.Kubeconfig,
		LeaderElection:          cfg.Controller.LeaderElection.Enabled,
		LeaderElectionID:        cfg.Controller.LeaderElection.LeaseName,
		LeaderElectionNamespace: cfg.Controller.LeaderElection.Namespace,
		MetricsBindAddress:      cfg.Controller.MetricsAddr,
		HealthProbeBindAddress:  cfg.Controller.ProbeAddr,
	}
}

// NewScheme returns a scheme registered with clientgo types and flowc CRDs.
// The same scheme is used by the manager and by anything that marshals
// flowc objects.
func NewScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(flowcv1alpha1.AddToScheme(scheme))
	return scheme
}

// NewManager builds a controller-runtime Manager with the namespace, leader
// election, and endpoints configured. Callers register their reconcilers on
// the returned manager and then call mgr.Start(ctx).
func NewManager(opts ManagerOpts) (manager.Manager, error) {
	restConfig, err := loadRESTConfig(opts.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	cacheOpts := ctrlcache.Options{}
	if opts.WatchNamespace != "" {
		cacheOpts.DefaultNamespaces = map[string]ctrlcache.Config{opts.WatchNamespace: {}}
	}

	mgrOpts := ctrl.Options{
		Scheme: NewScheme(),
		Cache:  cacheOpts,
		Metrics: metricsserver.Options{
			BindAddress: opts.MetricsBindAddress,
		},
		HealthProbeBindAddress: opts.HealthProbeBindAddress,
		LeaderElection:         opts.LeaderElection,
		LeaderElectionID:       opts.LeaderElectionID,
	}
	if opts.LeaderElectionNamespace != "" {
		mgrOpts.LeaderElectionNamespace = opts.LeaderElectionNamespace
	}
	if opts.LeaseDuration > 0 {
		mgrOpts.LeaseDuration = &opts.LeaseDuration
	}
	if opts.RenewDeadline > 0 {
		mgrOpts.RenewDeadline = &opts.RenewDeadline
	}
	if opts.RetryPeriod > 0 {
		mgrOpts.RetryPeriod = &opts.RetryPeriod
	}

	return ctrl.NewManager(restConfig, mgrOpts)
}

func loadRESTConfig(explicitPath string) (*rest.Config, error) {
	if explicitPath != "" {
		return clientcmd.BuildConfigFromFlags("", explicitPath)
	}
	return ctrl.GetConfig()
}
