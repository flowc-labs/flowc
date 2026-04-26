//go:build e2e
// +build e2e

// Package e2e contains flowc's end-to-end test suite. The Kind cluster
// itself is created and deleted by the surrounding Makefile target
// (`setup-test-e2e` / `cleanup-test-e2e`); this suite assumes a kind
// cluster identified by $KIND_CLUSTER already exists and the current
// kubeconfig context points at it.
package e2e

import (
	"fmt"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flowc-labs/flowc/test/e2e/common"
)

// TestE2E is the Go test entry point. Ginkgo discovers Describe / It
// blocks across all _test.go files in this package.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting flowc e2e suite\n")
	RunSpecs(t, "flowc e2e suite")
}

var _ = BeforeSuite(func() {
	By("building and loading the flowc image into the kind cluster")
	Expect(common.BuildAndLoadImage(common.DefaultImage)).To(Succeed())

	By("installing the flowc Helm chart")
	Expect(common.HelmInstall(
		common.DefaultRelease,
		common.DefaultNamespace,
		common.DefaultImage,
		common.DefaultChartPath,
	)).To(Succeed())
})

var _ = AfterSuite(func() {
	By("uninstalling the flowc Helm release")
	// Failures are tolerated: the kind cluster gets torn down by the
	// outer Makefile target so leaked resources don't accumulate.
	if err := common.HelmUninstall(common.DefaultRelease, common.DefaultNamespace); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "helm uninstall failed (ignored): %v\n", err)
	}
})
