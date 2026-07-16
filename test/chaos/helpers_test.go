// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package chaos

import (
	"fmt"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/inditextech/redkeyoperator/test/chaos/framework"
)

const (
	clusterName      = "redis-cluster"
	defaultPrimaries = 5

	// chaosStabilizationWindow is the calm period granted after each cluster recovery so the k6
	// load generator can refresh its view of the topology, reconnect to the new pods and actually
	// insert keys before the next disruptive action. Without it the scaling and pod-deletion
	// operations run back-to-back and k6 never gets a stable window to drive traffic, which makes
	// the scenario unrealistic and leaves the cluster with no keys.
	chaosStabilizationWindow = 30 * time.Second
	diagnosticsLogTail       = int64(100)

	minPrimaries = 3
	maxPrimaries = 10
	defaultVUs   = 10

	// k6ProgressTimeout bounds how long the k6 load generator is given to show forward progress
	// once the cluster has recovered. A frozen generator (e.g. stuck on stale topology) never
	// advances within this window and fails the spec.
	k6ProgressTimeout = 60 * time.Second
)

// setupChaosNamespace creates an isolated namespace, deploys a namespace-scoped operator into it,
// creates a Redis cluster and waits for it to be ready. It returns the created namespace.
func setupChaosNamespace(prefix string, primaries int32, purgeKeysOnRebalance bool) *corev1.Namespace {
	namespace, err := framework.CreateNamespace(ctx, k8sClient, prefix)
	Expect(err).NotTo(HaveOccurred(), "failed to create namespace")

	By("deploying namespace-scoped operator")
	Expect(framework.EnsureOperatorSetup(ctx, k8sClientset, namespace.Name)).To(Succeed())
	Expect(framework.WaitForOperatorAvailable(ctx, k8sClientset, namespace.Name)).To(Succeed(),
		"operator did not become available in namespace %s", namespace.Name)

	By(fmt.Sprintf("creating Redis cluster with %d primaries (purge=%v)", primaries, purgeKeysOnRebalance))
	opts := framework.DefaultClusterOptions(clusterName, namespace.Name, primaries, purgeKeysOnRebalance)
	_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
	Expect(err).NotTo(HaveOccurred(), "failed to create RedkeyCluster")

	By("waiting for cluster to be ready")
	Expect(framework.WaitForChaosReady(
		ctx, k8sClient, k8sClientset, namespace.Name, clusterName, chaosReadyTimeout,
	)).To(Succeed())

	return namespace
}

// teardownChaosNamespace stops k6 (if running), collects diagnostics on failure, and deletes the
// namespace unless preservation is requested for a failed spec.
func teardownChaosNamespace(namespace *corev1.Namespace, k6DepName string) {
	namespaceName := ""
	if namespace != nil {
		namespaceName = namespace.Name
	}

	if CurrentSpecReport().Failed() && namespaceName != "" {
		collectDiagnostics(namespaceName)
	}
	if k6DepName != "" && namespaceName != "" {
		Expect(framework.StopK6Load(ctx, k8sClientset, namespaceName, k6DepName)).To(Succeed(),
			"failed to clean up k6 deployment %s in namespace %s", k6DepName, namespaceName)
	}
	if skipDeleteNamespace && CurrentSpecReport().Failed() {
		GinkgoWriter.Printf(
			"CHAOS_KEEP_NAMESPACE_ON_FAILED is set and spec failed — preserving namespace %s for inspection\n",
			namespaceName)
		return
	}
	Expect(framework.DeleteNamespace(ctx, k8sClient, namespace)).To(Succeed(),
		"failed to clean up namespace %s", namespaceName)
}

// startK6OrFail starts a k6 load deployment and fails the test if it errors.
func startK6OrFail(namespace, clusterName string) string {
	depName, err := framework.StartK6LoadDeployment(ctx, k8sClientset, namespace, clusterName, defaultVUs)
	Expect(err).NotTo(HaveOccurred(), "failed to start k6 load deployment")
	return depName
}

// stopK6Load stops the k6 load deployment and fails the spec if cleanup fails.
func stopK6Load(namespace, depName string) {
	if depName == "" {
		return
	}
	Expect(framework.StopK6Load(ctx, k8sClientset, namespace, depName)).To(Succeed(),
		"failed to stop k6 deployment %s in namespace %s", depName, namespace)
}

// verifyK6Healthy asserts the k6 load generator is alive and making forward progress against the
// recovered cluster. It must be called while k6 is still running (before stopK6Load). A frozen or
// fully erroring load generator fails the spec here instead of passing silently.
func verifyK6Healthy(namespace string) {
	By("verifying k6 load generator is making progress")
	Expect(framework.WaitForK6Progress(ctx, k8sClientset, namespace, k6ProgressTimeout)).To(Succeed())
}

// stabilizeUnderLoad grants the k6 load generator a calm window against the freshly recovered
// cluster before the next disruptive action. It first asserts k6 resumes forward progress (i.e. it
// refreshed the topology and is inserting keys again) and then holds steady for the stabilization
// window so a meaningful amount of traffic lands while the cluster is healthy. This spaces out the
// chaos operations and makes the scenario behave like a real workload instead of firing scaling and
// pod deletions back-to-back.
func stabilizeUnderLoad(namespace, phase string) {
	By(fmt.Sprintf("stabilizing under load (%s)", phase))
	GinkgoWriter.Printf(
		"Allowing k6 to refresh topology and insert keys for %s (%s)\n", chaosStabilizationWindow, phase)
	Expect(framework.WaitForK6Progress(ctx, k8sClientset, namespace, k6ProgressTimeout)).To(Succeed(),
		"k6 made no progress during stabilization window (%s)", phase)
	time.Sleep(chaosStabilizationWindow)
}

// verifyClusterHealthy runs all cluster health checks.
func verifyClusterHealthy(namespace, clusterName string) {
	By("verifying cluster readiness")
	Expect(framework.WaitForChaosReady(
		ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
	)).To(Succeed())

	By("verifying all slots assigned")
	Expect(framework.AssertAllSlotsAssigned(ctx, k8sClientset, namespace, clusterName)).To(Succeed())

	By("verifying no nodes in fail state")
	Expect(framework.AssertNoNodesInFailState(ctx, k8sClientset, namespace, clusterName)).To(Succeed())
}

// scaleDirection returns a human-readable label for the scaling direction between the current and
// the target primary count.
func scaleDirection(current, target int32) string {
	switch {
	case target > current:
		return "up"
	case target < current:
		return "down"
	default:
		return "to same size"
	}
}

// runScalingChaos runs the continuous-scaling-and-pod-deletion scenario.
func runScalingChaos(rng *rand.Rand, namespace, clusterName string, purgeKeysOnRebalance bool) string {
	By("starting k6 load deployment")
	k6DepName := startK6OrFail(namespace, clusterName)

	By(fmt.Sprintf("executing chaos loop (%d iterations)", chaosIterations))

	currentPrimaries := int32(defaultPrimaries)

	for i := 1; i <= chaosIterations; i++ {
		GinkgoWriter.Printf("=== Chaos iteration %d/%d ===\n", i, chaosIterations)

		newSize := int32(rng.Intn(maxPrimaries-minPrimaries+1) + minPrimaries)
		scaleDir := scaleDirection(currentPrimaries, newSize)
		By(fmt.Sprintf("iteration %d/%d: scaling cluster %s", i, chaosIterations, scaleDir))
		GinkgoWriter.Printf("Scaling %s: %d -> %d primaries\n", scaleDir, currentPrimaries, newSize)
		Expect(framework.ScaleCluster(ctx, k8sClient, namespace, clusterName, newSize)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: failed to scale cluster %s to %d", i, chaosIterations, scaleDir, newSize))

		// With purge enabled the StatefulSet is recreated on scaling, so wait for the new one to
		// reflect the target replica count before interacting with pods.
		if purgeKeysOnRebalance {
			Expect(framework.WaitForScaleAck(ctx, k8sClient, namespace, clusterName, newSize)).To(Succeed(),
				fmt.Sprintf("iteration %d/%d: StatefulSet did not acknowledge scale to %d", i, chaosIterations, newSize))
		}

		By(fmt.Sprintf("iteration %d/%d: deleting random redis pods", i, chaosIterations))
		deleteCount := rng.Intn(int(newSize)/2) + 1
		deleted, err := framework.DeleteRandomRedisPods(ctx, k8sClientset, namespace, clusterName, deleteCount, rng)
		Expect(err).NotTo(HaveOccurred(),
			fmt.Sprintf("iteration %d/%d: failed to delete random redis pods", i, chaosIterations))
		Expect(deleted).NotTo(BeEmpty(),
			fmt.Sprintf("iteration %d/%d: expected at least one redis pod deletion", i, chaosIterations))
		GinkgoWriter.Printf("Deleted pods: %v\n", deleted)

		By(fmt.Sprintf("iteration %d/%d: waiting for cluster recovery", i, chaosIterations))
		Expect(framework.WaitForChaosReady(
			ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
		)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: cluster did not recover after pod deletion", i, chaosIterations))

		cluster, err := framework.GetRedkeyCluster(ctx, k8sClient, namespace, clusterName)
		Expect(err).NotTo(HaveOccurred(),
			fmt.Sprintf("iteration %d/%d: failed to get cluster after scale-up recovery", i, chaosIterations))
		Expect(cluster.Spec.Primaries).To(Equal(newSize),
			fmt.Sprintf("iteration %d/%d: expected spec.primaries=%d after scale-up, got %d",
				i, chaosIterations, newSize, cluster.Spec.Primaries))
		currentPrimaries = newSize

		stabilizeUnderLoad(namespace, fmt.Sprintf("iteration %d/%d: after scale-up and pod deletion", i, chaosIterations))

		By(fmt.Sprintf("iteration %d/%d: scaling cluster down", i, chaosIterations))
		downSize := int32(minPrimaries - rng.Intn(3))
		GinkgoWriter.Printf("Scaling down: %d -> %d primaries\n", currentPrimaries, downSize)
		Expect(framework.ScaleCluster(ctx, k8sClient, namespace, clusterName, downSize)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: failed to scale cluster down to %d", i, chaosIterations, downSize))

		Expect(framework.WaitForChaosReady(
			ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
		)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: cluster did not become ready after scaling down", i, chaosIterations))

		cluster, err = framework.GetRedkeyCluster(ctx, k8sClient, namespace, clusterName)
		Expect(err).NotTo(HaveOccurred(),
			fmt.Sprintf("iteration %d/%d: failed to get cluster after scale-down recovery", i, chaosIterations))
		Expect(cluster.Spec.Primaries).To(Equal(downSize),
			fmt.Sprintf("iteration %d/%d: expected spec.primaries=%d after scale-down, got %d",
				i, chaosIterations, downSize, cluster.Spec.Primaries))
		currentPrimaries = downSize

		stabilizeUnderLoad(namespace, fmt.Sprintf("iteration %d/%d: after scale-down", i, chaosIterations))
	}

	verifyK6Healthy(namespace)

	By("stopping k6 load")
	stopK6Load(namespace, k6DepName)

	By("verifying final cluster state")
	verifyClusterHealthy(namespace, clusterName)

	return k6DepName
}

// runOperatorDeletionChaos runs the operator-pod-deletion scenario.
func runOperatorDeletionChaos(rng *rand.Rand, namespace, clusterName string) string {
	By("starting k6 load deployment")
	k6DepName := startK6OrFail(namespace, clusterName)

	By(fmt.Sprintf("executing chaos with operator deletion (%d iterations)", chaosIterations))

	for i := 1; i <= chaosIterations; i++ {
		GinkgoWriter.Printf("=== Chaos iteration %d/%d ===\n", i, chaosIterations)

		By(fmt.Sprintf("iteration %d/%d: deleting operator pod", i, chaosIterations))
		Expect(framework.DeleteOperatorPods(ctx, k8sClientset, namespace)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: failed to delete operator pods", i, chaosIterations))

		By(fmt.Sprintf("iteration %d/%d: deleting random redis pods", i, chaosIterations))
		deleted, err := framework.DeleteRandomRedisPods(ctx, k8sClientset, namespace, clusterName, 2, rng)
		Expect(err).NotTo(HaveOccurred(),
			fmt.Sprintf("iteration %d/%d: failed to delete random redis pods", i, chaosIterations))
		Expect(deleted).NotTo(BeEmpty(),
			fmt.Sprintf("iteration %d/%d: expected at least one redis pod deletion", i, chaosIterations))
		GinkgoWriter.Printf("Deleted pods: %v\n", deleted)

		By(fmt.Sprintf("iteration %d/%d: waiting for recovery", i, chaosIterations))
		Expect(framework.WaitForChaosReady(
			ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
		)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: cluster did not recover after operator deletion", i, chaosIterations))

		stabilizeUnderLoad(namespace, fmt.Sprintf("iteration %d/%d: after operator deletion", i, chaosIterations))
	}

	verifyK6Healthy(namespace)

	By("stopping k6 load")
	stopK6Load(namespace, k6DepName)

	By("verifying final cluster state")
	Expect(framework.WaitForChaosReady(
		ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
	)).To(Succeed())

	return k6DepName
}

// runRobinDeletionChaos runs the robin-pod-deletion scenario.
func runRobinDeletionChaos(rng *rand.Rand, namespace, clusterName string) string {
	By("starting k6 load deployment")
	k6DepName := startK6OrFail(namespace, clusterName)

	By(fmt.Sprintf("executing chaos with robin deletion (%d iterations)", chaosIterations))

	for i := 1; i <= chaosIterations; i++ {
		GinkgoWriter.Printf("=== Chaos iteration %d/%d ===\n", i, chaosIterations)

		By(fmt.Sprintf("iteration %d/%d: deleting robin pods", i, chaosIterations))
		Expect(framework.DeleteRobinPods(ctx, k8sClientset, namespace, clusterName)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: failed to delete robin pods", i, chaosIterations))

		By(fmt.Sprintf("iteration %d/%d: deleting random redis pods", i, chaosIterations))
		deletedRedis, err := framework.DeleteRandomRedisPods(ctx, k8sClientset, namespace, clusterName, 2, rng)
		Expect(err).NotTo(HaveOccurred(),
			fmt.Sprintf("iteration %d/%d: failed to delete random redis pods", i, chaosIterations))
		Expect(deletedRedis).NotTo(BeEmpty(),
			fmt.Sprintf("iteration %d/%d: expected at least one redis pod deletion", i, chaosIterations))
		GinkgoWriter.Printf("Deleted pods: %v\n", deletedRedis)

		By(fmt.Sprintf("iteration %d/%d: waiting for recovery", i, chaosIterations))
		Expect(framework.WaitForChaosReady(
			ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
		)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: cluster did not recover after robin deletion", i, chaosIterations))

		stabilizeUnderLoad(namespace, fmt.Sprintf("iteration %d/%d: after robin deletion", i, chaosIterations))
	}

	verifyK6Healthy(namespace)

	By("stopping k6 load")
	stopK6Load(namespace, k6DepName)

	By("verifying final cluster state")
	Expect(framework.WaitForChaosReady(
		ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
	)).To(Succeed())

	return k6DepName
}

// runFullChaos fires multiple overlapping actions per iteration (operator/robin/redis deletion and
// scaling) without waiting for recovery between them, testing healing from accumulated failures.
func runFullChaos(rng *rand.Rand, namespace, clusterName string) string {
	By("starting k6 load deployment")
	k6DepName := startK6OrFail(namespace, clusterName)

	By(fmt.Sprintf("executing full chaos (%d iterations)", chaosIterations))

	currentPrimaries := int32(defaultPrimaries)
	var scaled bool

	for i := 1; i <= chaosIterations; i++ {
		GinkgoWriter.Printf("=== Full chaos iteration %d/%d ===\n", i, chaosIterations)
		scaled = false

		if rng.Intn(2) == 0 {
			By(fmt.Sprintf("iteration %d/%d: deleting operator pod", i, chaosIterations))
			Expect(framework.DeleteOperatorPods(ctx, k8sClientset, namespace)).To(Succeed(),
				fmt.Sprintf("iteration %d/%d: failed to delete operator pods", i, chaosIterations))
		}

		if rng.Intn(2) == 0 {
			By(fmt.Sprintf("iteration %d/%d: deleting robin pods", i, chaosIterations))
			Expect(framework.DeleteRobinPods(ctx, k8sClientset, namespace, clusterName)).To(Succeed(),
				fmt.Sprintf("iteration %d/%d: failed to delete robin pods", i, chaosIterations))
		}

		if rng.Intn(2) == 0 {
			By(fmt.Sprintf("iteration %d/%d: deleting random redis pods", i, chaosIterations))
			deleted, err := framework.DeleteRandomRedisPods(ctx, k8sClientset, namespace, clusterName, 2, rng)
			Expect(err).NotTo(HaveOccurred(),
				fmt.Sprintf("iteration %d/%d: failed to delete random redis pods", i, chaosIterations))
			Expect(deleted).NotTo(BeEmpty(),
				fmt.Sprintf("iteration %d/%d: expected at least one redis pod deletion", i, chaosIterations))
			GinkgoWriter.Printf("Deleted pods: %v\n", deleted)
		}

		var newSize int32
		if rng.Intn(2) == 0 {
			By(fmt.Sprintf("iteration %d/%d: scaling cluster", i, chaosIterations))
			newSize = int32(rng.Intn(maxPrimaries-minPrimaries+1) + minPrimaries)
			GinkgoWriter.Printf("Scaling: %d -> %d primaries\n", currentPrimaries, newSize)
			Expect(framework.ScaleCluster(ctx, k8sClient, namespace, clusterName, newSize)).To(Succeed(),
				fmt.Sprintf("iteration %d/%d: failed to scale cluster to %d", i, chaosIterations, newSize))
			scaled = true
		}

		By(fmt.Sprintf("iteration %d/%d: waiting for recovery", i, chaosIterations))
		Expect(framework.WaitForChaosReady(
			ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
		)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: cluster did not recover after chaos actions", i, chaosIterations))

		if scaled {
			cluster, err := framework.GetRedkeyCluster(ctx, k8sClient, namespace, clusterName)
			Expect(err).NotTo(HaveOccurred(),
				fmt.Sprintf("iteration %d/%d: failed to get cluster after recovery", i, chaosIterations))
			Expect(cluster.Spec.Primaries).To(Equal(newSize),
				fmt.Sprintf("iteration %d/%d: expected spec.primaries=%d after scaling, got %d",
					i, chaosIterations, newSize, cluster.Spec.Primaries))
			currentPrimaries = newSize
		}

		stabilizeUnderLoad(namespace, fmt.Sprintf("iteration %d/%d: after chaos actions", i, chaosIterations))
	}

	verifyK6Healthy(namespace)

	By("stopping k6 load")
	stopK6Load(namespace, k6DepName)

	By("verifying final cluster state")
	verifyClusterHealthy(namespace, clusterName)

	return k6DepName
}

// collectDiagnostics collects logs and state for debugging failed tests.
func collectDiagnostics(namespace string) {
	GinkgoWriter.Printf("\n=== COLLECTING DIAGNOSTICS FOR NAMESPACE %s ===\n", namespace)

	cluster, err := framework.GetRedkeyCluster(ctx, k8sClient, namespace, clusterName)
	if err == nil {
		GinkgoWriter.Printf("Cluster phase: %s (status: %s)\n", cluster.Status.Phase, cluster.Status.Status)
		GinkgoWriter.Printf("Cluster conditions: %+v\n", cluster.Status.Conditions)
	}

	pods, err := k8sClientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err == nil {
		GinkgoWriter.Printf("\nPods in namespace:\n")
		for _, pod := range pods.Items {
			GinkgoWriter.Printf("  %s: Phase=%s\n", pod.Name, pod.Status.Phase)
		}
	}

	GinkgoWriter.Printf("\n--- Operator Pod Logs (last %d lines) ---\n", diagnosticsLogTail)
	if operatorLogs, lerr := framework.GetPodLogs(
		ctx, k8sClientset, namespace, framework.OperatorPodsSelector(), diagnosticsLogTail,
	); lerr == nil {
		GinkgoWriter.Printf("%s\n", operatorLogs)
	} else {
		GinkgoWriter.Printf("Failed to get operator logs: %v\n", lerr)
	}

	GinkgoWriter.Printf("\n--- Redis Pod Logs (last %d lines, first pod) ---\n", diagnosticsLogTail)
	if redisLogs, lerr := framework.GetPodLogs(
		ctx, k8sClientset, namespace, framework.RedisPodsSelector(clusterName), diagnosticsLogTail,
	); lerr == nil {
		GinkgoWriter.Printf("%s\n", redisLogs)
	} else {
		GinkgoWriter.Printf("Failed to get redis logs: %v\n", lerr)
	}

	GinkgoWriter.Printf("\n--- Robin Pod Logs (last %d lines) ---\n", diagnosticsLogTail)
	if robinLogs, lerr := framework.GetPodLogs(
		ctx, k8sClientset, namespace, framework.RobinPodsSelector(clusterName), diagnosticsLogTail,
	); lerr == nil {
		GinkgoWriter.Printf("%s\n", robinLogs)
	} else {
		GinkgoWriter.Printf("Failed to get robin logs: %v\n", lerr)
	}

	GinkgoWriter.Printf("=== END DIAGNOSTICS ===\n\n")
}
