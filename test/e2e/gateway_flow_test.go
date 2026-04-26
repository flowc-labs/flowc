//go:build e2e
// +build e2e

package e2e

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flowc-labs/flowc/test/e2e/common"
)

// upstreamNamespace is the namespace where the httpbin upstream and the
// in-cluster curl pod live. "default" is fine — the fixtures are
// labelled `app.kubernetes.io/managed-by=flowc-e2e` so a co-tenant can
// tell them apart from anything else.
const upstreamNamespace = "default"

const (
	curlPodName       = "flowc-e2e-curl"
	gatewayName       = "gateway-sample"
	listenerName      = "listener-sample"
	apiName           = "api-sample"
	flowcDeployment   = "deployment-sample"
	apiContext        = "/httpbin"
	listenerPort      = 8080
	defaultWaitWindow = 90 * time.Second
)

const gatewayManifest = `apiVersion: flowc.io/v1alpha1
kind: Gateway
metadata:
  name: ` + gatewayName + `
spec:
  nodeId: flowc-` + gatewayName + `
`

const listenerManifest = `apiVersion: flowc.io/v1alpha1
kind: Listener
metadata:
  name: ` + listenerName + `
spec:
  gatewayRef: ` + gatewayName + `
  port: 8080
`

const apiManifest = `apiVersion: flowc.io/v1alpha1
kind: API
metadata:
  name: ` + apiName + `
spec:
  version: 1.0.0
  context: ` + apiContext + `
  upstream:
    host: httpbin.` + upstreamNamespace + `.svc.cluster.local
    port: 8000
`

const deploymentManifest = `apiVersion: flowc.io/v1alpha1
kind: Deployment
metadata:
  name: ` + flowcDeployment + `
spec:
  apiRef: ` + apiName + `
  gateway:
    name: ` + gatewayName + `
    listener: ` + listenerName + `
`

var _ = Describe("Gateway flow", Ordered, func() {
	BeforeAll(func() {
		By("deploying the httpbin upstream")
		Expect(common.DeployHTTPBin(upstreamNamespace)).To(Succeed())
		Expect(common.WaitForDeploymentAvailable("httpbin", upstreamNamespace, defaultWaitWindow)).To(Succeed())

		By("starting the in-cluster curl pod")
		Expect(common.SetupCurlPod(curlPodName, upstreamNamespace)).To(Succeed())
	})

	AfterAll(func() {
		By("tearing down flowc resources")
		// Reverse-order cleanup; ignore errors so a partial setup still
		// gets cleaned up as far as it can.
		_ = common.DeleteYAML(deploymentManifest, common.DefaultNamespace)
		_ = common.DeleteYAML(apiManifest, common.DefaultNamespace)
		_ = common.DeleteYAML(listenerManifest, common.DefaultNamespace)
		_ = common.DeleteYAML(gatewayManifest, common.DefaultNamespace)

		By("removing the curl pod and httpbin upstream")
		common.TeardownCurlPod(curlPodName, upstreamNamespace)
		_ = common.DeleteHTTPBin(upstreamNamespace)
	})

	AfterEach(func() {
		if !CurrentSpecReport().Failed() {
			return
		}
		// On failure, dump the controller log + relevant resource yaml so
		// CI artefacts capture enough to diagnose without re-running.
		if out, err := common.Kubectl("logs", "-n", common.DefaultNamespace,
			"-l", "app.kubernetes.io/name=flowc", "--tail=200"); err == nil {
			fmt.Fprintf(GinkgoWriter, "flowc logs:\n%s\n", out)
		}
		for _, kind := range []string{"gateway", "listener", "api", "deployment.flowc.io"} {
			if out, err := common.Kubectl("get", kind, "-n", common.DefaultNamespace, "-o", "yaml"); err == nil {
				fmt.Fprintf(GinkgoWriter, "%s yaml:\n%s\n", kind, out)
			}
		}
	})

	It("routes /httpbin/* through Envoy to the httpbin upstream", func() {
		By("creating the Gateway (Accepted should flip True immediately)")
		Expect(common.ApplyYAML(gatewayManifest, common.DefaultNamespace)).To(Succeed())

		By("creating the Listener and waiting for Ready")
		Expect(common.ApplyYAML(listenerManifest, common.DefaultNamespace)).To(Succeed())
		Expect(common.WaitForReady("listener", listenerName, common.DefaultNamespace, defaultWaitWindow)).To(Succeed(),
			"Listener should reach Ready as soon as Gateway is Accepted (independent of Envoy startup)")

		By("creating the API and waiting for Ready")
		Expect(common.ApplyYAML(apiManifest, common.DefaultNamespace)).To(Succeed())
		Expect(common.WaitForReady("api", apiName, common.DefaultNamespace, defaultWaitWindow)).To(Succeed())

		By("creating the Deployment and waiting for Ready")
		Expect(common.ApplyYAML(deploymentManifest, common.DefaultNamespace)).To(Succeed())
		Expect(common.WaitForReady("deployment.flowc.io", flowcDeployment, common.DefaultNamespace, defaultWaitWindow)).To(Succeed())

		By("waiting for the Envoy data-plane Deployment to become Available")
		Expect(common.WaitForDeploymentAvailable(gatewayName, common.DefaultNamespace, defaultWaitWindow)).To(Succeed())

		By("waiting for the Gateway Ready condition")
		Expect(common.WaitForReady("gateway", gatewayName, common.DefaultNamespace, defaultWaitWindow)).To(Succeed())

		By("verifying the Envoy pod did not crash-loop")
		// Zero restarts confirms the seedEmptyOnConnect callback served
		// an initial snapshot in time for /ready to flip green before
		// the liveness probe killed the pod. A regression here surfaces
		// as restartCount >= 1.
		restarts, err := common.PodRestartCount(gatewayName, common.DefaultNamespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(restarts).To(BeZero(),
			"Envoy pod restarted (count=%d); seedEmptyOnConnect or the Accepted/Ready split may be regressed", restarts)

		By("issuing a request through Envoy → httpbin")
		envoyURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s/get",
			gatewayName, common.DefaultNamespace, listenerPort, apiContext)

		// Allow a brief window for Envoy to apply the latest snapshot
		// after Deployment Ready. CurlEventually polls until 200.
		Expect(common.CurlEventually(curlPodName, upstreamNamespace, envoyURL, 200, 30*time.Second)).To(Succeed(),
			"expected 200 from Envoy → httpbin within 30s")

		By("checking the response body reflects the rewritten /get path")
		_, body, err := common.CurlStatusAndBody(curlPodName, upstreamNamespace, envoyURL)
		Expect(err).NotTo(HaveOccurred())
		// httpbin's /get echoes the Host header and the URL it served at.
		// If the RegexRewrite is correct we should see "/get" reflected
		// in the body; if the rewrite was broken (//get → 301) the body
		// would be the 301 HTML instead.
		Expect(body).To(ContainSubstring(`"url"`),
			"response body should be httpbin's JSON envelope; got: %s", body)
		Expect(strings.Contains(body, `"method"`) && strings.Contains(body, `"GET"`)).To(BeTrue(),
			"response body should describe a GET request; got: %s", body)
	})
})
