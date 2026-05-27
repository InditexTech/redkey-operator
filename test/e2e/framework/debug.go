// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"os/exec"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/inditextech/redkeyoperator/test/utils"
)

// CollectDebugInfo gathers debugging data for a namespace on test failure.
// It prints operator/robin logs, kubernetes events, pod descriptions, and cluster info.
func CollectDebugInfo(ctx context.Context, c client.Client, namespace string) {
	_, _ = fmt.Fprintf(GinkgoWriter, "\n=== DEBUG INFO FOR NAMESPACE %s ===\n\n", namespace)

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

	// Pod logs (last 50 lines)
	cmd = exec.Command("kubectl", "logs", podName, "-n", namespace, "--tail=50", "--all-containers=true")
	output, _ = utils.Run(cmd)
	_, _ = fmt.Fprintf(GinkgoWriter, "Logs (last 50 lines):\n%s\n", output)
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
