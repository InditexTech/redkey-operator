// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package chaos

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/inditextech/redkey-operator/test/chaos/framework"
)

const (
	clusterName      = "redis-cluster"
	defaultPrimaries = 5

	diagnosticsLogTail = int64(100)

	minPrimaries = 3
	maxPrimaries = 10
	defaultVUs   = 10

	// k6ProgressTimeout bounds how long the k6 load generator is given to show forward progress
	// once the cluster has recovered. A frozen generator (e.g. stuck on stale topology) never
	// advances within this window and fails the spec.
	k6ProgressTimeout = 60 * time.Second

	// maxActionsPerOperation is the number of pod deletions the disruptor injects per operation before
	// it goes quiet, so the operation gets a calm window to fully converge. It proves the cluster
	// survives a bounded burst of disruption AND, given time, reaches a stable Ready state — the
	// realistic contract, since a large nopurge slot migration cannot complete while pods are deleted
	// (and lose their data) faster than the migration progresses.
	maxActionsPerOperation = 5

	// disruptBudgetTimeout bounds how long a scenario waits for the disruptor to spend its
	// per-operation budget. Under gating the budget is spent during the slot-movement phase, which
	// recurs as each deletion knocks the cluster back into it; this is only a safety net so a spec
	// never blocks forever if that phase is never reached.
	disruptBudgetTimeout = 8 * time.Minute

	// minTotalDisruptions is the floor of disruptions a scenario must accumulate; it proves the
	// disruptor ran throughout the scenario rather than acting once.
	minTotalDisruptions = 2
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
	_, err = framework.CreateRedkey(ctx, k8sClient, opts)
	Expect(err).NotTo(HaveOccurred(), "failed to create Redkey")

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

// verifyLoadResumed asserts the k6 load generator resumes forward progress against the freshly
// recovered cluster — it refreshed the topology and is inserting keys again. Reaching Ready is a
// server-side signal (Robin's aggregated health conditions); this is the client-side counterpart that
// proves a real Redis client can route and write after the recovery. The cluster has already fully
// converged by the time this runs (WaitForChaosReady blocks on convergence beforehand), so this only
// checks client liveness — it does not wait for or contribute to convergence. The background
// disruptor is expected to be paused by the caller.
func verifyLoadResumed(namespace, phase string) {
	By(fmt.Sprintf("verifying client traffic resumed after recovery (%s)", phase))
	Expect(framework.WaitForK6Progress(ctx, k8sClientset, namespace, k6ProgressTimeout)).To(Succeed(),
		"k6 made no forward progress after recovery (%s)", phase)
}

// verifyClusterHealthy runs all cluster health checks.
func verifyClusterHealthy(namespace, clusterName string) {
	By("verifying cluster readiness")
	Expect(framework.WaitForChaosReady(
		ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
	)).To(Succeed())

	By("verifying topology is stable (no in-flight resharding)")
	Expect(framework.AssertTopologyStable(ctx, k8sClientset, namespace, clusterName, chaosReadyTimeout)).To(Succeed())

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

// disruptor runs a single long-lived goroutine that repeatedly performs a scenario-specific
// pod-deletion action against the cluster while it is not paused. It provides continuous chaos
// throughout an operation and its recovery (the M2 model): it stays active while a scaling/deletion
// operation is in flight and is only paused around the verification+calm checkpoint so the cluster
// can reach a stable Ready state (WaitForChaosReady cannot converge while pods keep being deleted).
//
// It is designed to be safe inside Ginkgo: the goroutine never calls Expect/Fail — a failure raised
// from a non-spec goroutine would be lost — so it tolerates transient errors (pods momentarily
// gone, list churn), logs them via GinkgoWriter and only counts successful actions. The main spec
// asserts progress through the recorded action count. The goroutine owns its own *rand.Rand because
// math/rand is not safe for concurrent use with the main loop's generator.
type disruptor struct {
	name   string
	action func(*rand.Rand) (int, error)
	// baseInterval is the cadence between pod deletions (with jitter). gate, when non-nil, restricts
	// deletions to the moments it returns true — used to land the bounded burst specifically during
	// the cluster's slot-movement phase (rebalance/drain/forming).
	baseInterval time.Duration
	gate         func() bool
	rng          *rand.Rand

	mu      sync.Mutex
	paused  bool
	budget  int // remaining deletions for the current operation; <=0 means quiet until re-armed
	actions int

	armCh  chan struct{} // buffered(1): arm() signals run() to fire one immediate ungated disruption
	stopCh chan struct{}
	doneCh chan struct{}
}

// startDisruptor launches a disruptor in the paused state with an empty budget. Callers arm it with a
// per-operation budget around each operation and pause it before verifying recovery. When gate is
// non-nil the disruptor only deletes while gate() is true, so its bounded burst lands specifically
// during the cluster's slot-movement phase; once the budget is spent it goes quiet, giving the
// operation a calm window to converge.
func startDisruptor(
	name string,
	seed int64,
	action func(*rand.Rand) (int, error),
	gate func() bool,
) *disruptor {
	d := &disruptor{
		name:         name,
		action:       action,
		baseInterval: framework.GetDisruptionInterval(),
		gate:         gate,
		rng:          rand.New(rand.NewSource(seed)),
		paused:       true,
		armCh:        make(chan struct{}, 1),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	go d.run()
	return d
}

func (d *disruptor) run() {
	defer GinkgoRecover()
	defer close(d.doneCh)

	for {
		select {
		case <-d.stopCh:
			return
		case <-ctx.Done():
			return
		case <-d.armCh:
			// Guaranteed per-operation disruption: fire one deletion immediately when armed, ungated,
			// so every operation is disrupted even when it converges before the gated interval or the
			// slot-movement window can land a hit (fast purge scaling converges in seconds).
			d.act(true)
		case <-time.After(d.jitteredInterval()):
			d.act(false)
		}
	}
}

// act performs one disruption if the disruptor is armed and still has budget. When ungated is false it
// also requires the gate (if any) to be open, so the gated burst lands specifically during the target
// phase. All rng/action use stays on the run() goroutine, so callers must never invoke it directly.
func (d *disruptor) act(ungated bool) {
	d.mu.Lock()
	skip := d.paused || d.budget <= 0
	d.mu.Unlock()
	if skip {
		return
	}
	// Gate the deletion to the target phase without consuming budget while waiting for it, so the
	// whole burst lands where it matters (e.g. during slot movement). The guaranteed on-arm hit
	// bypasses the gate.
	if !ungated && d.gate != nil && !d.gate() {
		return
	}

	n, err := d.action(d.rng)
	if err != nil {
		GinkgoWriter.Printf("[disruptor %s] action error (tolerated): %v\n", d.name, err)
		return
	}

	d.mu.Lock()
	d.actions++
	d.budget--
	total := d.actions
	d.mu.Unlock()
	GinkgoWriter.Printf("[disruptor %s] deleted %d pod(s) (total actions=%d)\n", d.name, n, total)
}

// jitteredInterval returns the base interval with +/-25% jitter so deletions are not perfectly
// periodic.
func (d *disruptor) jitteredInterval() time.Duration {
	base := int64(d.baseInterval)
	jitter := d.rng.Int63n(base/2+1) - base/4
	return time.Duration(base + jitter)
}

// arm resumes the disruptor with a fresh per-operation deletion budget (maxActionsPerOperation) and
// triggers one immediate ungated disruption so the operation is disrupted regardless of how fast it
// converges. The rest of the budget is spent by the gated ticker during the sensitive phase. Once the
// budget is spent the disruptor goes quiet on its own, leaving the operation a calm window to converge.
func (d *disruptor) arm() {
	d.mu.Lock()
	d.paused = false
	d.budget = maxActionsPerOperation
	d.mu.Unlock()
	// Signal run() to fire one immediate ungated hit. Non-blocking: the buffered channel already
	// holds a pending trigger if run() has not consumed the previous one yet, which is enough.
	select {
	case d.armCh <- struct{}{}:
	default:
	}
}

func (d *disruptor) pause() {
	d.mu.Lock()
	d.paused = true
	d.mu.Unlock()
}

func (d *disruptor) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.actions
}

// remainingBudget returns how many deletions of the current operation's budget are still unspent.
func (d *disruptor) remainingBudget() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.budget
}

// stop terminates the goroutine and waits for it to exit. Safe to call once.
func (d *disruptor) stop() {
	select {
	case <-d.stopCh:
	default:
		close(d.stopCh)
	}
	<-d.doneCh
}

// awaitBudget blocks until the disruptor has spent its per-operation budget (its burst has fully
// landed), the cluster has converged early, or a safety timeout elapses. Gating means the budget is
// spent during the slot-movement phase, which recurs as each deletion knocks the cluster back into
// it; the timeout is only a safeguard so a spec never blocks forever. It never fails: the
// end-of-scenario minTotalDisruptions assertion is what proves disruption actually happened.
//
// converged, when non-nil, is a single-shot convergence check. For a gated disruptor a converged
// cluster means the slot-movement phase is over and the gate can no longer open, so the remaining
// budget would never be spent — waiting on it only burns the safety timeout. In that case awaitBudget
// returns immediately instead of blocking for the full disruptBudgetTimeout. The early exit is
// deliberately restricted to gated disruptors: an ungated disruptor keeps firing into a steady
// cluster, so "converged" is its normal between-deletions state and must not cut its burst short.
func (d *disruptor) awaitBudget(converged func() bool) {
	deadline := time.Now().Add(disruptBudgetTimeout)
	for d.remainingBudget() > 0 && time.Now().Before(deadline) {
		if d.gate != nil && converged != nil && converged() {
			GinkgoWriter.Printf("[disruptor %s] cluster converged with %d of %d budget unspent; proceeding early\n",
				d.name, d.remainingBudget(), maxActionsPerOperation)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	if rem := d.remainingBudget(); rem > 0 {
		GinkgoWriter.Printf("[disruptor %s] budget not fully spent within %s (%d of %d remaining); proceeding\n",
			d.name, disruptBudgetTimeout, rem, maxActionsPerOperation)
	}
}

// redisDisruption returns an action that deletes a single random redis pod per tick.
func redisDisruption(namespace, clusterName string) func(*rand.Rand) (int, error) {
	return func(r *rand.Rand) (int, error) {
		deleted, err := framework.DeleteRandomRedisPods(ctx, k8sClientset, namespace, clusterName, 1, r)
		return len(deleted), err
	}
}

// operatorDisruption returns an action that deletes the operator pods and a random redis pod per
// tick, keeping the scenario focused on operator restarts while still churning the data plane.
func operatorDisruption(namespace, clusterName string) func(*rand.Rand) (int, error) {
	return func(r *rand.Rand) (int, error) {
		if err := framework.DeleteOperatorPods(ctx, k8sClientset, namespace); err != nil {
			return 0, err
		}
		deleted, _ := framework.DeleteRandomRedisPods(ctx, k8sClientset, namespace, clusterName, 1, r)
		return 1 + len(deleted), nil
	}
}

// robinDisruption returns an action that deletes the robin pods and a random redis pod per tick.
func robinDisruption(namespace, clusterName string) func(*rand.Rand) (int, error) {
	return func(r *rand.Rand) (int, error) {
		if err := framework.DeleteRobinPods(ctx, k8sClientset, namespace, clusterName); err != nil {
			return 0, err
		}
		deleted, _ := framework.DeleteRandomRedisPods(ctx, k8sClientset, namespace, clusterName, 1, r)
		return 1 + len(deleted), nil
	}
}

// mixedDisruption returns an action that randomly deletes operator, robin or redis pods per tick,
// used by the full-chaos scenario alongside periodic scaling.
func mixedDisruption(namespace, clusterName string) func(*rand.Rand) (int, error) {
	return func(r *rand.Rand) (int, error) {
		switch r.Intn(3) {
		case 0:
			if err := framework.DeleteOperatorPods(ctx, k8sClientset, namespace); err != nil {
				return 0, err
			}
			return 1, nil
		case 1:
			if err := framework.DeleteRobinPods(ctx, k8sClientset, namespace, clusterName); err != nil {
				return 0, err
			}
			return 1, nil
		default:
			deleted, err := framework.DeleteRandomRedisPods(ctx, k8sClientset, namespace, clusterName, 1, r)
			return len(deleted), err
		}
	}
}

// runScalingChaos runs the continuous-scaling-and-pod-deletion scenario.
func runScalingChaos(rng *rand.Rand, namespace, clusterName string, purgeKeysOnRebalance bool) string {
	By("starting k6 load deployment")
	k6DepName := startK6OrFail(namespace, clusterName)

	By("starting background redis-pod disruptor")
	d := startDisruptor("redis", chaosSeed+1, redisDisruption(namespace, clusterName),
		func() bool { return framework.IsInSlotMovementPhase(ctx, k8sClient, namespace, clusterName) })
	defer d.stop()

	By(fmt.Sprintf("executing chaos loop (%d iterations)", chaosIterations))

	currentPrimaries := int32(defaultPrimaries)

	// converged short-circuits awaitBudget: once the scaling operation has fully settled the
	// slot-movement phase the disruptor gates on can no longer recur, so waiting for the rest of the
	// budget would only burn the safety timeout.
	converged := func() bool {
		return framework.IsChaosConverged(ctx, k8sClient, k8sClientset, namespace, clusterName)
	}

	for i := 1; i <= chaosIterations; i++ {
		GinkgoWriter.Printf("=== Chaos iteration %d/%d ===\n", i, chaosIterations)

		newSize := int32(rng.Intn(maxPrimaries-minPrimaries+1) + minPrimaries)
		scaleDir := scaleDirection(currentPrimaries, newSize)
		By(fmt.Sprintf("iteration %d/%d: scaling cluster %s under continuous disruption", i, chaosIterations, scaleDir))
		GinkgoWriter.Printf("Scaling %s: %d -> %d primaries\n", scaleDir, currentPrimaries, newSize)
		d.arm()
		Expect(framework.ScaleCluster(ctx, k8sClient, namespace, clusterName, newSize)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: failed to scale cluster %s to %d", i, chaosIterations, scaleDir, newSize))

		// With purge enabled the StatefulSet is recreated on scaling, so wait for the new one to
		// reflect the target replica count. The disruptor keeps deleting pods throughout.
		if purgeKeysOnRebalance {
			Expect(framework.WaitForScaleAck(ctx, k8sClient, namespace, clusterName, newSize)).To(Succeed(),
				fmt.Sprintf("iteration %d/%d: StatefulSet did not acknowledge scale to %d", i, chaosIterations, newSize))
		}

		d.awaitBudget(converged)
		d.pause()
		By(fmt.Sprintf("iteration %d/%d: waiting for convergence in the calm after the disruption burst", i, chaosIterations))
		Expect(framework.WaitForChaosReady(
			ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
		)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: cluster did not converge after disruption (scale-up)", i, chaosIterations))
		d.pause()

		cluster, err := framework.GetRedkey(ctx, k8sClient, namespace, clusterName)
		Expect(err).NotTo(HaveOccurred(),
			fmt.Sprintf("iteration %d/%d: failed to get cluster after scale-up recovery", i, chaosIterations))
		Expect(cluster.Spec.Primaries).To(Equal(newSize),
			fmt.Sprintf("iteration %d/%d: expected spec.primaries=%d after scale-up, got %d",
				i, chaosIterations, newSize, cluster.Spec.Primaries))
		currentPrimaries = newSize

		verifyLoadResumed(namespace, fmt.Sprintf("iteration %d/%d: after scale-up and pod deletion", i, chaosIterations))

		downSize := int32(minPrimaries - rng.Intn(3))
		By(fmt.Sprintf("iteration %d/%d: scaling cluster down under continuous disruption", i, chaosIterations))
		GinkgoWriter.Printf("Scaling down: %d -> %d primaries\n", currentPrimaries, downSize)
		d.arm()
		Expect(framework.ScaleCluster(ctx, k8sClient, namespace, clusterName, downSize)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: failed to scale cluster down to %d", i, chaosIterations, downSize))

		d.awaitBudget(converged)
		d.pause()
		Expect(framework.WaitForChaosReady(
			ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
		)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: cluster did not converge after disruption (scale-down)", i, chaosIterations))
		d.pause()

		cluster, err = framework.GetRedkey(ctx, k8sClient, namespace, clusterName)
		Expect(err).NotTo(HaveOccurred(),
			fmt.Sprintf("iteration %d/%d: failed to get cluster after scale-down recovery", i, chaosIterations))
		Expect(cluster.Spec.Primaries).To(Equal(downSize),
			fmt.Sprintf("iteration %d/%d: expected spec.primaries=%d after scale-down, got %d",
				i, chaosIterations, downSize, cluster.Spec.Primaries))
		currentPrimaries = downSize

		verifyLoadResumed(namespace, fmt.Sprintf("iteration %d/%d: after scale-down", i, chaosIterations))
	}

	By("stopping background disruptor")
	d.stop()
	Expect(d.count()).To(BeNumerically(">=", minTotalDisruptions),
		"disruptor performed only %d actions; expected continuous disruption", d.count())

	verifyK6Healthy(namespace)

	By("stopping k6 load")
	stopK6Load(namespace, k6DepName)

	By("verifying final cluster state")
	verifyClusterHealthy(namespace, clusterName)

	return k6DepName
}

// runPodDeletionChaos runs a pod-deletion scenario driven by a single focused background disruptor
// (operator+redis or robin+redis). The disruptor deletes pods continuously while the fault is being
// injected; each iteration it is hammered for a few actions, paused so the cluster can settle, and
// verified to recover, with a calm window in between.
func runPodDeletionChaos(
	namespace, clusterName, disruptorName string,
	seed int64,
	action func(*rand.Rand) (int, error),
	faultDesc string,
) string {
	By("starting k6 load deployment")
	k6DepName := startK6OrFail(namespace, clusterName)

	By(fmt.Sprintf("starting background %s disruptor", disruptorName))
	d := startDisruptor(disruptorName, seed, action, nil)
	defer d.stop()

	By(fmt.Sprintf("executing chaos with %s (%d iterations)", faultDesc, chaosIterations))

	for i := 1; i <= chaosIterations; i++ {
		GinkgoWriter.Printf("=== Chaos iteration %d/%d ===\n", i, chaosIterations)

		By(fmt.Sprintf("iteration %d/%d: injecting %s (bounded burst)", i, chaosIterations, faultDesc))
		d.arm()
		d.awaitBudget(nil)
		d.pause()

		By(fmt.Sprintf("iteration %d/%d: waiting for convergence in the calm after the disruption burst", i, chaosIterations))
		Expect(framework.WaitForChaosReady(
			ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
		)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: cluster did not converge after %s", i, chaosIterations, faultDesc))
		// disruptor already quiet (budget spent); explicit pause above keeps it so during recovery.

		verifyLoadResumed(namespace, fmt.Sprintf("iteration %d/%d: after %s", i, chaosIterations, faultDesc))
	}

	By("stopping background disruptor")
	d.stop()
	Expect(d.count()).To(BeNumerically(">=", minTotalDisruptions),
		"disruptor performed only %d actions; expected continuous disruption", d.count())

	verifyK6Healthy(namespace)

	By("stopping k6 load")
	stopK6Load(namespace, k6DepName)

	By("verifying final cluster state")
	Expect(framework.WaitForChaosReady(
		ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
	)).To(Succeed())

	return k6DepName
}

// runOperatorDeletionChaos runs the operator-pod-deletion scenario.
func runOperatorDeletionChaos(namespace, clusterName string) string {
	return runPodDeletionChaos(namespace, clusterName, "operator",
		chaosSeed+2, operatorDisruption(namespace, clusterName), "operator deletion")
}

// runRobinDeletionChaos runs the robin-pod-deletion scenario.
func runRobinDeletionChaos(namespace, clusterName string) string {
	return runPodDeletionChaos(namespace, clusterName, "robin",
		chaosSeed+3, robinDisruption(namespace, clusterName), "robin deletion")
}

// runFullChaos runs continuous mixed disruption (operator/robin/redis deletion) alongside periodic
// scaling, testing the operator's ability to heal from sustained, overlapping failures.
func runFullChaos(rng *rand.Rand, namespace, clusterName string) string {
	By("starting k6 load deployment")
	k6DepName := startK6OrFail(namespace, clusterName)

	By("starting background mixed disruptor")
	d := startDisruptor("mixed", chaosSeed+4, mixedDisruption(namespace, clusterName), nil)
	defer d.stop()

	By(fmt.Sprintf("executing full chaos (%d iterations)", chaosIterations))

	currentPrimaries := int32(defaultPrimaries)

	for i := 1; i <= chaosIterations; i++ {
		GinkgoWriter.Printf("=== Full chaos iteration %d/%d ===\n", i, chaosIterations)

		d.arm()

		var newSize int32
		scaled := false
		if rng.Intn(2) == 0 {
			By(fmt.Sprintf("iteration %d/%d: scaling cluster under continuous disruption", i, chaosIterations))
			newSize = int32(rng.Intn(maxPrimaries-minPrimaries+1) + minPrimaries)
			GinkgoWriter.Printf("Scaling: %d -> %d primaries\n", currentPrimaries, newSize)
			Expect(framework.ScaleCluster(ctx, k8sClient, namespace, clusterName, newSize)).To(Succeed(),
				fmt.Sprintf("iteration %d/%d: failed to scale cluster to %d", i, chaosIterations, newSize))
			scaled = true
		}

		d.awaitBudget(nil)
		d.pause()
		By(fmt.Sprintf("iteration %d/%d: waiting for convergence in the calm after the disruption burst", i, chaosIterations))
		Expect(framework.WaitForChaosReady(
			ctx, k8sClient, k8sClientset, namespace, clusterName, chaosReadyTimeout,
		)).To(Succeed(),
			fmt.Sprintf("iteration %d/%d: cluster did not converge after disruption", i, chaosIterations))
		d.pause()

		if scaled {
			cluster, err := framework.GetRedkey(ctx, k8sClient, namespace, clusterName)
			Expect(err).NotTo(HaveOccurred(),
				fmt.Sprintf("iteration %d/%d: failed to get cluster after recovery", i, chaosIterations))
			Expect(cluster.Spec.Primaries).To(Equal(newSize),
				fmt.Sprintf("iteration %d/%d: expected spec.primaries=%d after scaling, got %d",
					i, chaosIterations, newSize, cluster.Spec.Primaries))
			currentPrimaries = newSize
		}

		verifyLoadResumed(namespace, fmt.Sprintf("iteration %d/%d: after chaos actions", i, chaosIterations))
	}

	By("stopping background disruptor")
	d.stop()
	Expect(d.count()).To(BeNumerically(">=", minTotalDisruptions),
		"disruptor performed only %d actions; expected continuous disruption", d.count())

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

	cluster, err := framework.GetRedkey(ctx, k8sClient, namespace, clusterName)
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
