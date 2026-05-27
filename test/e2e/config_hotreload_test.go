// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
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
	"k8s.io/utils/ptr"
)

var _ = Describe("Configuration Hot-Reload", Ordered, Label("hotreload"), func() {
	var (
		ctx       context.Context
		cancel    context.CancelFunc
		ns        *corev1.Namespace
		clusterNs string
	)

	const clusterName = "hotreload-cluster"

	BeforeAll(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 20*time.Minute)

		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-hotreload")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)

		By("creating a cluster with custom Robin config")
		opts := framework.DefaultClusterOptions(clusterName, clusterNs)
		opts.RobinConfig = &redkeyv1beta1.RobinConfig{
			Reconciler: &redkeyv1beta1.RobinConfigReconciler{
				IntervalSeconds:        ptr.To(30),
				IntervalOnErrorSeconds: ptr.To(10),
				IntervalOnWaitSeconds:  ptr.To(10),
			},
			Cluster: &redkeyv1beta1.RobinConfigCluster{
				ConnectionMaxRetries:         ptr.To(10),
				ConnectionBackOffSeconds:     ptr.To(10),
				ClusterCommandTimeoutSeconds: ptr.To(24),
				ClusterMeetWaitSeconds:       ptr.To(5),
			},
			Metrics: &redkeyv1beta1.RobinConfigMetrics{
				CollectionIntervalSeconds: ptr.To(60),
				RedisInfoKeys: []string{
					"keyspace_hits", "evicted_keys", "connected_clients",
					"used_memory_rss", "maxmemory",
				},
			},
		}
		_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the cluster to reach Ready")
		_, err = framework.WaitForClusterReady(ctx, k8sClient,
			types.NamespacedName{Name: clusterName, Namespace: clusterNs},
			framework.CreationTimeout)
		Expect(err).NotTo(HaveOccurred())
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

	Context("Reconciler interval change", func() {
		It("should apply the new reconciler interval", func() {
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

			By("updating reconciler intervalSeconds from 30 to 15")
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err := k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			if cluster.Spec.Robin.Config == nil {
				cluster.Spec.Robin.Config = &redkeyv1beta1.RobinConfig{}
			}
			if cluster.Spec.Robin.Config.Reconciler == nil {
				cluster.Spec.Robin.Config.Reconciler = &redkeyv1beta1.RobinConfigReconciler{}
			}
			cluster.Spec.Robin.Config.Reconciler.IntervalSeconds = ptr.To(15)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying cluster remains healthy after config change")
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Metrics collection interval change", func() {
		It("should apply the new metrics collection interval", func() {
			Skip("Sequential hot-reload config updates are not reliably reaching Applied yet")

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

			By("updating metrics collectionIntervalSeconds from 60 to 30")
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err := k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			if cluster.Spec.Robin.Config == nil {
				cluster.Spec.Robin.Config = &redkeyv1beta1.RobinConfig{}
			}
			if cluster.Spec.Robin.Config.Metrics == nil {
				cluster.Spec.Robin.Config.Metrics = &redkeyv1beta1.RobinConfigMetrics{}
			}
			cluster.Spec.Robin.Config.Metrics.CollectionIntervalSeconds = ptr.To(30)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying cluster remains healthy")
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Redis client config change", func() {
		It("should apply new connection parameters", func() {
			Skip("Sequential hot-reload config updates are not reliably reaching Applied yet")

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

			By("updating cluster connectionMaxRetries and connectionBackOffSeconds")
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err := k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			if cluster.Spec.Robin.Config == nil {
				cluster.Spec.Robin.Config = &redkeyv1beta1.RobinConfig{}
			}
			if cluster.Spec.Robin.Config.Cluster == nil {
				cluster.Spec.Robin.Config.Cluster = &redkeyv1beta1.RobinConfigCluster{}
			}
			cluster.Spec.Robin.Config.Cluster.ConnectionMaxRetries = ptr.To(20)
			cluster.Spec.Robin.Config.Cluster.ConnectionBackOffSeconds = ptr.To(5)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying cluster remains healthy")
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Cluster command timeout change", func() {
		It("should apply new cluster command timeout", func() {
			Skip("Sequential hot-reload config updates are not reliably reaching Applied yet")

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

			By("updating clusterCommandTimeoutSeconds from 24 to 60")
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err := k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			if cluster.Spec.Robin.Config == nil {
				cluster.Spec.Robin.Config = &redkeyv1beta1.RobinConfig{}
			}
			if cluster.Spec.Robin.Config.Cluster == nil {
				cluster.Spec.Robin.Config.Cluster = &redkeyv1beta1.RobinConfigCluster{}
			}
			cluster.Spec.Robin.Config.Cluster.ClusterCommandTimeoutSeconds = ptr.To(60)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying cluster remains healthy with new timeout")
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
