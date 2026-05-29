// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/internal/controller"
)

var _ = Describe("Configuration Creation", func() {
	const namespace = "default"

	ctx := context.Background()

	Context("When reconciling a new RedkeyCluster", Ordered, func() {
		const clusterName = "config-create-basic"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should create a config on first reconcile", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Spec.Sequence).To(Equal(1))
			Expect(configs[0].Labels[controller.ClusterLabel]).To(Equal(clusterName))
			Expect(configs[0].Annotations).To(HaveKey("redkey.inditex.dev/cluster-generation"))
		})

		It("should not create a duplicate config on re-reconcile", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
		})

		It("should create a new config when cluster spec changes", func() {
			By("updating the cluster spec")
			var cluster redisv1.RedkeyCluster
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			cluster.Spec.Primaries = 5
			Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())

			By("reconciling")
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(2))
			Expect(configs[0].Spec.Sequence).To(Equal(1))
			Expect(configs[1].Spec.Sequence).To(Equal(2))
			Expect(configs[1].Spec.Primaries).To(Equal(int32(5)))
		})
	})

	Context("When verifying spec snapshot completeness", Ordered, func() {
		const clusterName = "config-create-snapshot"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			deletePVC := true
			purgeKeys := true
			labels := map[string]string{"team": "platform"}
			annotations := map[string]string{"prometheus.io/scrape": "true"}
			cluster := &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeyClusterSpec{
					Primaries:            3,
					ReplicasPerPrimary:   1,
					Ephemeral:            true,
					Image:                "redis:7.2",
					Robin:                redisv1.RobinSpec{Image: "redkey-robin:latest"},
					Version:              "7.2.0",
					Config:               "maxmemory-policy allkeys-lru",
					DeletePVC:            &deletePVC,
					PurgeKeysOnRebalance: &purgeKeys,
					Labels:               &labels,
					Annotations:          &annotations,
					Pdb: redisv1.Pdb{
						Enabled: true,
					},
					Resources: &corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should snapshot the full spec into the config", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))

			spec := configs[0].Spec
			Expect(spec.Primaries).To(Equal(int32(3)))
			Expect(spec.ReplicasPerPrimary).To(Equal(int32(1)))
			Expect(spec.Ephemeral).To(BeTrue())
			Expect(spec.Image).To(Equal("redis:7.2"))
			Expect(spec.Version).To(Equal("7.2.0"))
			Expect(spec.RedisConfig).To(Equal("maxmemory-policy allkeys-lru"))
			Expect(spec.DeletePVC).NotTo(BeNil())
			Expect(*spec.DeletePVC).To(BeTrue())
			Expect(spec.PurgeKeysOnRebalance).NotTo(BeNil())
			Expect(*spec.PurgeKeysOnRebalance).To(BeTrue())
			Expect(spec.Labels).NotTo(BeNil())
			Expect(*spec.Labels).To(HaveKeyWithValue("team", "platform"))
			Expect(spec.Annotations).NotTo(BeNil())
			Expect(*spec.Annotations).To(HaveKeyWithValue("prometheus.io/scrape", "true"))
			Expect(spec.Pdb.Enabled).To(BeTrue())
			Expect(spec.Resources).NotTo(BeNil())
			Expect(spec.Resources.Requests[corev1.ResourceCPU]).To(Equal(resource.MustParse("100m")))
		})
	})

	Context("When the cluster has Robin config", Ordered, func() {
		const clusterName = "config-create-robin"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			intervalSec := 30
			intervalOnErrorSec := 12
			intervalOnWaitSec := 10
			connectionMaxRetries := 10
			connectionBackOffSeconds := 5
			collectionIntervalSeconds := 60
			redisInfoKeys := []string{"connected_clients", "total_commands_processed"}
			metricsLabels := map[string]string{"env": "prod", "team": "platform"}
			cluster := &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeyClusterSpec{
					Ephemeral: true,
					Primaries: 3,
					Robin: redisv1.RobinSpec{
						Image: "redkey-robin:latest",
						Config: &redisv1.RobinConfig{
							Reconciler: &redisv1.RobinConfigReconciler{
								IntervalSeconds:        &intervalSec,
								IntervalOnErrorSeconds: &intervalOnErrorSec,
								IntervalOnWaitSeconds:  &intervalOnWaitSec,
							},
							Cluster: &redisv1.RobinConfigCluster{
								ConnectionMaxRetries:     &connectionMaxRetries,
								ConnectionBackOffSeconds: &connectionBackOffSeconds,
							},
							Metrics: &redisv1.RobinConfigMetrics{
								CollectionIntervalSeconds: &collectionIntervalSeconds,
								RedisInfoKeys:             redisInfoKeys,
								MetricsLabels:             metricsLabels,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should pass Robin config into the new config", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Spec.RobinConfig).NotTo(BeNil())
			Expect(configs[0].Spec.RobinConfig.Reconciler).NotTo(BeNil())
			Expect(configs[0].Spec.RobinConfig.Cluster).NotTo(BeNil())
			Expect(configs[0].Spec.RobinConfig.Metrics).NotTo(BeNil())
			Expect(*configs[0].Spec.RobinConfig.Reconciler.IntervalSeconds).To(Equal(30))
			Expect(*configs[0].Spec.RobinConfig.Reconciler.IntervalOnErrorSeconds).To(Equal(12))
			Expect(*configs[0].Spec.RobinConfig.Reconciler.IntervalOnWaitSeconds).To(Equal(10))
			Expect(*configs[0].Spec.RobinConfig.Cluster.ConnectionMaxRetries).To(Equal(10))
			Expect(*configs[0].Spec.RobinConfig.Cluster.ConnectionBackOffSeconds).To(Equal(5))
			Expect(*configs[0].Spec.RobinConfig.Metrics.CollectionIntervalSeconds).To(Equal(60))
			Expect(configs[0].Spec.RobinConfig.Metrics.RedisInfoKeys).To(
				Equal([]string{"connected_clients", "total_commands_processed"}),
			)
			Expect(configs[0].Spec.RobinConfig.Metrics.MetricsLabels).To(
				Equal(map[string]string{"env": "prod", "team": "platform"}),
			)
		})
	})

	Context("When the cluster has no Robin config", Ordered, func() {
		const clusterName = "config-create-no-robin"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should not set RobinConfig when Robin.Config is nil", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Spec.RobinConfig).To(BeNil())
		})
	})

	Context("When verifying ownerReference", Ordered, func() {
		const clusterName = "config-create-owner"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should set ownerReference on the config", func() {
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].OwnerReferences).NotTo(BeEmpty())
			Expect(configs[0].OwnerReferences[0].Kind).To(Equal("RedkeyCluster"))
			Expect(configs[0].OwnerReferences[0].Name).To(Equal(clusterName))

			// Verify it's a controller reference
			Expect(configs[0].OwnerReferences[0].Controller).NotTo(BeNil())
			Expect(*configs[0].OwnerReferences[0].Controller).To(BeTrue())
		})
	})

	Context("When multiple spec changes occur", Ordered, func() {
		const clusterName = "config-create-multi"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		BeforeAll(func() {
			cluster := newTestCluster(clusterName, namespace)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should sequence correctly after multiple spec changes", func() {
			By("first reconcile - creates config seq=1")
			reconcileCluster(ctx, namespacedName)

			By("second spec change - creates config seq=2")
			var cluster redisv1.RedkeyCluster
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			cluster.Spec.Primaries = 5
			Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())
			reconcileCluster(ctx, namespacedName)

			By("third spec change - creates config seq=3")
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			cluster.Spec.Primaries = 7
			Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())
			reconcileCluster(ctx, namespacedName)

			configs := listConfigs(ctx, clusterName, namespace)
			Expect(configs).To(HaveLen(3))
			Expect(configs[0].Spec.Sequence).To(Equal(1))
			Expect(configs[1].Spec.Sequence).To(Equal(2))
			Expect(configs[2].Spec.Sequence).To(Equal(3))
			Expect(configs[0].Spec.Primaries).To(Equal(int32(3)))
			Expect(configs[1].Spec.Primaries).To(Equal(int32(5)))
			Expect(configs[2].Spec.Primaries).To(Equal(int32(7)))
		})
	})

	Context("When reconciling a deleted RedkeyCluster", func() {
		const clusterName = "config-create-notfound"
		namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

		It("should not error when cluster is not found", func() {
			r := newReconciler()
			result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			var configList redisv1.RedkeyClusterConfigList
			Expect(k8sClient.List(ctx, &configList,
				client.InNamespace(namespace),
				client.MatchingLabels{controller.ClusterLabel: clusterName},
			)).To(Succeed())
			Expect(configList.Items).To(BeEmpty())
		})
	})
})
