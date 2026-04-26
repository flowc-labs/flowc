//go:build e2e
// +build e2e

package common

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SetupCurlPod starts a long-lived curl pod that the suite can exec into
// for HTTP requests. Long-lived (vs `kubectl run --rm` per request) so
// each test exec is a sub-second operation rather than a pod-create
// round trip, and so we don't depend on local port-forwarding from CI.
func SetupCurlPod(name, namespace string) error {
	if _, err := Kubectl(
		"run", name,
		"-n", namespace,
		"--image=curlimages/curl:latest",
		"--restart=Never",
		"--command", "--",
		"sleep", "infinity",
	); err != nil {
		return fmt.Errorf("create curl pod: %w", err)
	}
	if _, err := Kubectl(
		"wait", "pod/"+name,
		"-n", namespace,
		"--for=condition=Ready",
		"--timeout=60s",
	); err != nil {
		return fmt.Errorf("wait for curl pod: %w", err)
	}
	return nil
}

// TeardownCurlPod removes the curl pod. Failures are ignored.
func TeardownCurlPod(name, namespace string) {
	_, _ = Kubectl(
		"delete", "pod", name,
		"-n", namespace,
		"--force", "--grace-period=0",
		"--ignore-not-found=true",
	)
}

// CurlStatus returns just the HTTP status code from `curl <url>` issued
// inside podName. Two separate execs (status + body) keep parsing
// trivial — see CurlBody for the body fetch.
func CurlStatus(podName, namespace, url string) (int, error) {
	out, err := Kubectl(
		"exec", "-n", namespace, podName, "--",
		"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", url,
	)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// CurlBody returns the body of `curl <url>` issued inside podName.
func CurlBody(podName, namespace, url string) (string, error) {
	return Kubectl("exec", "-n", namespace, podName, "--", "curl", "-s", url)
}

// CurlStatusAndBody combines CurlStatus + CurlBody. Useful when the test
// needs to assert on both at once.
func CurlStatusAndBody(podName, namespace, url string) (int, string, error) {
	code, err := CurlStatus(podName, namespace, url)
	if err != nil {
		return 0, "", err
	}
	body, err := CurlBody(podName, namespace, url)
	if err != nil {
		return code, "", err
	}
	return code, body, nil
}

// CurlEventually polls CurlStatus until it returns the expected code or
// timeout elapses. Useful for waiting out the brief window between
// Deployment Ready and Envoy actually serving the new RouteConfig.
func CurlEventually(podName, namespace, url string, expected int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastCode int
	var lastErr error
	for time.Now().Before(deadline) {
		code, err := CurlStatus(podName, namespace, url)
		if err == nil && code == expected {
			return nil
		}
		lastCode, lastErr = code, err
		time.Sleep(time.Second)
	}
	if lastErr != nil {
		return fmt.Errorf("curl never returned %d within %s; last error: %w", expected, timeout, lastErr)
	}
	return fmt.Errorf("curl never returned %d within %s; last code: %d", expected, timeout, lastCode)
}

