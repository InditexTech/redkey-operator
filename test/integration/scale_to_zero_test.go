// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

var _ = Describe("Scale to Zero Reconciliation", func() {
	const namespace = "default"
	ctx := context.Background()

	Context("Creating a Redkey with 0 primaries", Ordered, func() {
		const clusterName = "zero-create"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := &redisv1.Redkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeySpec{
					Ephemeral: true,
					Primaries: 0,
					Robin:     redisv1.RobinSpec{Image: "robin:latest"},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should not create any RedkeyConfig", func() {
			reconcileCluster(ctx, namespacedName)
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(BeEmpty())
		})

		It("should not create Robin deployment", func() {
			var deploy appsv1.Deployment
			err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}, &deploy)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should not create RBAC resources", func() {
			var sa corev1.ServiceAccount
			err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}, &sa)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			var role rbacv1.Role
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}, &role)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should set status to Ready with 0 replicas", func() {
			var cluster redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			Expect(cluster.Status.Phase).To(Equal(redisv1.PhaseReady))
			Expect(cluster.Status.Replicas).To(Equal(int32(0)))
			Expect(cluster.Status.Nodes).To(BeEmpty())
		})

		It("should be idempotent on subsequent reconciles", func() {
			reconcileCluster(ctx, namespacedName)
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(BeEmpty())

			var cluster redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			Expect(cluster.Status.Phase).To(Equal(redisv1.PhaseReady))
		})
	})

	Context("Scaling from >0 to 0 primaries", Ordered, func() {
		const clusterName = "scale-to-zero"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			// Create a cluster with 3 primaries and make it "ready"
			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			reconcileCluster(ctx, namespacedName)

			// Simulate Robin marking the config as Applied
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			configs[0].Status.ConfigPhase = redisv1.ConfigPhaseApplied
			configs[0].Status.Status = redisv1.ClusterStatusReady
			updateConfigStatus(ctx, &configs[0])
			reconcileCluster(ctx, namespacedName)
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should create a config with primaries=0 when scaled to zero", func() {
			// Scale to 0
			var cluster redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			cluster.Spec.Primaries = 0
			Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())

			reconcileCluster(ctx, namespacedName)

			// Should have created a new config with primaries=0
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(len(configs)).To(BeNumerically(">=", 2))
			lastConfig := configs[len(configs)-1]
			Expect(lastConfig.Spec.Primaries).To(Equal(int32(0)))
		})

		It("should keep Robin alive while config is not Applied", func() {
			var deploy appsv1.Deployment
			err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}, &deploy)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should clean up operator objects once config is Applied", func() {
			// Simulate Robin marking the scale-to-zero config as Applied
			configs := listConfigs(ctx, clusterName, namespace)
			lastConfig := configs[len(configs)-1]
			lastConfig.Status.ConfigPhase = redisv1.ConfigPhaseApplied
			lastConfig.Status.Status = redisv1.ClusterStatusReady
			lastConfig.Status.Nodes = map[string]*redisv1.RedisNode{}
			Expect(k8sClient.Status().Update(ctx, &lastConfig)).To(Succeed())

			reconcileCluster(ctx, namespacedName)

			// All configs should be deleted
			configs = listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(BeEmpty())

			// Robin deployment should be deleted
			var deploy appsv1.Deployment
			err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}, &deploy)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			// RBAC should be deleted
			var sa corev1.ServiceAccount
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}, &sa)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("should set status to Ready with 0 replicas after cleanup", func() {
			var cluster redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			Expect(cluster.Status.Phase).To(Equal(redisv1.PhaseReady))
			Expect(cluster.Status.Replicas).To(Equal(int32(0)))
			Expect(cluster.Status.Nodes).To(BeEmpty())
		})
	})

	Context("Scaling from 0 to >0 primaries", Ordered, func() {
		const clusterName = "scale-from-zero"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			// Create a cluster with 0 primaries
			cluster := &redisv1.Redkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeySpec{
					Ephemeral: true,
					Primaries: 0,
					Robin:     redisv1.RobinSpec{Image: "robin:latest"},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			reconcileCluster(ctx, namespacedName)
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should create configs and Robin when scaled up from zero", func() {
			// Scale to 3
			var cluster redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			cluster.Spec.Primaries = 3
			Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())

			reconcileCluster(ctx, namespacedName)

			// Should now have a config
			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Spec.Primaries).To(Equal(int32(3)))
		})

		It("should create Robin deployment when scaling from zero", func() {
			var deploy appsv1.Deployment
			err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}, &deploy)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should create RBAC when scaling from zero", func() {
			var sa corev1.ServiceAccount
			err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}, &sa)
			Expect(err).NotTo(HaveOccurred())

			var role rbacv1.Role
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}, &role)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Status conditions during scale to zero", Ordered, func() {
		const clusterName = "zero-conditions"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := &redisv1.Redkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeySpec{
					Ephemeral: true,
					Primaries: 0,
					Robin:     redisv1.RobinSpec{Image: "robin:latest"},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			reconcileCluster(ctx, namespacedName)
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should have Ready=True condition with ScaledToZero reason", func() {
			var cluster redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())

			var readyCond *metav1.Condition
			for i := range cluster.Status.Conditions {
				if cluster.Status.Conditions[i].Type == "Ready" {
					readyCond = &cluster.Status.Conditions[i]
					break
				}
			}
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal("ScaledToZero"))
		})

		It("should have ConfigPending=False and Error=False", func() {
			var cluster redisv1.Redkey
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())

			for _, cond := range cluster.Status.Conditions {
				switch cond.Type {
				case "ConfigPending":
					Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				case "Error":
					Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				}
			}
		})
	})
})

// deleteClusterIgnoreRBAC is a helper that suppresses RBAC-not-found errors during cleanup
func deleteClusterObjects(ctx context.Context, clusterName, namespace string) { //nolint:unused
	objects := []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: clusterName + "-robin", Namespace: namespace}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: clusterName + "-robin", Namespace: namespace}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: clusterName + "-robin", Namespace: namespace}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterName + "-robin", Namespace: namespace}},
	}
	for _, obj := range objects {
		_ = k8sClient.Delete(ctx, obj)
	}
}
