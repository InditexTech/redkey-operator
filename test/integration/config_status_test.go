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

var _ = Describe("Aggregate Status", func() {
	const namespace = "default"

	ctx := context.Background()

	Context("Phase field based on ConfigPhase", func() {
		It("should set Phase=Configuring when ConfigPhase is Pending", func() {
			const clusterName = "status-pending"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			// First reconcile creates config (ConfigPhase is empty/Pending by default)
			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(redisv1.PhaseConfiguring))
		})

		It("should set Phase=Configuring when ConfigPhase is InProgress", func() {
			const clusterName = "status-configuring"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			reconcileCluster(ctx, namespacedName)

			// Set config to InProgress
			configs := listConfigs(ctx, clusterName, namespace)
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseInProgress
			updateConfigStatus(ctx, &configs[0])

			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(redisv1.PhaseConfiguring))
		})

		It("should set Phase=Ready when ConfigPhase is Applied", func() {
			const clusterName = "status-applied"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			configs[0].Status.Status = redisv1.ClusterStatusReady
			updateConfigStatus(ctx, &configs[0])

			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(redisv1.PhaseReady))
		})

		It("should set Phase=Ready when ConfigPhase is Superseded", func() {
			const clusterName = "status-superseded"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseSuperseded
			updateConfigStatus(ctx, &configs[0])

			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(redisv1.PhaseReady))
		})
	})

	Context("Status and Substatus mirroring", func() {
		It("should mirror Status and Substatus from config", func() {
			const clusterName = "status-mirror"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseInProgress
			configs[0].Status.Status = redisv1.ClusterStatusScalingUp
			configs[0].Status.Substatus = redisv1.RedkeySubstatus{
				Status:             "WaitingForPods",
				UpgradingPartition: 2,
			}
			updateConfigStatus(ctx, &configs[0])

			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
			Expect(updated.Status.Status).To(Equal(redisv1.ClusterStatusScalingUp))
			Expect(updated.Status.Substatus.Status).To(Equal("WaitingForPods"))
			Expect(updated.Status.Substatus.UpgradingPartition).To(Equal(2))
		})
	})

	Context("Nodes mirroring", func() {
		It("should mirror Nodes from config to cluster", func() {
			const clusterName = "status-nodes"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			configs[0].Status.Status = redisv1.ClusterStatusReady
			configs[0].Status.Nodes = map[string]*redisv1.RedisNode{
				"node-0": {Role: "primary", IP: "10.0.0.1"},
				"node-1": {Role: "replica", IP: "10.0.0.2", ReplicationStatus: "ok"},
			}
			updateConfigStatus(ctx, &configs[0])

			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
			Expect(updated.Status.Nodes).To(HaveLen(2))
			Expect(updated.Status.Nodes["node-0"].Role).To(Equal("primary"))
			Expect(updated.Status.Nodes["node-0"].IP).To(Equal("10.0.0.1"))
			Expect(updated.Status.Nodes["node-1"].Role).To(Equal("replica"))
			Expect(updated.Status.Nodes["node-1"].ReplicationStatus).To(Equal("ok"))
		})

		It("should return empty Nodes map when config has nil nodes", func() {
			const clusterName = "status-nodes-nil"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			reconcileCluster(ctx, namespacedName)

			// Config has nil Nodes by default
			configs := listConfigs(ctx, clusterName, namespace)
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			configs[0].Status.Status = redisv1.ClusterStatusReady
			updateConfigStatus(ctx, &configs[0])

			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
			Expect(updated.Status.Nodes).NotTo(BeNil())
			Expect(updated.Status.Nodes).To(BeEmpty())
		})
	})

	Context("Conditions", func() {
		It("should set Ready=True when terminal and no error", func() {
			const clusterName = "status-ready-true"
			defer deleteCluster(ctx, clusterName, namespace)

			updated := setupAppliedReadyCluster(ctx, clusterName, namespace)
			readyCond := findCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should set Ready=False and Error=True when terminal with error", func() {
			const clusterName = "status-error"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			configs[0].Status.Status = redisv1.ClusterPhaseError
			updateConfigStatus(ctx, &configs[0])

			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())

			readyCond := findCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))

			errorCond := findCondition(updated.Status.Conditions, "Error")
			Expect(errorCond).NotTo(BeNil())
			Expect(errorCond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should set ConfigPending=True when config not in terminal phase", func() {
			const clusterName = "status-config-pending"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			reconcileCluster(ctx, namespacedName)

			// Config stays Pending (default)
			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())

			pendingCond := findCondition(updated.Status.Conditions, "ConfigPending")
			Expect(pendingCond).NotTo(BeNil())
			Expect(pendingCond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should set ConfigPending=False when config in terminal phase", func() {
			const clusterName = "status-config-not-pending"
			defer deleteCluster(ctx, clusterName, namespace)

			updated := setupAppliedReadyCluster(ctx, clusterName, namespace)
			pendingCond := findCondition(updated.Status.Conditions, "ConfigPending")
			Expect(pendingCond).NotTo(BeNil())
			Expect(pendingCond.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should set Error=False when terminal without error", func() {
			const clusterName = "status-no-error"
			defer deleteCluster(ctx, clusterName, namespace)

			updated := setupAppliedReadyCluster(ctx, clusterName, namespace)
			errorCond := findCondition(updated.Status.Conditions, "Error")
			Expect(errorCond).NotTo(BeNil())
			Expect(errorCond.Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Context("ObservedGeneration and LastUpdatedAt", func() {
		It("should update ObservedGeneration", func() {
			const clusterName = "status-observed-gen"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
			Expect(updated.Status.ObservedGeneration).To(Equal(updated.Generation))
		})

		It("should update LastUpdatedAt", func() {
			const clusterName = "status-last-updated"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			reconcileCluster(ctx, namespacedName)

			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
			Expect(updated.Status.LastUpdatedAt).NotTo(BeNil())
		})
	})

	Context("Aggregation source", func() {
		It("should use the last Applied config for aggregation after cleanup", func() {
			const clusterName = "status-first-config"
			namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			defer deleteCluster(ctx, clusterName, namespace)

			// Create 2 configs by changing spec
			reconcileCluster(ctx, namespacedName)
			var cl redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &cl)).To(Succeed())
			cl.Spec.Primaries = 5
			Expect(k8sClient.Update(ctx, &cl)).To(Succeed())
			reconcileCluster(ctx, namespacedName)

			// Set different statuses on each config
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(2))

			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseInProgress
			configs[0].Status.Status = redisv1.ClusterStatusScalingUp
			updateConfigStatus(ctx, &configs[0])

			configs[1].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			configs[1].Status.Status = redisv1.ClusterStatusReady
			updateConfigStatus(ctx, &configs[1])

			reconcileCluster(ctx, namespacedName)

			// Cluster status should reflect the remaining Applied config (seq 2)
			var updated redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
			Expect(updated.Status.Status).To(Equal(redisv1.ClusterStatusReady))
			Expect(updated.Status.Phase).To(Equal(redisv1.PhaseReady))
		})
	})
})

// findCondition returns the condition with the given type, or nil if not found.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
