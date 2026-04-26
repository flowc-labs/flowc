//go:build e2e
// +build e2e

// Package common provides shared helpers for the flowc e2e test suite:
// image build / load, Helm install, kubectl apply / wait, an in-cluster
// curl pod, and an httpbin upstream. Helpers are intentionally
// thin wrappers around the kubectl / helm / kind / docker CLIs so the
// behaviour matches what a developer running the same commands by hand
// would see.
package common

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
)

// projectRoot walks up from the working directory until it finds a
// go.mod, returning that directory. Used for commands like `docker build`
// that need the repository root as their working directory.
//
// We don't reuse test/utils.GetProjectDir because that function strips a
// hard-coded "/test/utils" substring from $PWD, which produces a wrong
// path when called from anywhere other than test/utils.
func projectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", wd)
		}
		dir = parent
	}
}

// runCmd executes cmd, captures combined output, and logs the command
// line to the Ginkgo writer so failed runs are easy to reproduce.
func runCmd(cmd *exec.Cmd) (string, error) {
	fmt.Fprintf(GinkgoWriter, "running: %s\n", strings.Join(cmd.Args, " "))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w\n%s", strings.Join(cmd.Args, " "), err, string(out))
	}
	return string(out), nil
}

// runInRoot is runCmd with cmd.Dir pinned to the project root.
func runInRoot(cmd *exec.Cmd) (string, error) {
	root, err := projectRoot()
	if err != nil {
		return "", err
	}
	cmd.Dir = root
	return runCmd(cmd)
}

// runWithStdin pipes stdin into cmd, then runs it via runCmd.
func runWithStdin(stdin string, cmd *exec.Cmd) (string, error) {
	cmd.Stdin = strings.NewReader(stdin)
	return runCmd(cmd)
}

// Kubectl runs `kubectl <args...>` and returns combined output.
func Kubectl(args ...string) (string, error) {
	return runCmd(exec.Command("kubectl", args...))
}
