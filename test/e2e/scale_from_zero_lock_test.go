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
)

// expectScaleUpRejected attempts to change the topology and asserts the API server rejects it
// (the scale-up-from-zero CEL rule), and that the rejection message names the expected topology.
func expectScaleUpRejected(
	ctx context.Context, key types.NamespacedName, mutate func(*redkeyv1beta1.Redkey), msgSubstrings ...string,
) {
	cluster := &redkeyv1beta1.Redkey{}
	Expect(k8sClient.Get(ctx, key, cluster)).To(Succeed())
	mutate(cluster)
	err := k8sClient.Update(ctx, cluster)
	Expect(err).To(HaveOccurred(), "scale-up from zero to a mismatched topology must be rejected")
	for _, s := range msgSubstrings {
		Expect(err.Error()).To(ContainSubstring(s))
	}
}

// waitForLastAppliedPrimaries polls until the operator has recorded the given last-applied topology
// in the Redkey status (continuous tracking of storage clusters running at primaries>0).
func waitForLastAppliedPrimaries(ctx context.Context, key types.NamespacedName, primaries, replicas int32) {
	Eventually(func(g Gomega) {
		cluster := &redkeyv1beta1.Redkey{}
		g.Expect(k8sClient.Get(ctx, key, cluster)).To(Succeed())
		g.Expect(cluster.Status.LastAppliedPrimaries).To(Equal(primaries))
		g.Expect(cluster.Status.LastAppliedReplicasPerPrimary).To(Equal(replicas))
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())
}

var _ = Describe("Scale-up-from-zero topology lock", Ordered, Label("scale-to-zero"), func() {
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
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-scale-zero-lock")
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

	// --- Storage cluster without replicas: lock on primaries ---

	Context("Storage cluster (3 primaries) scaled to zero and back", func() {
		const clusterName = "lock-storage-3"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary storage cluster and records the applied topology", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 3
			cluster := opts.BuildRedkey()
			cluster.Spec.DeletePVC = new(false)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
			waitForLastAppliedPrimaries(ctx, key(), 3, 0)
		})

		It("scales down to 0 primaries", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 0
			})
			waitForScaledToZero(ctx, clusterName, clusterNs)
		})

		It("rejects scaling up from zero to fewer primaries", func() {
			expectScaleUpRejected(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 2
			}, "restore the same primaries")
		})

		It("rejects scaling up from zero to more primaries", func() {
			expectScaleUpRejected(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 5
			}, "restore the same primaries")
		})

		It("does not mention replicas in the rejection message when there are none", func() {
			cluster := &redkeyv1beta1.Redkey{}
			Expect(k8sClient.Get(ctx, key(), cluster)).To(Succeed())
			cluster.Spec.Primaries = 4
			err := k8sClient.Update(ctx, cluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(".status.lastAppliedPrimaries"))
			Expect(err.Error()).NotTo(ContainSubstring("replicasPerPrimary"))
		})

		It("accepts scaling up from zero back to the exact previous topology", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 3
			})
			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})
	})

	// --- Storage cluster with replicas: lock on primaries AND replicas ---

	Context("Storage cluster (3 primaries / 1 replica) scaled to zero and back", func() {
		const clusterName = "lock-storage-3-1"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary/1-replica storage cluster and records the applied topology", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi").WithReplicas(1)
			opts.Primaries = 3
			cluster := opts.BuildRedkey()
			cluster.Spec.DeletePVC = new(false)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// 3 primaries + 3 replicas = 6 nodes
			waitForScaledCluster(ctx, clusterName, clusterNs, 6)
			waitForLastAppliedPrimaries(ctx, key(), 3, 1)
		})

		It("scales down to 0 primaries", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 0
			})
			waitForScaledToZero(ctx, clusterName, clusterNs)
		})

		It("rejects scaling up from zero with a different replica count", func() {
			expectScaleUpRejected(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 3
				c.Spec.ReplicasPerPrimary = 2
			}, "restore the same replicasPerPrimary")
		})

		It("accepts scaling up from zero back to the exact previous primaries and replicas", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 3
				c.Spec.ReplicasPerPrimary = 1
			})
			waitForScaledCluster(ctx, clusterName, clusterNs, 6)
		})
	})

	// --- Ephemeral clusters are never locked ---

	Context("Ephemeral cluster scaled to zero and back to a different topology", func() {
		const clusterName = "lock-ephemeral-free"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary ephemeral cluster", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())
			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})

		It("does not record any last-applied topology for ephemeral clusters", func() {
			cluster := &redkeyv1beta1.Redkey{}
			Expect(k8sClient.Get(ctx, key(), cluster)).To(Succeed())
			Expect(cluster.Status.LastAppliedPrimaries).To(Equal(int32(0)))
		})

		It("scales down to 0 primaries", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 0
			})
			waitForScaledToZero(ctx, clusterName, clusterNs)
		})

		It("allows scaling up from zero to a different topology (no lock)", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 5
			})
			waitForScaledCluster(ctx, clusterName, clusterNs, 5)
		})
	})
})
