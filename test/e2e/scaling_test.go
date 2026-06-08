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

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
)

// updateClusterTopology mutates a RedkeyCluster's spec with conflict-retry, used to
// trigger a scaling operation.
func updateClusterTopology(
	ctx context.Context, key types.NamespacedName, mutate func(*redkeyv1beta1.RedkeyCluster),
) {
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &redkeyv1beta1.RedkeyCluster{}
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
	cluster := &redkeyv1beta1.RedkeyCluster{}
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
		ctx       context.Context
		cancel    context.CancelFunc
		ns        *corev1.Namespace
		clusterNs string
	)

	BeforeAll(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Minute)

		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-scaling")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)
	})

	AfterAll(func() {
		By("cleaning up the test namespace")
		if ns != nil {
			_ = framework.DeleteNamespace(ctx, k8sClient, ns)
		}
		cancel()
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			framework.CollectDebugInfo(ctx, k8sClient, clusterNs)
		}
	})

	// --- Normal scaling: data is migrated and preserved across the operation. ---

	Context("Ephemeral cluster without replicas (normal rebalance)", func() {
		const clusterName = "scale-ephemeral-noreplica"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0
			opts.PurgeKeysOnRebalance = ptr.To(false) // force normal rebalance

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("inserting keys")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 50)).To(Succeed())
		})

		It("scales up 3→5 primaries preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 5
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 5)

			By("verifying data is preserved after scale up")
			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(50), "all keys should survive a normal scale up")
		})

		It("scales down 5→3 primaries preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 3
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("verifying data is preserved after scale down")
			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(50), "all keys should survive a normal scale down")
		})
	})

	Context("Ephemeral cluster with replicas (normal rebalance)", func() {
		const clusterName = "scale-ephemeral-replica"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 3
			opts.PurgeKeysOnRebalance = ptr.To(false)

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 40)).To(Succeed())
		})

		It("scales up 3→4 primaries (1 replica each) preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 4
			})

			// 4 primaries + 4 replicas = 8 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 8)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(40))
		})

		It("scales down 4→3 primaries (1 replica each) preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
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
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary persistent cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 30)).To(Succeed())
		})

		It("scales up 3→5 primaries preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 5
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 5)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(30))
		})

		It("scales down 5→3 primaries preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
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
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica persistent cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi").WithReplicas(1)
			opts.Primaries = 3

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 25)).To(Succeed())
		})

		It("scales up 3→5 primaries (1 replica each) preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 5
			})

			// 5 primaries + 5 replicas = 10 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 10)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(25))
		})

		It("scales down 5→3 primaries (1 replica each) preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
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
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 3
			opts.PurgeKeysOnRebalance = ptr.To(false)

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 60)).To(Succeed())
		})

		It("scales replicas 1→2 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.ReplicasPerPrimary = 2
			})

			// 3 primaries + 6 replicas = 9 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 9)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(60), "all keys should survive replica scale up")
		})

		It("scales replicas 2→1 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.ReplicasPerPrimary = 1
			})

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(60), "all keys should survive replica scale down")
		})

		It("scales replicas 1→0 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.ReplicasPerPrimary = 0
			})

			// 3 primaries + 0 replicas = 3 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(60), "all keys should survive removing all replicas")
		})

		It("scales replicas 0→1 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
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
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica persistent cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi").WithReplicas(1)
			opts.Primaries = 3

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 35)).To(Succeed())
		})

		It("scales replicas 1→0 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.ReplicasPerPrimary = 0
			})

			// 3 primaries + 0 replicas = 3 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(35), "all keys should survive removing replicas on persistent cluster")
		})

		It("scales replicas 0→2 preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
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
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica cluster with purgeKeysOnRebalance=true", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 3
			opts.PurgeKeysOnRebalance = ptr.To(true)

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 45)).To(Succeed())
		})

		It("scales replicas 1→0 preserving data (fast scaling blocked by existing replicas)", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
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
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 3
			opts.PurgeKeysOnRebalance = ptr.To(false)

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			// 3 primaries + 3 replicas = 6 nodes.
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 55)).To(Succeed())
		})

		It("scales 3P/1R → 5P/2R simultaneously preserving data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
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
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary cluster eligible for fast scaling", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0
			opts.PurgeKeysOnRebalance = ptr.To(true)

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)
			Expect(framework.InsertKeys(clusterNs, podNames[0], 20)).To(Succeed())
		})

		It("fast scales up 3→6 primaries purging data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
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
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 3
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(size).To(Equal(0), "fast scaling recreates the cluster and purges all data")
		})
	})
})
