//go:build e2e
// +build e2e

package common

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ApplyYAML pipes content to `kubectl apply -f -` in the given namespace
// (omitted when empty). Multi-document YAML is fine — kubectl accepts a
// stream of documents separated by `---`.
func ApplyYAML(content, namespace string) error {
	args := []string{"apply", "-f", "-"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	_, err := runWithStdin(content, exec.Command("kubectl", args...))
	return err
}

// DeleteYAML pipes content to `kubectl delete -f -`. --ignore-not-found
// is set so cleanup paths are idempotent.
func DeleteYAML(content, namespace string) error {
	args := []string{"delete", "-f", "-", "--ignore-not-found=true"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	_, err := runWithStdin(content, exec.Command("kubectl", args...))
	return err
}

// WaitForReady blocks until the named resource reports
// status.conditions[Ready]=True, or the timeout elapses. Used to drive
// the Accepted→Ready chain across Gateway / Listener / API / Deployment.
func WaitForReady(kind, name, namespace string, timeout time.Duration) error {
	args := []string{
		"wait",
		fmt.Sprintf("%s/%s", kind, name),
		"--for=condition=Ready",
		"--timeout=" + timeout.String(),
	}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	_, err := Kubectl(args...)
	return err
}

// WaitForDeploymentAvailable blocks until a Kubernetes apps/v1 Deployment
// reports condition Available=True (replicas observed and ready).
func WaitForDeploymentAvailable(name, namespace string, timeout time.Duration) error {
	_, err := Kubectl(
		"wait",
		"deployment/"+name,
		"--for=condition=Available",
		"-n", namespace,
		"--timeout="+timeout.String(),
	)
	return err
}

// PodRestartCount returns the sum of containerStatuses[*].restartCount
// across all pods matching app.kubernetes.io/instance=<instance> in the
// given namespace. Zero is the success signal for cold-start regression
// checks (e.g. seedEmptyOnConnect keeps Envoy from crash-looping).
func PodRestartCount(instance, namespace string) (int, error) {
	out, err := Kubectl(
		"get", "pods",
		"-n", namespace,
		"-l", "app.kubernetes.io/instance="+instance,
		"-o", "jsonpath={.items[*].status.containerStatuses[*].restartCount}",
	)
	if err != nil {
		return 0, err
	}
	sum := 0
	for _, f := range strings.Fields(out) {
		n, parseErr := strconv.Atoi(f)
		if parseErr != nil {
			return 0, fmt.Errorf("parse restart count %q: %w", f, parseErr)
		}
		sum += n
	}
	return sum, nil
}
