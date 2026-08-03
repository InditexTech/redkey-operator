// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/inditextech/redkey-operator/test/utils"
)

// DefaultSpecTimeout is the per-spec time budget applied by SetupSpecContexts when it is given a
// non-positive timeout.
const DefaultSpecTimeout = 20 * time.Minute

// SetupSpecContexts wires per-spec timeout management into an Ordered container and removes the
// cross-spec starvation that a single container-wide deadline causes: an Ordered container runs its
// specs serially on one process, so a single shared deadline is consumed cumulatively and starves
// the later specs as the suite grows (observed as "context deadline exceeded" failures in seconds
// on otherwise-healthy specs). It registers the required Ginkgo lifecycle nodes and writes:
//   - *suiteCtx: a long-lived context for BeforeAll/AfterAll resource lifecycle (e.g. namespace
//     create/delete). It is context.Background(): namespace operations do not need a deadline (the
//     suite-level ginkgo timeout bounds them), and giving the suite context a cancel that runs in a
//     separate AfterAll would race with the container's own DeleteNamespace AfterAll and abort it.
//   - *specCtx: a fresh context with an independent timeout, created before each spec.
//
// The per-spec context is cancelled at the start of the next spec (and the last one in AfterAll)
// rather than in an AfterEach, so it remains valid during any nested Context-level AfterAll cleanup
// that still references it (e.g. deleting the cluster created by that Context), while still ensuring
// every context is eventually cancelled.
//
// Call it once inside the Describe body, before the container's own BeforeAll, so the suite context
// is created before the container's setup runs.
func SetupSpecContexts(suiteCtx, specCtx *context.Context, timeout time.Duration) {
	if timeout <= 0 {
		timeout = DefaultSpecTimeout
	}
	var specCancel context.CancelFunc
	BeforeAll(func() {
		*suiteCtx = context.Background()
	})
	BeforeEach(func() {
		if specCancel != nil {
			specCancel()
		}
		*specCtx, specCancel = context.WithTimeout(context.Background(), timeout)
	})
	AfterAll(func() {
		if specCancel != nil {
			specCancel()
		}
	})
}

// CollectDebugInfoOnFailure collects debug info for the namespace when the current spec has failed,
// using a fresh short-lived context. The spec's own context may already be cancelled or past its
// deadline (for example when the failure was itself a timeout), which would otherwise prevent the
// collector from listing pods and gathering their descriptions.
func CollectDebugInfoOnFailure(c client.Client, namespace string) {
	if !CurrentSpecReport().Failed() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	CollectDebugInfo(ctx, c, namespace)
}

// CollectDebugInfo gathers debugging data for a namespace on test failure.
// It prints operator/robin logs, kubernetes events, pod descriptions, and cluster info.
func CollectDebugInfo(ctx context.Context, c client.Client, namespace string) {
	_, _ = fmt.Fprintf(GinkgoWriter, "\n=== DEBUG INFO FOR NAMESPACE %s ===\n\n", namespace)

	// Robin logs first: Robin is the component that drives topology convergence, so its logs
	// are the primary diagnostic on a scaling/rebalance failure. Collect them before anything
	// else and by label selector (not by the cached pod name, which can be stale) so they are
	// captured even while the pod is being torn down during teardown.
	collectRobinLogs(namespace)

	// Kubernetes events
	collectEvents(namespace)

	// Pod descriptions and logs
	pods := &corev1.PodList{}
	if err := c.List(ctx, pods, client.InNamespace(namespace)); err == nil {
		for _, pod := range pods.Items {
			collectPodInfo(namespace, pod.Name)
		}
	}

	// Operator logs (from the operator namespace)
	collectOperatorLogs()

	// Redis cluster info from the first redis pod
	collectRedisInfo(ctx, c, namespace)

	_, _ = fmt.Fprintf(GinkgoWriter, "\n=== END DEBUG INFO ===\n\n")
}

func collectEvents(namespace string) {
	_, _ = fmt.Fprintf(GinkgoWriter, "--- Kubernetes Events (namespace: %s) ---\n", namespace)
	cmd := exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
	output, err := utils.Run(cmd)
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get events: %v\n", err)
		return
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", output)
}

func collectPodInfo(namespace, podName string) {
	_, _ = fmt.Fprintf(GinkgoWriter, "--- Pod %s/%s ---\n", namespace, podName)

	// Pod status
	cmd := exec.Command("kubectl", "get", "pod", podName, "-n", namespace, "-o", "wide")
	output, _ := utils.Run(cmd)
	_, _ = fmt.Fprintf(GinkgoWriter, "Status: %s\n", output)

	// Pod logs (last 50 lines). Fall back to the previous container's logs when the current
	// logs are empty (e.g. a container that crashed and restarted).
	cmd = exec.Command("kubectl", "logs", podName, "-n", namespace, "--tail=50", "--all-containers=true")
	output, _ = utils.Run(cmd)
	if output == "" {
		prev := exec.Command("kubectl", "logs", podName, "-n", namespace,
			"--tail=50", "--all-containers=true", "--previous")
		if prevOut, err := utils.Run(prev); err == nil && prevOut != "" {
			output = prevOut
		}
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "Logs (last 50 lines):\n%s\n", output)
}

// collectRobinLogs prints the Robin deployment's logs for the namespace, selected by label so
// it does not depend on a specific (possibly already-deleted) pod name. It falls back to the
// previous container's logs when the current logs are empty, so a crashed/restarted or
// terminating Robin still yields its last output — the case where the primary diagnostic was
// previously lost because kubectl logs ran against an already-gone pod.
func collectRobinLogs(namespace string) {
	_, _ = fmt.Fprintf(GinkgoWriter, "--- Robin Logs (last 200 lines) ---\n")
	cmd := exec.Command("kubectl", "logs", "-l", "redkey.inditex.dev/component=robin",
		"-n", namespace, "--tail=200", "--all-containers=true", "--prefix=true")
	output, err := utils.Run(cmd)
	if err != nil || output == "" {
		prev := exec.Command("kubectl", "logs", "-l", "redkey.inditex.dev/component=robin",
			"-n", namespace, "--tail=200", "--all-containers=true", "--prefix=true", "--previous")
		prevOut, prevErr := utils.Run(prev)
		if prevErr != nil && output == "" {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get robin logs: %v (previous: %v)\n", err, prevErr)
			return
		}
		if prevOut != "" {
			output = prevOut
		}
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", output)
}

func collectOperatorLogs() {
	_, _ = fmt.Fprintf(GinkgoWriter, "--- Operator Logs (last 100 lines) ---\n")
	cmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager",
		"-n", "redkey-operator", "--tail=100", "--all-containers=true")
	output, err := utils.Run(cmd)
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get operator logs: %v\n", err)
		return
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "%s\n", output)
}

func collectRedisInfo(ctx context.Context, c client.Client, namespace string) {
	pods := &corev1.PodList{}
	labels := map[string]string{}
	// Try to find redis pods by common label patterns
	if err := c.List(ctx, pods, client.InNamespace(namespace)); err != nil {
		return
	}

	_ = labels // suppress unused warning
	for _, pod := range pods.Items {
		// Check if this is a redis pod by trying to run CLUSTER INFO
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		stdout, _, err := ExecInPod(namespace, pod.Name, "redis-cli cluster info 2>/dev/null || true")
		if err == nil && stdout != "" && len(stdout) > 20 {
			_, _ = fmt.Fprintf(GinkgoWriter, "--- Redis CLUSTER INFO from %s ---\n%s\n", pod.Name, stdout)
			// Only log from one pod
			break
		}
	}
}
