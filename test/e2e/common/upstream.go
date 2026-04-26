//go:build e2e
// +build e2e

package common

// HTTPBinManifest is the Deployment + Service spec for an httpbin
// upstream the gateway flow scenario points at. Kept inline (rather
// than read from a YAML file) so the test is self-contained — adding
// a new scenario doesn't require co-evolving manifests in another
// directory.
const HTTPBinManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: httpbin
  labels:
    app: httpbin
    app.kubernetes.io/managed-by: flowc-e2e
spec:
  replicas: 1
  selector:
    matchLabels:
      app: httpbin
  template:
    metadata:
      labels:
        app: httpbin
    spec:
      containers:
      - name: httpbin
        image: mccutchen/go-httpbin:v2.15.0
        ports:
        - containerPort: 8080
        readinessProbe:
          httpGet:
            path: /status/200
            port: 8080
          periodSeconds: 2
---
apiVersion: v1
kind: Service
metadata:
  name: httpbin
  labels:
    app: httpbin
    app.kubernetes.io/managed-by: flowc-e2e
spec:
  selector:
    app: httpbin
  ports:
  - name: http
    port: 8000
    targetPort: 8080
`

// DeployHTTPBin applies HTTPBinManifest in the given namespace.
func DeployHTTPBin(namespace string) error {
	return ApplyYAML(HTTPBinManifest, namespace)
}

// DeleteHTTPBin removes the httpbin Deployment + Service.
func DeleteHTTPBin(namespace string) error {
	return DeleteYAML(HTTPBinManifest, namespace)
}
