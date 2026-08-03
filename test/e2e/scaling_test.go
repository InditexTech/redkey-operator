// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	redkeyv1beta1 "github.com/inditextech/redkey-operator/api/v1beta1"
	"github.com/inditextech/redkey-operator/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// updateClusterTopology mutates a Redkey's spec with conflict-retry, used to
// trigger a scaling operation.
func updateClusterTopology(
	ctx context.Context, key types.NamespacedName, mutate func(*redkeyv1beta1.Redkey),
) {
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &redkeyv1beta1.Redkey{}
		if err := k8sClient.Get(ctx, key, cluster); err != nil {
			return err
		}
		mutate(cluster)
		return k8sClient.Update(ctx, cluster)
	})).To(Succeed())
}

// waitForScaledCluster waits until the active config is Applied, the cluster is Ready,
// the StatefulSet has exactly expectedNodes ready pods, and the Redis cluster reports
// a healthy state with the expected node count. It returns the current Redis pod names.
func waitForScaledCluster(
	ctx context.Context, clusterName, clusterNs string, expectedNodes int,
) []string {
	key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

	// Read the desired topology from the (already-updated) cluster spec so the config wait
	// can require the active config to reflect it. Without this, the wait could observe the
	// previous, already-Applied config during the window before the operator creates the
	// new config and return prematurely — letting a subsequent scaling spec start while
	// this operation is still in flight and starving its timeout budget.
	cluster := &redkeyv1beta1.Redkey{}
	Expect(k8sClient.Get(ctx, key, cluster)).To(Succeed())
	expectedPrimaries := int(cluster.Spec.Primaries)
	expectedReplicasPerPrimary := int(cluster.Spec.ReplicasPerPrimary)

	By(fmt.Sprintf("waiting for the active config to be Applied (target %d nodes)", expectedNodes))
	_, err := framework.WaitForActiveConfigAppliedTopology(ctx, k8sClient, clusterName, clusterNs,
		expectedPrimaries, expectedReplicasPerPrimary, framework.HealthTimeout)
	Expect(err).NotTo(HaveOccurred())

	By("waiting for the cluster to reach Ready")
	_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.HealthTimeout)
	Expect(err).NotTo(HaveOccurred())

	By(fmt.Sprintf("waiting for %d Redis pods to be ready", expectedNodes))
	err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
		framework.RedisPodLabels(clusterName), expectedNodes, framework.HealthTimeout)
	Expect(err).NotTo(HaveOccurred())

	podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedNodes)
	Expect(err).NotTo(HaveOccurred())

	By("verifying the Redis cluster is healthy after scaling")
	Eventually(func() error {
		return framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedNodes)
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())

	return podNames
}

var _ = Describe("Cluster Scaling", Ordered, Label("scaling"), func() {
	var (
		suiteCtx  context.Context
		ctx       context.Context
		ns        *corev1.Namespace
		clusterNs string
	)

	framework.SetupSpecContexts(&suiteCtx, &ctx, 20*time.Minute)

	BeforeAll(func() {
		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-scaling")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)
	})

	AfterAll(func() {
		By("cleaning up the test namespace")
		if ns != nil {
			_ = framework.DeleteNamespace(suiteCtx, k8sClient, ns)
		}
	})

	AfterEach(func() {
		framework.CollectDebugInfoOnFailure(k8sClient, clusterNs)
	})

	// --- Normal scaling: data is migrated and preserved across the operation. ---

	Context("Ephemeral cluster without replicas (normal rebalance)", func() {
		const clusterName = "scale-ephemeral-noreplica"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0
			opts.PurgeKeysOnRebalance = new(false) // force normal rebalance

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("inserting keys")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 50)).To(Succeed())
		})

		It("scales up 3→5 primaries preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 5
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 5)

			By("verifying data is preserved after scale up")
			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(50), "all keys should survive a normal scale up")
		})

		It("scales down 5→3 primaries preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 3
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("verifying data is preserved after scale down")
			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(50), "all keys should survive a normal scale down")
		})
	})

	// Regression: a scale-up used to stall forever when a leftover half-migration sat on a non-seed
	// node. The importing/migrating markers redis-cli writes are local to the node that holds them and
	// are not gossiped, so the rebalance seed's CLUSTER NODES view could not see the open slot; the
	// pre-rebalance repair only inspected the seed and skipped the fix, while redis-cli --cluster
	// rebalance (which checks every node) refused forever, leaving the new primaries empty. Robin now
	// probes every master's own view before rebalancing and force-repairs after any refused attempt.
	// This spec reproduces the exact condition deterministically: it injects the open slot on non-seed
	// nodes only after the scale-up is underway (so Robin is on the scale path and its Ready-state
	// health remediation, which would otherwise fix the slot, does not run) and asserts convergence.
	Context("Scale-up with a pre-existing open slot on a non-seed node (regression)", func() {
		const clusterName = "scale-openslot-regression"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("converges when a non-seed node holds a leftover open slot during scale-up", func() {
			By("creating a 3-primary ephemeral cluster (normal rebalance)")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0
			opts.PurgeKeysOnRebalance = new(false) // force normal, data-preserving rebalance

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("inserting keys so the scale-up performs real slot-with-data migration")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 50)).To(Succeed())

			By("requesting a scale-up to 5 primaries")
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 5
			})

			By("waiting until the scale-up is underway so Robin is on the scale path (no Ready-state healing)")
			_, err = framework.WaitForClusterStatus(ctx, k8sClient, key(),
				redkeyv1beta1.ClusterStatusScalingUp, framework.HealthTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("injecting a leftover open slot between two non-seed nodes while the new pods are still being added")
			// The rebalance seed is the lowest-ordinal existing primary (...-0); placing the open slot on
			// ...-1 -> ...-2 keeps it off the seed, reproducing the seed-blind detection. The new pods
			// (...-3, ...-4) take real time to start, so this lands well before the rebalance runs.
			slot, err := framework.SetSlotMigratingBetween(clusterNs, clusterName+"-1", clusterName+"-2")
			Expect(err).NotTo(HaveOccurred(), "failed to inject the open slot on non-seed nodes")
			_, _ = fmt.Fprintf(GinkgoWriter, "injected open slot %d: migrating on %s-1, importing on %s-2\n",
				slot, clusterName, clusterName)

			By("asserting the scale-up still converges to 5 healthy primaries despite the non-seed open slot")
			// With the old seed-only detection the rebalance refused forever on the invisible open slot
			// and this wait would time out; the fix lets the scale-up converge.
			podNames = waitForScaledCluster(ctx, clusterName, clusterNs, 5)

			By("verifying data survived the scale-up")
			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(50), "all keys should survive the scale-up despite the injected open slot")
		})
	})

	// Regression: a scale-up whose target primary count is already met by the existing primaries
	// (futurePrimaries == 0) used to loop forever in Verifying when the cluster had uncovered slots —
	// e.g. after a primary was killed mid-operation and its slots orphaned. The Phase-2 rebalance,
	// which repairs open/uncovered slots, is skipped when no new primaries need slots, so nothing
	// re-covered the keyspace and verifyCluster failed on every pass (observed as
	// WaitingForPods -> InitializingNodes -> Verifying, never reaching Ready). handleScalingUp now
	// repairs uncovered slots in that branch too. This reproduces the state deterministically: freeze
	// Robin (Maintenance), orphan slots with CLUSTER DELSLOTS, then drive Robin through the scale-up
	// path (ScalingUp) with the primary count already satisfied.
	Context("Scale-up with orphaned slots and no new primaries (regression)", func() {
		const clusterName = "scale-orphan-noverify"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("recovers instead of looping in Verifying when the target is already met", func() {
			By("creating a 3-primary ephemeral cluster (normal rebalance)")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0
			opts.PurgeKeysOnRebalance = new(false)

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("freezing Robin (Maintenance) so it cannot re-cover the slots we are about to orphan")
			Expect(framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs,
				redkeyv1beta1.ClusterStatusMaintenance)).To(Succeed())
			_, err = framework.WaitForClusterStatus(ctx, k8sClient, key(),
				redkeyv1beta1.ClusterStatusMaintenance, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("orphaning a range of slots via CLUSTER DELSLOTS (leaving the node a non-empty primary)")
			orphaned := make([]int, 200)
			for i := range orphaned {
				orphaned[i] = i
			}
			Expect(framework.DelSlots(clusterNs, podNames[0], orphaned)).To(Succeed())

			By("confirming the keyspace is now only partially covered")
			info, err := framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.SlotsAssigned).To(BeNumerically("<", 16384),
				"DELSLOTS should have left the cluster with uncovered slots")

			By("driving Robin through the scale-up path with the primary count already satisfied")
			// The target (3) already equals the existing slot-owning primaries, so handleScalingUp sees
			// futurePrimaries == 0 and skips the rebalance — the exact branch that used to loop.
			Expect(framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs,
				redkeyv1beta1.ClusterStatusScalingUp)).To(Succeed())

			By("asserting Robin repairs the coverage and the cluster reaches Ready instead of looping")
			// With the old code verifyCluster failed forever on the missing coverage and this would time
			// out; the fix repairs the uncovered slots and the scale-up completes.
			waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("verifying all slots are covered again")
			info, err = framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.SlotsAssigned).To(Equal(16384))
			Expect(info.State).To(Equal("ok"))
		})
	})

	Context("Ephemeral cluster with replicas (normal rebalance)", func() {
		const clusterName = "scale-ephemeral-replica"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 3
			opts.PurgeKeysOnRebalance = new(false)

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 40)).To(Succeed())
		})

		It("scales up 3→4 primaries (1 replica each) preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 4
			})

			// 4 primaries + 4 replicas = 8 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 8)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(40))
		})

		It("scales down 4→3 primaries (1 replica each) preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 3
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(40))
		})
	})

	Context("Persistent cluster without replicas (normal rebalance)", func() {
		const clusterName = "scale-persistent-noreplica"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary persistent cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 30)).To(Succeed())
		})

		It("scales up 3→5 primaries preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 5
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 5)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(30))
		})

		It("scales down 5→3 primaries preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 3
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(30))
		})
	})

	Context("Persistent cluster with replicas (normal rebalance)", func() {
		const clusterName = "scale-persistent-replica"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica persistent cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi").WithReplicas(1)
			opts.Primaries = 3

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 25)).To(Succeed())
		})

		It("scales up 3→5 primaries (1 replica each) preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 5
			})

			// 5 primaries + 5 replicas = 10 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 10)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(25))
		})

		It("scales down 5→3 primaries (1 replica each) preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 3
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(25))
		})
	})

	// --- Replica count scaling: tests that mutate replicasPerPrimary while keeping primaries fixed. ---

	Context("Ephemeral cluster — replica count scaling", func() {
		const clusterName = "scale-replica-ephemeral"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 3
			opts.PurgeKeysOnRebalance = new(false)

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 60)).To(Succeed())
		})

		It("scales replicas 1→2 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.ReplicasPerPrimary = 2
			})

			// 3 primaries + 6 replicas = 9 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 9)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(60), "all keys should survive replica scale up")
		})

		It("scales replicas 2→1 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.ReplicasPerPrimary = 1
			})

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(60), "all keys should survive replica scale down")
		})

		It("scales replicas 1→0 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.ReplicasPerPrimary = 0
			})

			// 3 primaries + 0 replicas = 3 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(60), "all keys should survive removing all replicas")
		})

		It("scales replicas 0→1 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.ReplicasPerPrimary = 1
			})

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(60), "all keys should survive adding replicas back")
		})
	})

	Context("Persistent cluster — replica count scaling", func() {
		const clusterName = "scale-replica-persistent"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica persistent cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi").WithReplicas(1)
			opts.Primaries = 3

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 35)).To(Succeed())
		})

		It("scales replicas 1→0 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.ReplicasPerPrimary = 0
			})

			// 3 primaries + 0 replicas = 3 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(35), "all keys should survive removing replicas on persistent cluster")
		})

		It("scales replicas 0→2 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.ReplicasPerPrimary = 2
			})

			// 3 primaries + 6 replicas = 9 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 9)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(35), "all keys should survive adding replicas on persistent cluster")
		})
	})

	// --- Edge case: replicas→0 with purge enabled must NOT trigger fast scaling. ---

	Context("Replicas to zero with purge enabled (no fast scaling)", func() {
		const clusterName = "scale-replica-purge-guard"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica cluster with purgeKeysOnRebalance=true", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 3
			opts.PurgeKeysOnRebalance = new(true)

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 45)).To(Succeed())
		})

		It("scales replicas 1→0 preserving data (fast scaling blocked by existing replicas)", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.ReplicasPerPrimary = 0
			})

			// 3 primaries + 0 replicas = 3 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("verifying data is preserved — fast scaling was NOT used despite purge=true")
			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(45), "data must survive: fast scaling is blocked when cluster has replicas")
		})
	})

	// --- Combined change: primaries and replicas mutated simultaneously. ---

	Context("Combined primaries and replicas change", func() {
		const clusterName = "scale-combined"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 3
			opts.PurgeKeysOnRebalance = new(false)

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 55)).To(Succeed())
		})

		It("scales 3P/1R → 5P/2R simultaneously preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 5
				c.Spec.ReplicasPerPrimary = 2
			})

			// 5 primaries + 10 replicas = 15 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 15)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(55), "all keys should survive a combined primaries+replicas scale up")
		})
	})

	// --- Fast scaling: cluster is recreated; data is intentionally purged. ---

	Context("Fast scaling (ephemeral, no replicas, purgeKeysOnRebalance)", func() {
		const clusterName = "scale-fast"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary cluster eligible for fast scaling", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0
			opts.PurgeKeysOnRebalance = new(true)

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 20)).To(Succeed())
		})

		It("fast scales up 3→6 primaries purging data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 6
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)

			By("verifying data was purged by fast scaling")
			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(0), "fast scaling recreates the cluster and purges all data")

			By("re-inserting keys for the next operation")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 15)).To(Succeed())
		})

		It("fast scales down 6→3 primaries purging data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 3
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(0), "fast scaling recreates the cluster and purges all data")
		})
	})

	// --- deletePVC: removed ordinals' PVCs are deleted on scale-down (deletePVC=true). ---
	// The StatefulSet PVC retention policy is set to WhenScaled=Delete for deletePVC=true, so the
	// volumes of the ordinals removed by a scale-down are cleaned up instead of lingering with
	// stale slot/node data.

	Context("Persistent cluster with deletePVC=true (scale-down deletes removed PVCs)", func() {
		const clusterName = "scale-deletepvc"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		listDataPVCNames := func() []string {
			pvcs := &corev1.PersistentVolumeClaimList{}
			Expect(k8sClient.List(ctx, pvcs,
				client.InNamespace(clusterNs),
				client.MatchingLabels(framework.RedisPodLabels(clusterName)),
			)).To(Succeed())
			names := make([]string, 0, len(pvcs.Items))
			for i := range pvcs.Items {
				names = append(names, pvcs.Items[i].Name)
			}
			return names
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary persistent cluster with deletePVC=true", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0
			cluster := opts.BuildRedkey()
			cluster.Spec.DeletePVC = new(true)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("verifying one PVC exists per primary ordinal")
			Expect(listDataPVCNames()).To(ConsistOf(
				"data-"+clusterName+"-0",
				"data-"+clusterName+"-1",
				"data-"+clusterName+"-2",
			))
		})

		It("deletes the removed ordinals' PVCs when scaling down 3→1", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 1
			})

			waitForScaledCluster(ctx, clusterName, clusterNs, 1)

			By("verifying only the surviving ordinal's PVC remains (removed PVCs deleted)")
			Eventually(listDataPVCNames, framework.HealthTimeout, framework.DefaultPollInterval).
				Should(ConsistOf("data-" + clusterName + "-0"))
		})
	})

	// --- deletePVC=false counterpart: removed ordinals' PVCs are retained on scale-down. ---
	// With deletePVC=false the StatefulSet PVC retention policy is WhenScaled=Retain, so the
	// volumes of the ordinals removed by a scale-down are preserved (data is kept).

	Context("Persistent cluster with deletePVC=false (scale-down retains removed PVCs)", func() {
		const clusterName = "scale-keeppvc"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		listDataPVCNames := func() []string {
			pvcs := &corev1.PersistentVolumeClaimList{}
			Expect(k8sClient.List(ctx, pvcs,
				client.InNamespace(clusterNs),
				client.MatchingLabels(framework.RedisPodLabels(clusterName)),
			)).To(Succeed())
			names := make([]string, 0, len(pvcs.Items))
			for i := range pvcs.Items {
				names = append(names, pvcs.Items[i].Name)
			}
			return names
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary persistent cluster with deletePVC=false", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0
			cluster := opts.BuildRedkey()
			cluster.Spec.DeletePVC = new(false)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("verifying one PVC exists per primary ordinal")
			Expect(listDataPVCNames()).To(ConsistOf(
				"data-"+clusterName+"-0",
				"data-"+clusterName+"-1",
				"data-"+clusterName+"-2",
			))
		})

		It("retains the removed ordinals' PVCs when scaling down 3→1", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 1
			})

			waitForScaledCluster(ctx, clusterName, clusterNs, 1)

			By("verifying all three PVCs remain (removed ordinals' PVCs retained)")
			// WhenScaled=Retain never deletes; poll briefly to prove no async deletion occurs.
			Consistently(listDataPVCNames, 15*time.Second, framework.DefaultPollInterval).
				Should(ConsistOf(
					"data-"+clusterName+"-0",
					"data-"+clusterName+"-1",
					"data-"+clusterName+"-2",
				))
		})
	})
})
