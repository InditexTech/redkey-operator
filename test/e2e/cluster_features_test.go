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

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Cluster Features", Ordered, Label("features"), func() {
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
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-features")
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

	Context("PodDisruptionBudget", func() {
		const clusterName = "features-pdb"

		It("should create a PDB when configured in the cluster spec", func() {
			By("creating a cluster with PDB enabled")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Pdb = redkeyv1beta1.Pdb{
				Enabled:            true,
				PdbSizeUnavailable: intstr.FromInt32(1),
			}
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying a PodDisruptionBudget was created")
			Eventually(func() bool {
				pdbList := &policyv1.PodDisruptionBudgetList{}
				if err := k8sClient.List(ctx, pdbList, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return false
				}
				return len(pdbList.Items) > 0
			}, 2*time.Minute, 5*time.Second).Should(BeTrue(), "PDB should be created")

			By("verifying PDB has correct settings")
			pdbList := &policyv1.PodDisruptionBudgetList{}
			err = k8sClient.List(ctx, pdbList, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
			Expect(err).NotTo(HaveOccurred())
			Expect(pdbList.Items).To(HaveLen(1))

			pdb := pdbList.Items[0]
			Expect(pdb.Spec.MaxUnavailable).NotTo(BeNil())
			Expect(pdb.Spec.MaxUnavailable.IntValue()).To(Equal(1))

			By("verifying PDB has owner reference")
			ownerFound := false
			for _, ref := range pdb.OwnerReferences {
				if ref.Kind == "RedkeyCluster" && ref.Name == clusterName {
					ownerFound = true
					break
				}
			}
			Expect(ownerFound).To(BeTrue(), "PDB should have OwnerReference to RedkeyCluster")

			By("verifying the cluster remains healthy")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Custom Redis configuration", func() {
		const clusterName = "features-redis-config"

		It("should apply custom redis.conf parameters to all nodes", func() {
			By("creating a cluster with custom Redis config")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Config = `maxmemory 50mb
maxmemory-policy allkeys-lru
protected-mode no
appendonly no
save ""
tcp-keepalive 120
hz 20`
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())

			By("verifying custom config params are applied via CONFIG GET")
			for _, pod := range podNames {
				// Check maxmemory
				stdout, _, err := framework.ExecInPod(clusterNs, pod, "redis-cli config get maxmemory")
				Expect(err).NotTo(HaveOccurred())
				Expect(stdout).To(ContainSubstring("52428800"), // 50MB in bytes
					"Pod %s should have maxmemory set to 50mb", pod)

				// Check maxmemory-policy
				stdout, _, err = framework.ExecInPod(clusterNs, pod, "redis-cli config get maxmemory-policy")
				Expect(err).NotTo(HaveOccurred())
				Expect(stdout).To(ContainSubstring("allkeys-lru"),
					"Pod %s should have maxmemory-policy set to allkeys-lru", pod)

				// Check tcp-keepalive
				stdout, _, err = framework.ExecInPod(clusterNs, pod, "redis-cli config get tcp-keepalive")
				Expect(err).NotTo(HaveOccurred())
				Expect(stdout).To(ContainSubstring("120"),
					"Pod %s should have tcp-keepalive set to 120", pod)

				// Check hz
				stdout, _, err = framework.ExecInPod(clusterNs, pod, "redis-cli config get hz")
				Expect(err).NotTo(HaveOccurred())
				Expect(stdout).To(ContainSubstring("20"),
					"Pod %s should have hz set to 20", pod)
			}

			By("verifying the cluster is healthy with custom config")
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Data integrity during health remediation", func() {
		const clusterName = "features-data-integrity"

		It("should preserve data during meet/forget and slot fix remediation", func() {
			By("creating an ephemeral cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data across the cluster")
			err = framework.InsertKeys(clusterNs, podNames[0], 50)
			Expect(err).NotTo(HaveOccurred())

			initialSize, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(initialSize).To(Equal(50))

			By("simulating cluster damage phase 1: forget a node")
			targetPod := podNames[2]
			targetNodeID, err := framework.GetNodeID(clusterNs, targetPod)
			Expect(err).NotTo(HaveOccurred())

			for _, pod := range podNames {
				if pod == targetPod {
					continue
				}
				_ = framework.ForgetNode(clusterNs, pod, targetNodeID)
			}
			_, _ = fmt.Fprintf(GinkgoWriter, "Phase 1: forgot node %s\n", targetNodeID[:8])

			By("waiting for Robin to re-meet the forgotten node")
			Eventually(func() error {
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed(),
				"Robin should re-meet the forgotten node")

			By("simulating cluster damage phase 2: delete some slots")
			slotsToRemove := []int{0, 1, 2, 3, 4}
			_ = framework.DelSlots(clusterNs, podNames[0], slotsToRemove)
			_, _ = fmt.Fprintf(GinkgoWriter, "Phase 2: deleted slots 0-4 from %s\n", podNames[0])

			By("waiting for Robin to repair missing slots")
			Eventually(func() error {
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed(),
				"Robin should repair the missing slots")

			By("verifying data integrity after remediation")
			Eventually(func() int {
				size, err := framework.GetDBSize(clusterNs, podNames)
				if err != nil {
					return -1
				}
				return size
			}, 2*time.Minute, 5*time.Second).Should(Equal(50),
				"All 50 keys should be preserved after remediation")

			By("verifying the cluster is fully operational")
			info, err := framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.State).To(Equal("ok"))
			Expect(info.SlotsAssigned).To(Equal(16384))
			Expect(info.KnownNodes).To(Equal(3))
		})
	})

	Context("Robin Prometheus metrics", func() {
		const clusterName = "features-robin-metrics"

		It("should expose Redis cluster metrics via Robin's /metrics endpoint", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("finding the Robin pod")
			robinPods := &corev1.PodList{}
			err = k8sClient.List(ctx, robinPods, client.InNamespace(clusterNs),
				client.MatchingLabels{
					"redkey.inditex.dev/cluster":   clusterName,
					"redkey.inditex.dev/component": "robin",
				})
			Expect(err).NotTo(HaveOccurred())
			Expect(robinPods.Items).NotTo(BeEmpty(), "Robin pod should exist")

			robinPodName := robinPods.Items[0].Name
			_, _ = fmt.Fprintf(GinkgoWriter, "Robin pod: %s\n", robinPodName)

			By("waiting for Robin metrics to be available")
			Eventually(func() bool {
				stdout, _, err := framework.ExecInPod(clusterNs, robinPodName,
					"wget -qO- http://localhost:8080/metrics 2>/dev/null || curl -s http://localhost:8080/metrics 2>/dev/null")
				if err != nil {
					return false
				}
				return len(stdout) > 100
			}, 3*time.Minute, 10*time.Second).Should(BeTrue(), "Robin /metrics endpoint should be serving")

			By("verifying metrics contain Redis cluster information")
			stdout, _, err := framework.ExecInPod(clusterNs, robinPodName,
				"wget -qO- http://localhost:8080/metrics 2>/dev/null || curl -s http://localhost:8080/metrics 2>/dev/null")
			Expect(err).NotTo(HaveOccurred())

			_, _ = fmt.Fprintf(GinkgoWriter, "Metrics output (first 500 chars): %s\n", truncate(stdout, 500))

			// Verify standard Go/process metrics are present
			Expect(stdout).To(ContainSubstring("go_goroutines"),
				"Should contain Go runtime metrics")

			// Verify controller-runtime or custom Redis metrics are present
			Expect(stdout).To(Or(
				ContainSubstring("redis_"),
				ContainSubstring("redkey_"),
				ContainSubstring("process_"),
			), "Should contain Redis or process metrics")
		})
	})

	Context("Profiling toggle", func() {
		const clusterName = "features-profiling"

		It("should enable and disable pprof endpoints at runtime via RobinConfig", func() {
			By("creating a cluster with profiling disabled")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Profiling: &redkeyv1beta1.RobinConfigProfiling{
					Enabled: ptr.To(false),
				},
			}
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("finding the Robin pod")
			robinPods := &corev1.PodList{}
			err = k8sClient.List(ctx, robinPods, client.InNamespace(clusterNs),
				client.MatchingLabels{
					"redkey.inditex.dev/cluster":   clusterName,
					"redkey.inditex.dev/component": "robin",
				})
			Expect(err).NotTo(HaveOccurred())
			Expect(robinPods.Items).NotTo(BeEmpty())
			robinPodName := robinPods.Items[0].Name

			By("verifying pprof is NOT available when disabled")
			Eventually(func() bool {
				cmd := "wget -qO- --spider http://localhost:8080/debug/pprof/ 2>&1" +
					" || curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/debug/pprof/ 2>/dev/null"
				stdout, _, err := framework.ExecInPod(clusterNs, robinPodName, cmd)
				if err != nil {
					// Connection refused or 404 means pprof is disabled — expected
					return true
				}
				// If we get a response, check it's not 200
				return stdout != "200" && !containsString(stdout, "Types of profiles available")
			}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "pprof should NOT be available when disabled")

			By("enabling profiling via spec update")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			if cluster.Spec.Robin.Config == nil {
				cluster.Spec.Robin.Config = &redkeyv1beta1.RobinConfig{}
			}
			if cluster.Spec.Robin.Config.Profiling == nil {
				cluster.Spec.Robin.Config.Profiling = &redkeyv1beta1.RobinConfigProfiling{}
			}
			cluster.Spec.Robin.Config.Profiling.Enabled = ptr.To(true)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying pprof IS available after enabling")
			Eventually(func() bool {
				cmd := "wget -qO- http://localhost:8080/debug/pprof/ 2>/dev/null" +
					" || curl -s http://localhost:8080/debug/pprof/ 2>/dev/null"
				stdout, _, err := framework.ExecInPod(clusterNs, robinPodName, cmd)
				if err != nil {
					return false
				}
				return containsString(stdout, "Types of profiles available") || containsString(stdout, "pprof")
			}, 3*time.Minute, 10*time.Second).Should(BeTrue(),
				"pprof endpoint should be available after enabling profiling")
		})
	})

	Context("Profiling disable without restart", func() {
		const clusterName = "features-profiling-disable"

		It("should disable pprof at runtime without restarting Robin", func() {
			By("creating a cluster with profiling enabled")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Profiling: &redkeyv1beta1.RobinConfigProfiling{
					Enabled: ptr.To(true),
				},
			}
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("finding the Robin pod and recording UID")
			robinPods := &corev1.PodList{}
			err = k8sClient.List(ctx, robinPods, client.InNamespace(clusterNs),
				client.MatchingLabels{
					"redkey.inditex.dev/cluster":   clusterName,
					"redkey.inditex.dev/component": "robin",
				})
			Expect(err).NotTo(HaveOccurred())
			Expect(robinPods.Items).NotTo(BeEmpty())
			robinPodName := robinPods.Items[0].Name
			originalUID := string(robinPods.Items[0].UID)

			By("verifying pprof IS available when enabled")
			Eventually(func() bool {
				cmd := "wget -qO- http://localhost:8080/debug/pprof/ 2>/dev/null" +
					" || curl -s http://localhost:8080/debug/pprof/ 2>/dev/null"
				stdout, _, err := framework.ExecInPod(clusterNs, robinPodName, cmd)
				if err != nil {
					return false
				}
				return containsString(stdout, "Types of profiles available") || containsString(stdout, "pprof")
			}, 3*time.Minute, 10*time.Second).Should(BeTrue())

			By("disabling profiling via spec update")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Robin.Config.Profiling.Enabled = ptr.To(false)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying pprof is NOT available after disabling")
			Eventually(func() bool {
				cmd := "wget -qO- --spider http://localhost:8080/debug/pprof/ 2>&1" +
					" || curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/debug/pprof/ 2>/dev/null"
				stdout, _, err := framework.ExecInPod(clusterNs, robinPodName, cmd)
				if err != nil {
					return true
				}
				return stdout != "200" && !containsString(stdout, "Types of profiles available")
			}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
				"pprof should NOT be available after disabling")

			By("verifying Robin pod was NOT restarted")
			currentUID := getRobinPodUID(ctx, clusterNs, clusterName)
			Expect(currentUID).To(Equal(originalUID),
				"Robin pod should NOT be restarted for profiling toggle")
		})
	})

	Context("Profiling persists across Robin restart", func() {
		const clusterName = "features-profiling-restart"

		It("should re-enable pprof after Robin pod is recreated", func() {
			By("creating a cluster with profiling enabled")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.RobinConfig = &redkeyv1beta1.RobinConfig{
				Profiling: &redkeyv1beta1.RobinConfigProfiling{
					Enabled: ptr.To(true),
				},
			}
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying pprof is available")
			robinPods := &corev1.PodList{}
			err = k8sClient.List(ctx, robinPods, client.InNamespace(clusterNs),
				client.MatchingLabels{
					"redkey.inditex.dev/cluster":   clusterName,
					"redkey.inditex.dev/component": "robin",
				})
			Expect(err).NotTo(HaveOccurred())
			Expect(robinPods.Items).NotTo(BeEmpty())
			robinPodName := robinPods.Items[0].Name

			Eventually(func() bool {
				cmd := "wget -qO- http://localhost:8080/debug/pprof/ 2>/dev/null" +
					" || curl -s http://localhost:8080/debug/pprof/ 2>/dev/null"
				stdout, _, err := framework.ExecInPod(clusterNs, robinPodName, cmd)
				if err != nil {
					return false
				}
				return containsString(stdout, "Types of profiles available") || containsString(stdout, "pprof")
			}, 3*time.Minute, 10*time.Second).Should(BeTrue())

			By("force-deleting the Robin pod")
			err = k8sClient.Delete(ctx, &robinPods.Items[0])
			Expect(err).NotTo(HaveOccurred())

			By("waiting for a new Robin pod to be Running")
			Eventually(func() bool {
				pods := &corev1.PodList{}
				if err := k8sClient.List(ctx, pods, client.InNamespace(clusterNs),
					client.MatchingLabels{
						"redkey.inditex.dev/cluster":   clusterName,
						"redkey.inditex.dev/component": "robin",
					}); err != nil {
					return false
				}
				for _, pod := range pods.Items {
					if pod.DeletionTimestamp == nil && pod.Status.Phase == corev1.PodRunning {
						return true
					}
				}
				return false
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
				"New Robin pod should be running after deletion")

			By("verifying pprof is still available on the new Robin pod")
			Eventually(func() bool {
				pods := &corev1.PodList{}
				if err := k8sClient.List(ctx, pods, client.InNamespace(clusterNs),
					client.MatchingLabels{
						"redkey.inditex.dev/cluster":   clusterName,
						"redkey.inditex.dev/component": "robin",
					}); err != nil {
					return false
				}
				for _, pod := range pods.Items {
					if pod.DeletionTimestamp != nil || pod.Status.Phase != corev1.PodRunning {
						continue
					}
					cmd := "wget -qO- http://localhost:8080/debug/pprof/ 2>/dev/null" +
						" || curl -s http://localhost:8080/debug/pprof/ 2>/dev/null"
					stdout, _, err := framework.ExecInPod(clusterNs, pod.Name, cmd)
					if err != nil {
						return false
					}
					return containsString(stdout, "Types of profiles available") || containsString(stdout, "pprof")
				}
				return false
			}, 3*time.Minute, 10*time.Second).Should(BeTrue(),
				"pprof should remain enabled after Robin pod restart (config from CRD)")
		})
	})
})

// truncate returns the first n characters of s, or s if it's shorter.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// containsString checks if s contains substr.
func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}
