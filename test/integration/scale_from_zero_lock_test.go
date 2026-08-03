// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	redisv1 "github.com/inditextech/redkey-operator/api/v1beta1"
)

// These tests exercise the scale-up-from-zero topology lock for storage (non-ephemeral) clusters.
//
// The lock is a root-level CEL rule on the Redkey CRD (enforced by the envtest API server) that
// forces a scale-up from zero back to the exact previous topology (primaries + replicasPerPrimary).
// It reads status.lastAppliedPrimaries / status.lastAppliedReplicasPerPrimary, which the operator
// maintains CONTINUOUSLY while the cluster runs at primaries>0 (never only at scale-to-zero, never
// cleared). That continuous tracking is what makes the check RACE-FREE: the previous topology is
// persisted BEFORE any scale-to-zero, so the guard holds immediately without needing a
// post-scale-down reconcile. These tests assert both properties explicitly.
var _ = Describe("Scale-up-from-zero topology lock (CEL)", func() {
	const namespace = "default"
	ctx := context.Background()

	newStorageCluster := func(name string, primaries, replicas int32) *redisv1.Redkey {
		purgeKeys := false
		return &redisv1.Redkey{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: redisv1.RedkeySpec{
				Primaries:            primaries,
				ReplicasPerPrimary:   replicas,
				Ephemeral:            false,
				Storage:              "1Gi",
				StorageClassName:     "standard",
				AccessModes:          []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				PurgeKeysOnRebalance: &purgeKeys,
				Robin:                redisv1.RobinSpec{Image: "redkey-robin:latest"},
			},
		}
	}

	// bringToAppliedReady creates the cluster, reconciles it, marks its highest-sequence config
	// Applied+Ready, and reconciles again so the operator records the last applied topology.
	bringToAppliedReady := func(cluster *redisv1.Redkey) redisv1.Redkey {
		name := types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		reconcileCluster(ctx, name)

		configs := listConfigs(ctx, cluster.Name, namespace)
		Expect(configs).NotTo(BeEmpty())
		last := &configs[len(configs)-1]
		last.Status.ConfigPhase = redisv1.ConfigPhaseApplied
		last.Status.Status = redisv1.ClusterStatusReady
		updateConfigStatus(ctx, last)

		reconcileCluster(ctx, name)

		var updated redisv1.Redkey
		Expect(k8sClient.Get(ctx, name, &updated)).To(Succeed())
		return updated
	}

	// applyScaleTarget scales primaries (and replicas) via the highest applied config so the
	// operator records the new applied topology, mimicking a completed free scale while >0.
	updateSpec := func(name types.NamespacedName, mutate func(c *redisv1.Redkey)) error {
		var c redisv1.Redkey
		Expect(k8sClient.Get(ctx, name, &c)).To(Succeed())
		mutate(&c)
		return k8sClient.Update(ctx, &c)
	}

	Context("continuous tracking of the last applied topology", func() {
		It("records lastAppliedPrimaries once the cluster is applied at >0", func() {
			const name = "lock-track-record"
			cl := bringToAppliedReady(newStorageCluster(name, 3, 0))
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			Expect(cl.Status.LastAppliedPrimaries).To(Equal(int32(3)))
			Expect(cl.Status.LastAppliedReplicasPerPrimary).To(Equal(int32(0)))
		})

		It("updates lastAppliedPrimaries as the applied topology changes while >0", func() {
			const name = "lock-track-update"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			bringToAppliedReady(newStorageCluster(name, 3, 0))
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			// Free scale up to 5 while >0: a new config is created; it is not yet Applied, so the
			// recorded value must stay at 3 (we never capture an in-progress target).
			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 5 })).To(Succeed())
			reconcileCluster(ctx, nn)

			var midway redisv1.Redkey
			Expect(k8sClient.Get(ctx, nn, &midway)).To(Succeed())
			Expect(midway.Status.LastAppliedPrimaries).To(Equal(int32(3)),
				"in-progress scale target must not be recorded until applied")

			// Mark the new config Applied+Ready and reconcile: now the recorded value tracks to 5.
			configs := listConfigs(ctx, name, namespace)
			last := &configs[len(configs)-1]
			last.Status.ConfigPhase = redisv1.ConfigPhaseApplied
			last.Status.Status = redisv1.ClusterStatusReady
			updateConfigStatus(ctx, last)
			reconcileCluster(ctx, nn)

			var after redisv1.Redkey
			Expect(k8sClient.Get(ctx, nn, &after)).To(Succeed())
			Expect(after.Status.LastAppliedPrimaries).To(Equal(int32(5)))
		})

		It("does not track topology for ephemeral clusters", func() {
			const name = "lock-track-ephemeral"
			cluster := newTestCluster(name, namespace) // ephemeral, 3 primaries
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })
			reconcileCluster(ctx, nn)

			configs := listConfigs(ctx, name, namespace)
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			configs[0].Status.Status = redisv1.ClusterStatusReady
			updateConfigStatus(ctx, &configs[0])
			reconcileCluster(ctx, nn)

			var cl redisv1.Redkey
			Expect(k8sClient.Get(ctx, nn, &cl)).To(Succeed())
			Expect(cl.Status.LastAppliedPrimaries).To(Equal(int32(0)))
		})
	})

	Context("scale-up from zero is locked to the previous topology", func() {
		It("is race-free: the lock holds immediately after scale-to-zero, without a reconcile", func() {
			const name = "lock-race-free"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			bringToAppliedReady(newStorageCluster(name, 3, 0))
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			// Scale to zero (allowed).
			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 0 })).To(Succeed())

			// Immediately try to scale up to a WRONG number, WITHOUT running any reconcile after the
			// scale-to-zero. The value was recorded during the running phase, so the CEL rule must
			// already reject this — proving there is no admission-vs-reconcile race.
			err := updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 2 })
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(".status.lastAppliedPrimaries"))
		})

		It("rejects scaling up to fewer primaries", func() {
			const name = "lock-fewer"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			bringToAppliedReady(newStorageCluster(name, 3, 0))
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 0 })).To(Succeed())
			err := updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 2 })
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("restore the same primaries"))
		})

		It("rejects scaling up to more primaries", func() {
			const name = "lock-more"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			bringToAppliedReady(newStorageCluster(name, 3, 0))
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 0 })).To(Succeed())
			err := updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 5 })
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("restore the same primaries"))
		})

		It("accepts scaling up to the exact previous number of primaries", func() {
			const name = "lock-exact"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			bringToAppliedReady(newStorageCluster(name, 3, 0))
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 0 })).To(Succeed())
			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 3 })).To(Succeed())
		})

		It("reports the primaries rule (not replicas) when only primaries mismatch", func() {
			const name = "lock-msg-noreplicas"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			bringToAppliedReady(newStorageCluster(name, 3, 0))
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 0 })).To(Succeed())
			err := updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 2 })
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(".status.lastAppliedPrimaries"))
			Expect(err.Error()).NotTo(ContainSubstring("replicasPerPrimary"))
		})
	})

	Context("scale-up from zero also locks replicasPerPrimary", func() {
		It("rejects returning with a different number of replicas", func() {
			const name = "lock-replicas-reject"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			bringToAppliedReady(newStorageCluster(name, 3, 1))
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 0 })).To(Succeed())
			err := updateSpec(nn, func(c *redisv1.Redkey) {
				c.Spec.Primaries = 3
				c.Spec.ReplicasPerPrimary = 2
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("restore the same replicasPerPrimary"))
		})

		It("accepts returning with the exact previous primaries and replicas", func() {
			const name = "lock-replicas-accept"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			bringToAppliedReady(newStorageCluster(name, 3, 1))
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 0 })).To(Succeed())
			Expect(updateSpec(nn, func(c *redisv1.Redkey) {
				c.Spec.Primaries = 3
				c.Spec.ReplicasPerPrimary = 1
			})).To(Succeed())
		})
	})

	Context("clusters that are not restricted", func() {
		It("allows any scale-up from zero for a fresh cluster created at zero (storage)", func() {
			const name = "lock-fresh-zero"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			Expect(k8sClient.Create(ctx, newStorageCluster(name, 0, 0))).To(Succeed())
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })
			reconcileCluster(ctx, nn)

			// Never ran, so nothing recorded; scaling up to any topology is allowed.
			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 5 })).To(Succeed())
		})

		It("allows any scale-up from zero for ephemeral clusters", func() {
			const name = "lock-ephemeral-free"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			cluster := newTestCluster(name, namespace) // ephemeral, 3 primaries
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 0 })).To(Succeed())
			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 2 })).To(Succeed())
		})

		It("allows free scaling while primaries stays > 0 for storage clusters", func() {
			const name = "lock-free-nonzero"
			nn := types.NamespacedName{Name: name, Namespace: namespace}
			bringToAppliedReady(newStorageCluster(name, 3, 0))
			DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

			// Never went through zero: scaling to any positive topology is allowed.
			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 5 })).To(Succeed())
			Expect(updateSpec(nn, func(c *redisv1.Redkey) { c.Spec.Primaries = 2 })).To(Succeed())
		})
	})
})
