package kubernetes

import (
	"fmt"
	"net"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/flowc-labs/flowc/internal/flowc/config"
)

// SetupAll registers every reconciler this package provides on the manager.
// Values come from the controller section of the config.
func SetupAll(mgr ctrl.Manager, cfg *config.Config) error {
	host, port, err := splitHostPort(cfg.Controller.XDS.Address)
	if err != nil {
		return fmt.Errorf("controller.xds.address: %w", err)
	}

	pullPolicy := corev1.PullIfNotPresent
	if cfg.Controller.Envoy.ImagePullPolicy != "" {
		pullPolicy = corev1.PullPolicy(cfg.Controller.Envoy.ImagePullPolicy)
	}
	adminPort := cfg.Controller.Envoy.AdminPort
	if adminPort == 0 {
		adminPort = 9901
	}

	if err := (&GatewayReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		XDSHost:              host,
		XDSPort:              port,
		EnvoyImage:           cfg.Controller.Envoy.Image,
		EnvoyImagePullPolicy: pullPolicy,
		EnvoyAdminPort:       adminPort,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup GatewayReconciler: %w", err)
	}

	if err := NewAPIReconciler(mgr.GetClient(), mgr.GetScheme()).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup APIReconciler: %w", err)
	}

	if err := NewListenerReconciler(mgr.GetClient(), mgr.GetScheme()).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup ListenerReconciler: %w", err)
	}

	if err := NewDeploymentReconciler(mgr.GetClient(), mgr.GetScheme()).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setup DeploymentReconciler: %w", err)
	}

	return nil
}

// splitHostPort parses a "host:port" string into typed parts. The port is
// int32 so it plugs straight into containerPort / servicePort fields.
func splitHostPort(addr string) (string, int32, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	return host, int32(port), nil
}
