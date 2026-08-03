// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	redkeyv1beta1 "github.com/inditextech/redkey-operator/api/v1beta1"
	"github.com/inditextech/redkey-operator/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Configuration Hot-Reload", Ordered, Label("hotreload"), func() {
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
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-hotreload")
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

	// verifyNoRobinRestart asserts that the Robin pod was NOT restarted after a hot-reload config change.
	verifyNoRobinRestart := func(clusterName string, originalUID string) {
		By("verifying Robin pod was NOT restarted (same UID)")
		currentUID := getRobinPodUID(ctx, clusterNs, clusterName)
		Expect(currentUID).To(Equal(originalUID),
			"Robin pod should NOT be restarted for hot-reload config changes")
	}

	// --- Reconciler Config ---

	Context("Reconciler intervalSeconds change", func() {
		const clusterName = "hotreload-reconciler-interval"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply the new reconciler interval without restarting Robin", func() {
			By("creating a cluster with explicit reconciler config")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Reconciler: &redkeyv1beta1.RobinConfigReconciler{
					IntervalSeconds: new(30),
				},
			}
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)
			Expect(originalUID).NotTo(BeEmpty())

			By("updating reconciler intervalSeconds from 30 to 15")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Reconciler.IntervalSeconds = new(15)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying cluster remains healthy")
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)
		})
	})

	Context("Reconciler intervalOnErrorSeconds change", func() { //nolint:dupl
		const clusterName = "hotreload-reconciler-error"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply the new intervalOnErrorSeconds without restarting Robin", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Reconciler: &redkeyv1beta1.RobinConfigReconciler{
					IntervalOnErrorSeconds: new(10),
				},
			}
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)

			By("updating intervalOnErrorSeconds from 10 to 5")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Reconciler.IntervalOnErrorSeconds = new(5)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)
		})
	})

	Context("Reconciler intervalOnWaitSeconds change", func() { //nolint:dupl
		const clusterName = "hotreload-reconciler-wait"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply the new intervalOnWaitSeconds without restarting Robin", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Reconciler: &redkeyv1beta1.RobinConfigReconciler{
					IntervalOnWaitSeconds: new(10),
				},
			}
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)

			By("updating intervalOnWaitSeconds from 10 to 5")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Reconciler.IntervalOnWaitSeconds = new(5)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)
		})
	})

	// --- Cluster Config ---

	Context("Cluster connectionMaxRetries change", func() { //nolint:dupl
		const clusterName = "hotreload-cluster-retries"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply new connectionMaxRetries without restarting Robin", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Cluster: &redkeyv1beta1.RobinConfigCluster{
					ConnectionMaxRetries: new(10),
				},
			}
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)

			By("updating connectionMaxRetries from 10 to 5")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Cluster.ConnectionMaxRetries = new(5)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)
		})
	})

	Context("Cluster connectionBackOffSeconds change", func() { //nolint:dupl
		const clusterName = "hotreload-cluster-backoff"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply new connectionBackOffSeconds without restarting Robin", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Cluster: &redkeyv1beta1.RobinConfigCluster{
					ConnectionBackOffSeconds: new(10),
				},
			}
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)

			By("updating connectionBackOffSeconds from 10 to 5")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Cluster.ConnectionBackOffSeconds = new(5)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)
		})
	})

	Context("Cluster clusterCommandTimeoutSeconds change", func() { //nolint:dupl
		const clusterName = "hotreload-cluster-cmd-timeout"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply new clusterCommandTimeoutSeconds without restarting Robin", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Cluster: &redkeyv1beta1.RobinConfigCluster{
					ClusterCommandTimeoutSeconds: new(24),
				},
			}
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)

			By("updating clusterCommandTimeoutSeconds from 24 to 60")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Cluster.ClusterCommandTimeoutSeconds = new(60)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)
		})
	})

	Context("Cluster clusterMeetWaitSeconds change", func() { //nolint:dupl
		const clusterName = "hotreload-cluster-meet-wait"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply new clusterMeetWaitSeconds without restarting Robin", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Cluster: &redkeyv1beta1.RobinConfigCluster{
					ClusterMeetWaitSeconds: new(5),
				},
			}
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)

			By("updating clusterMeetWaitSeconds from 5 to 10")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Cluster.ClusterMeetWaitSeconds = new(10)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)
		})
	})

	Context("Cluster rebalanceTimeoutSeconds change", func() { //nolint:dupl
		const clusterName = "hotreload-cluster-rebalance-to"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply new rebalanceTimeoutSeconds without restarting Robin", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Cluster: &redkeyv1beta1.RobinConfigCluster{
					RebalanceTimeoutSeconds: new(120),
				},
			}
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)

			By("updating rebalanceTimeoutSeconds from 120 to 60")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Cluster.RebalanceTimeoutSeconds = new(60)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)
		})
	})

	// --- Metrics Config ---

	Context("Metrics collectionIntervalSeconds change", func() { //nolint:dupl
		const clusterName = "hotreload-metrics-interval"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply new collectionIntervalSeconds without restarting Robin", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Metrics: &redkeyv1beta1.RobinConfigMetrics{
					CollectionIntervalSeconds: new(60),
				},
			}
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)

			By("updating collectionIntervalSeconds from 60 to 30")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Metrics.CollectionIntervalSeconds = new(30)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)
		})
	})

	Context("Metrics redisInfoKeys change", func() {
		const clusterName = "hotreload-metrics-keys"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply new redisInfoKeys without restarting Robin", func() {
			By("creating a cluster with specific redis info keys")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Metrics: &redkeyv1beta1.RobinConfigMetrics{
					RedisInfoKeys: []string{
						"keyspace_hits", "evicted_keys", "connected_clients",
						"used_memory_rss", "maxmemory",
					},
				},
			}
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)

			By("updating redisInfoKeys to a different subset")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Metrics.RedisInfoKeys = []string{
				"connected_clients", "used_memory_rss", "total_commands_processed",
			}
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)
		})
	})

	Context("Metrics metricsLabels change", func() {
		const clusterName = "hotreload-metrics-labels"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should apply new metricsLabels and reflect them in /metrics endpoint", func() {
			By("creating a cluster without custom metrics labels")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			originalUID := getRobinPodUID(ctx, clusterNs, clusterName)

			By("adding custom metricsLabels")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			if cluster.Spec.Robin.Config == nil {
				cluster.Spec.Robin.Config = &redkeyv1beta1.RobinConfig{}
			}
			if cluster.Spec.Robin.Config.Metrics == nil {
				cluster.Spec.Robin.Config.Metrics = &redkeyv1beta1.RobinConfigMetrics{}
			}
			cluster.Spec.Robin.Config.Metrics.MetricsLabels = map[string]string{
				"env":  "e2e-test",
				"team": "platform",
			}
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			verifyNoRobinRestart(clusterName, originalUID)

			By("verifying custom labels appear in /metrics endpoint")
			Eventually(func() bool {
				robinPods := &corev1.PodList{}
				if err := k8sClient.List(ctx, robinPods, client.InNamespace(clusterNs),
					client.MatchingLabels{
						"redkey.inditex.dev/cluster":   clusterName,
						"redkey.inditex.dev/component": "robin",
					}); err != nil {
					return false
				}
				if len(robinPods.Items) == 0 {
					return false
				}
				stdout, _, err := framework.ExecInPod(clusterNs, robinPods.Items[0].Name,
					"wget -qO- http://localhost:8080/metrics 2>/dev/null || curl -s http://localhost:8080/metrics 2>/dev/null")
				if err != nil {
					return false
				}
				return strings.Contains(stdout, `env="e2e-test"`) &&
					strings.Contains(stdout, `team="platform"`)
			}, 3*time.Minute, 10*time.Second).Should(BeTrue(),
				"Custom metricsLabels should appear in /metrics output")
		})
	})
})
