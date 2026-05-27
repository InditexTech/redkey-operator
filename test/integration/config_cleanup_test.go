// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

var _ = Describe("Cleanup of Superseded Configs", func() {
	const namespace = "default"

	ctx := context.Background()

	defineKeepLastAppliedContext := func(description, clusterName, firstPhase string) {
		Context(description, Ordered, func() {
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			BeforeAll(func() {
				configs := setupThreeConfigs(ctx, clusterName, namespace)

				configs[0].Status.ConfigPhase = firstPhase
				updateConfigStatus(ctx, &configs[0])
				configs[1].Status.ConfigPhase = redisv1.ConfigPhaseApplied
				updateConfigStatus(ctx, &configs[1])
			})

			AfterAll(func() {
				deleteCluster(ctx, clusterName, namespace)
			})

			It("should keep the last Applied config and newer ones", func() {
				reconcileCluster(ctx, namespacedName)

				configs := listConfigs(ctx, clusterName, namespace)
				Expect(configs).To(HaveLen(2))
				Expect(configs[0].Spec.Sequence).To(Equal(2))
				Expect(configs[1].Spec.Sequence).To(Equal(3))
			})
		})
	}

	Context("When only one config exists", Ordered, func() {
		const clusterName = "cleanup-single"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			// Create initial config via reconcile
			reconcileCluster(ctx, namespacedName)
			// Set the config as Applied (simulating Robin)
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			updateConfigStatus(ctx, &configs[0])
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should not delete when only one config exists", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Spec.Sequence).To(Equal(1))
		})
	})

	defineKeepLastAppliedContext("When leading configs are Applied", "cleanup-applied", redisv1.ConfigPhaseApplied)

	Context("When no Applied config exists", Ordered, func() {
		const clusterName = "cleanup-superseded"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Create 3 configs
			reconcileCluster(ctx, namespacedName)
			var cl redisv1.RedkeyCluster
			Expect(k8sClient.Get(ctx, namespacedName, &cl)).To(Succeed())
			cl.Spec.Primaries = 5
			Expect(k8sClient.Update(ctx, &cl)).To(Succeed())
			reconcileCluster(ctx, namespacedName)
			Expect(k8sClient.Get(ctx, namespacedName, &cl)).To(Succeed())
			cl.Spec.Primaries = 7
			Expect(k8sClient.Update(ctx, &cl)).To(Succeed())
			reconcileCluster(ctx, namespacedName)

			// Set phases: [Superseded, Superseded, InProgress]
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(3))
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseSuperseded
			updateConfigStatus(ctx, &configs[0])
			configs[1].Status.ConfigPhase = redisv1.ConfigPhaseSuperseded
			updateConfigStatus(ctx, &configs[1])
			configs[2].Status.ConfigPhase = redisv1.ConfigPhaseInProgress
			updateConfigStatus(ctx, &configs[2])
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should not delete any config", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(3))
			Expect(configs[0].Spec.Sequence).To(Equal(1))
			Expect(configs[0].Status.ConfigPhase).To(Equal(redisv1.ConfigPhaseSuperseded))
			Expect(configs[1].Spec.Sequence).To(Equal(2))
			Expect(configs[1].Status.ConfigPhase).To(Equal(redisv1.ConfigPhaseSuperseded))
			Expect(configs[2].Spec.Sequence).To(Equal(3))
			Expect(configs[2].Status.ConfigPhase).To(Equal(redisv1.ConfigPhaseInProgress))
		})
	})

	Context("When a later Applied exists after a non-terminal config", Ordered, func() {
		const clusterName = "cleanup-stops"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Create 3 configs
			reconcileCluster(ctx, namespacedName)
			var cl redisv1.RedkeyCluster
			Expect(k8sClient.Get(ctx, namespacedName, &cl)).To(Succeed())
			cl.Spec.Primaries = 5
			Expect(k8sClient.Update(ctx, &cl)).To(Succeed())
			reconcileCluster(ctx, namespacedName)
			Expect(k8sClient.Get(ctx, namespacedName, &cl)).To(Succeed())
			cl.Spec.Primaries = 7
			Expect(k8sClient.Update(ctx, &cl)).To(Succeed())
			reconcileCluster(ctx, namespacedName)

			// Set phases: [Applied, Pending, Applied]
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(3))
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			updateConfigStatus(ctx, &configs[0])
			// configs[1] stays Pending
			configs[2].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			updateConfigStatus(ctx, &configs[2])
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should delete everything older than the last Applied config", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Spec.Sequence).To(Equal(3))
		})
	})

	Context("When all configs are terminal", Ordered, func() {
		const clusterName = "cleanup-preserve-last"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Create 2 configs
			reconcileCluster(ctx, namespacedName)
			var cl redisv1.RedkeyCluster
			Expect(k8sClient.Get(ctx, namespacedName, &cl)).To(Succeed())
			cl.Spec.Primaries = 5
			Expect(k8sClient.Update(ctx, &cl)).To(Succeed())
			reconcileCluster(ctx, namespacedName)

			// Set phases: [Applied, Applied]
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(2))
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			updateConfigStatus(ctx, &configs[0])
			configs[1].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			updateConfigStatus(ctx, &configs[1])
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should never delete the last config even if terminal", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Spec.Sequence).To(Equal(2))
		})
	})

	defineKeepLastAppliedContext(
		"When leading configs are a mix of Applied and Superseded",
		"cleanup-mix",
		redisv1.ConfigPhaseSuperseded,
	)

	Context("When first config is Pending", Ordered, func() {
		const clusterName = "cleanup-noop-pending"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Create 2 configs
			reconcileCluster(ctx, namespacedName)
			var cl redisv1.RedkeyCluster
			Expect(k8sClient.Get(ctx, namespacedName, &cl)).To(Succeed())
			cl.Spec.Primaries = 5
			Expect(k8sClient.Update(ctx, &cl)).To(Succeed())
			reconcileCluster(ctx, namespacedName)

			// Set phases: [Pending, Applied]
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(2))
			// configs[0] stays Pending
			configs[1].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			updateConfigStatus(ctx, &configs[1])
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should delete the Pending config because it is older than the last Applied", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Spec.Sequence).To(Equal(2))
		})
	})

	Context("When first config is InProgress", Ordered, func() {
		const clusterName = "cleanup-noop-inprogress"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeyClusterSpec{
					Ephemeral: true,
					Primaries: 3,
					Robin:     redisv1.RobinSpec{Image: "redkey-robin:latest"},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Create 2 configs
			reconcileCluster(ctx, namespacedName)
			var cl redisv1.RedkeyCluster
			Expect(k8sClient.Get(ctx, namespacedName, &cl)).To(Succeed())
			cl.Spec.Primaries = 5
			Expect(k8sClient.Update(ctx, &cl)).To(Succeed())
			reconcileCluster(ctx, namespacedName)

			// Set phases: [InProgress, Applied]
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(2))
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseInProgress
			updateConfigStatus(ctx, &configs[0])
			configs[1].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			updateConfigStatus(ctx, &configs[1])
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should delete the InProgress config because it is older than the last Applied", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Spec.Sequence).To(Equal(2))
		})
	})
})
