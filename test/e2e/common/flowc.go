//go:build e2e
// +build e2e

package common

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/flowc-labs/flowc/test/utils"
)

const (
	// DefaultImage is the local image tag used throughout the e2e suite.
	// Matches what `make image` produces by default (see make/install.mk).
	DefaultImage = "flowc:dev"

	// DefaultRelease and DefaultNamespace match the Helm chart defaults.
	DefaultRelease   = "flowc"
	DefaultNamespace = "flowc-system"

	// DefaultChartPath is the chart directory relative to the repo root.
	DefaultChartPath = "install/helm/flowc"
)

// BuildAndLoadImage builds the flowc image with `docker build` and loads
// it into the kind cluster identified by the KIND_CLUSTER env var
// (defaulting to "kind"). Equivalent to `make image image.load`.
func BuildAndLoadImage(image string) error {
	cmd := exec.Command("docker", "build",
		"-t", image,
		"-f", "build/flowc/Dockerfile",
		".",
	)
	if _, err := runInRoot(cmd); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	if err := utils.LoadImageToKindClusterWithName(image); err != nil {
		return fmt.Errorf("kind load: %w", err)
	}
	return nil
}

// HelmInstall installs (or upgrades) the flowc Helm chart, splitting
// repo:tag for the chart's image.repository / image.tag values. Waits
// for the rollout to complete via --wait.
func HelmInstall(release, namespace, image, chartPath string) error {
	repo, tag, ok := strings.Cut(image, ":")
	if !ok || tag == "" {
		tag = "latest"
	}
	cmd := exec.Command("helm", "upgrade", "--install", release, chartPath,
		"--namespace", namespace,
		"--create-namespace",
		"--set", "image.repository="+repo,
		"--set", "image.tag="+tag,
		"--set", "image.pullPolicy=IfNotPresent",
		"--wait", "--timeout=120s",
	)
	_, err := runInRoot(cmd)
	return err
}

// HelmUninstall removes the flowc Helm release. Helm v3 leaves CRDs
// behind on uninstall, which is fine — the kind cluster is torn down
// by the surrounding Makefile target after the suite finishes.
func HelmUninstall(release, namespace string) error {
	cmd := exec.Command("helm", "uninstall", release, "-n", namespace)
	_, err := runCmd(cmd)
	return err
}
