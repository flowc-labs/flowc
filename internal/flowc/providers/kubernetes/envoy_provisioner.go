package kubernetes

import (
	"bytes"
	"fmt"
	"text/template"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	flowcv1alpha1 "github.com/flowc-labs/flowc/api/v1alpha1"
)

const (
	// Labels identify resources provisioned for a particular Gateway.
	labelApp      = "app.kubernetes.io/name"
	labelInstance = "app.kubernetes.io/instance"
	labelPartOf   = "app.kubernetes.io/part-of"

	partOfFlowc = "flowc"
	envoyApp    = "envoy"

	bootstrapKey = "envoy.yaml"
)

// envoyBootstrapTemplate is a minimal Envoy v3 bootstrap that connects to a
// single ADS endpoint at {{ .XDSHost }}:{{ .XDSPort }}. Listeners and
// clusters are fetched dynamically via xDS; the only static cluster is the
// one Envoy uses to reach flowc's xDS server.
const envoyBootstrapTemplate = `node:
  id: {{ .NodeID }}
  cluster: flowc
admin:
  address:
    socket_address:
      address: 0.0.0.0
      port_value: {{ .AdminPort }}
dynamic_resources:
  ads_config:
    api_type: GRPC
    transport_api_version: V3
    grpc_services:
    - envoy_grpc:
        cluster_name: flowc_xds
  cds_config:
    resource_api_version: V3
    ads: {}
  lds_config:
    resource_api_version: V3
    ads: {}
static_resources:
  clusters:
  - name: flowc_xds
    type: STRICT_DNS
    connect_timeout: 5s
    typed_extension_protocol_options:
      envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http2_protocol_options: {}
    load_assignment:
      cluster_name: flowc_xds
      endpoints:
      - lb_endpoints:
        - endpoint:
            address:
              socket_address:
                address: {{ .XDSHost }}
                port_value: {{ .XDSPort }}
`

// bootstrapData holds the values interpolated into envoyBootstrapTemplate.
type bootstrapData struct {
	NodeID    string
	AdminPort int32
	XDSHost   string
	XDSPort   int32
}

// renderBootstrap fills envoyBootstrapTemplate.
func renderBootstrap(d bootstrapData) (string, error) {
	tpl, err := template.New("envoy-bootstrap").Parse(envoyBootstrapTemplate)
	if err != nil {
		return "", fmt.Errorf("parse bootstrap template: %w", err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, d); err != nil {
		return "", fmt.Errorf("execute bootstrap template: %w", err)
	}
	return buf.String(), nil
}

// resourceNames computes the deterministic names for each provisioned object.
func resourceNames(gw *flowcv1alpha1.Gateway) (deployment, service, configMap string) {
	return gw.Name, gw.Name, gw.Name + "-bootstrap"
}

// proxyLabels are the selector labels applied to the Deployment, the
// Service, and the Deployment's Pod template for a given Gateway.
func proxyLabels(gw *flowcv1alpha1.Gateway) map[string]string {
	return map[string]string{
		labelApp:      envoyApp,
		labelInstance: gw.Name,
		labelPartOf:   partOfFlowc,
	}
}

// buildConfigMap renders the Envoy bootstrap YAML into a ConfigMap.
func buildConfigMap(gw *flowcv1alpha1.Gateway, xdsHost string, xdsPort, adminPort int32) (*corev1.ConfigMap, error) {
	bootstrap, err := renderBootstrap(bootstrapData{
		NodeID:    gw.Spec.NodeID,
		AdminPort: adminPort,
		XDSHost:   xdsHost,
		XDSPort:   xdsPort,
	})
	if err != nil {
		return nil, err
	}
	_, _, cmName := resourceNames(gw)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: gw.Namespace,
			Labels:    proxyLabels(gw),
		},
		Data: map[string]string{
			bootstrapKey: bootstrap,
		},
	}, nil
}

// buildDeployment constructs the Envoy Deployment spec. Listener ports are
// declared as containerPorts so the Service (which targets this Deployment)
// has named ports to reference.
func buildDeployment(
	gw *flowcv1alpha1.Gateway,
	listeners []flowcv1alpha1.Listener,
	image string,
	pullPolicy corev1.PullPolicy,
	adminPort int32,
) *appsv1.Deployment {
	deployName, _, cmName := resourceNames(gw)
	labels := proxyLabels(gw)
	containerPorts := buildContainerPorts(listeners, adminPort)

	var replicas int32 = 1

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: gw.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            envoyApp,
							Image:           image,
							ImagePullPolicy: pullPolicy,
							Args: []string{
								"-c", "/etc/envoy/" + bootstrapKey,
								"--service-node", gw.Spec.NodeID,
								"--service-cluster", partOfFlowc,
							},
							Ports: containerPorts,
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "bootstrap",
									MountPath: "/etc/envoy",
									ReadOnly:  true,
								},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/ready",
										Port: intstr.FromInt32(adminPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/ready",
										Port: intstr.FromInt32(adminPort),
									},
								},
								InitialDelaySeconds: 2,
								PeriodSeconds:       5,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "bootstrap",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: cmName,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// buildService constructs the Service that exposes the proxy. Ports are
// driven by the Listener CRs that reference this Gateway; the admin port is
// always included so Kubernetes readiness probes have a target.
func buildService(gw *flowcv1alpha1.Gateway, listeners []flowcv1alpha1.Listener, adminPort int32) *corev1.Service {
	_, svcName, _ := resourceNames(gw)
	labels := proxyLabels(gw)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: gw.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports:    buildServicePorts(listeners, adminPort),
		},
	}
}

func buildContainerPorts(listeners []flowcv1alpha1.Listener, adminPort int32) []corev1.ContainerPort {
	ports := []corev1.ContainerPort{
		{Name: "admin", ContainerPort: adminPort, Protocol: corev1.ProtocolTCP},
	}
	seen := map[int32]bool{adminPort: true}
	for _, l := range listeners {
		p := int32(l.Spec.Port)
		if seen[p] {
			continue
		}
		seen[p] = true
		ports = append(ports, corev1.ContainerPort{
			Name:          listenerPortName(l.Name, p),
			ContainerPort: p,
			Protocol:      corev1.ProtocolTCP,
		})
	}
	return ports
}

func buildServicePorts(listeners []flowcv1alpha1.Listener, adminPort int32) []corev1.ServicePort {
	ports := []corev1.ServicePort{
		{
			Name:       "admin",
			Port:       adminPort,
			TargetPort: intstr.FromInt32(adminPort),
			Protocol:   corev1.ProtocolTCP,
		},
	}
	seen := map[int32]bool{adminPort: true}
	for _, l := range listeners {
		p := int32(l.Spec.Port)
		if seen[p] {
			continue
		}
		seen[p] = true
		ports = append(ports, corev1.ServicePort{
			Name:       listenerPortName(l.Name, p),
			Port:       p,
			TargetPort: intstr.FromInt32(p),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	return ports
}

// listenerPortName produces a port name that fits the 15-char DNS label limit
// and is deterministic for a given (listener name, port) pair.
func listenerPortName(listener string, port int32) string {
	name := fmt.Sprintf("l-%s", listener)
	if len(name) > 15 {
		name = fmt.Sprintf("p-%d", port)
		if len(name) > 15 {
			name = name[:15]
		}
	}
	return name
}
