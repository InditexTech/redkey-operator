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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Additional Features", Ordered, Label("features"), func() {
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
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-additional")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)
	})

	AfterAll(func() {
		if ns != nil {
			_ = framework.DeleteNamespace(suiteCtx, k8sClient, ns)
		}
	})

	AfterEach(func() {
		framework.CollectDebugInfoOnFailure(k8sClient, clusterNs)
	})

	Context("Redis config change propagation", func() {
		const clusterName = "additional-redis-config"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should propagate Redis config changes to all nodes via new RedkeyClusterConfig", func() {
			By("creating a cluster with initial redis config")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Config = `maxmemory 90mb
maxmemory-policy allkeys-lru
protected-mode no
appendonly no
save ""
hz 10`
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())

			By("verifying initial hz config")
			for _, pod := range podNames {
				stdout, _, err := framework.ExecInPod(clusterNs, pod, "redis-cli config get hz")
				Expect(err).NotTo(HaveOccurred())
				Expect(stdout).To(ContainSubstring("10"))
			}

			By("updating Redis config (changing hz and maxmemory-policy)")
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			cluster.Spec.Config = `maxmemory 90mb
maxmemory-policy volatile-lru
protected-mode no
appendonly no
save ""
hz 20`
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying new config is applied on all nodes")
			podNames, err = framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				for _, pod := range podNames {
					stdout, _, err := framework.ExecInPod(clusterNs, pod, "redis-cli config get hz")
					if err != nil {
						return false
					}
					if !containsString(stdout, "20") {
						return false
					}
					stdout, _, err = framework.ExecInPod(clusterNs, pod, "redis-cli config get maxmemory-policy")
					if err != nil {
						return false
					}
					if !containsString(stdout, "volatile-lru") {
						return false
					}
				}
				return true
			}, 3*time.Minute, 10*time.Second).Should(BeTrue(),
				"All nodes should have the updated Redis config")
		})
	})

	Context("Custom labels propagation", func() { //nolint:dupl
		const clusterName = "additional-labels"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should propagate custom labels to StatefulSet pods on creation", func() {
			By("creating a cluster with custom labels")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			labels := map[string]string{
				"app.kubernetes.io/team": "platform",
				"environment":            "e2e-test",
				// Collides with an internal base label — base must always win.
				"redkey.inditex.dev/component": "hijacked",
			}
			opts.Labels = &labels
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying labels are present on Redis pods")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			Expect(podNames).To(HaveLen(3))

			pods := &corev1.PodList{}
			err = k8sClient.List(ctx, pods, client.InNamespace(clusterNs),
				client.MatchingLabels{
					"redkey.inditex.dev/cluster":   clusterName,
					"redkey.inditex.dev/component": "redis",
				})
			Expect(err).NotTo(HaveOccurred())

			for _, pod := range pods.Items {
				Expect(pod.Labels).To(HaveKeyWithValue("app.kubernetes.io/team", "platform"),
					"Pod %s should have custom team label", pod.Name)
				Expect(pod.Labels).To(HaveKeyWithValue("environment", "e2e-test"),
					"Pod %s should have custom environment label", pod.Name)
				// Base label wins over the colliding spec.labels entry.
				Expect(pod.Labels).To(HaveKeyWithValue("redkey.inditex.dev/component", "redis"),
					"Pod %s base component label must win over spec.labels", pod.Name)
			}

			By("verifying labels are present on the StatefulSet object metadata")
			sts := &appsv1.StatefulSet{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, sts)
			Expect(err).NotTo(HaveOccurred())
			Expect(sts.Labels).To(HaveKeyWithValue("app.kubernetes.io/team", "platform"),
				"StatefulSet should carry custom team label")
			Expect(sts.Labels).To(HaveKeyWithValue("redkey.inditex.dev/component", "redis"),
				"StatefulSet base component label must win over spec.labels")
		})

		It("should update custom labels on existing pods when spec changes", func() { //nolint:dupl
			By("updating custom labels")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err := k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			// "environment" is removed on purpose to verify pruning of stale keys.
			updatedLabels := map[string]string{
				"app.kubernetes.io/team": "infra",
			}
			cluster.Spec.Labels = &updatedLabels
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying updated labels (and pruning of removed keys) on pods")
			Eventually(func() bool {
				pods := &corev1.PodList{}
				if err := k8sClient.List(ctx, pods, client.InNamespace(clusterNs),
					client.MatchingLabels{
						"redkey.inditex.dev/cluster":   clusterName,
						"redkey.inditex.dev/component": "redis",
					}); err != nil {
					return false
				}
				for _, pod := range pods.Items {
					if pod.Labels["app.kubernetes.io/team"] != "infra" {
						return false
					}
					// The removed key must be pruned.
					if _, ok := pod.Labels["environment"]; ok {
						return false
					}
				}
				return true
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
				"Updated labels should propagate to pods and removed keys should be pruned")
		})
	})

	Context("Custom annotations propagation", func() { //nolint:dupl
		const clusterName = "additional-annotations"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should propagate custom annotations to StatefulSet pods on creation", func() {
			By("creating a cluster with custom annotations")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			annotations := map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/port":   "9121",
			}
			opts.Annotations = &annotations
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying annotations are present on Redis pods")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			Expect(podNames).To(HaveLen(3))

			pods := &corev1.PodList{}
			err = k8sClient.List(ctx, pods, client.InNamespace(clusterNs),
				client.MatchingLabels{
					"redkey.inditex.dev/cluster":   clusterName,
					"redkey.inditex.dev/component": "redis",
				})
			Expect(err).NotTo(HaveOccurred())

			for _, pod := range pods.Items {
				Expect(pod.Annotations).To(HaveKeyWithValue("prometheus.io/scrape", "true"),
					"Pod %s should have prometheus scrape annotation", pod.Name)
				Expect(pod.Annotations).To(HaveKeyWithValue("prometheus.io/port", "9121"),
					"Pod %s should have prometheus port annotation", pod.Name)
			}
		})

		It("should update custom annotations on existing pods when spec changes", func() { //nolint:dupl
			By("updating custom annotations")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err := k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			updatedAnnotations := map[string]string{
				"prometheus.io/scrape": "false",
			}
			cluster.Spec.Annotations = &updatedAnnotations
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying updated annotations (and pruning of removed keys) on pods")
			Eventually(func() bool {
				pods := &corev1.PodList{}
				if err := k8sClient.List(ctx, pods, client.InNamespace(clusterNs),
					client.MatchingLabels{
						"redkey.inditex.dev/cluster":   clusterName,
						"redkey.inditex.dev/component": "redis",
					}); err != nil {
					return false
				}
				for _, pod := range pods.Items {
					if pod.Annotations["prometheus.io/scrape"] != "false" {
						return false
					}
					// The removed key must be pruned.
					if _, ok := pod.Annotations["prometheus.io/port"]; ok {
						return false
					}
				}
				return true
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
				"Updated annotations should propagate to pods and removed keys should be pruned")
		})
	})

	Context("PDB configuration change", func() {
		const clusterName = "additional-pdb-change"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should create PDB with correct configuration on cluster creation", func() {
			By("creating a cluster with PDB enabled")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Pdb = redkeyv1beta1.Pdb{
				Enabled:            true,
				PdbSizeUnavailable: intstr.FromInt32(1),
			}
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying PDB exists with maxUnavailable=1")
			Eventually(func() bool {
				pdbList := &policyv1.PodDisruptionBudgetList{}
				if err := k8sClient.List(ctx, pdbList, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return false
				}
				if len(pdbList.Items) == 0 {
					return false
				}
				pdb := pdbList.Items[0]
				return pdb.Spec.MaxUnavailable != nil && pdb.Spec.MaxUnavailable.IntValue() == 1
			}, 2*time.Minute, 5*time.Second).Should(BeTrue())
		})

		It("should update PDB when configuration changes and remove when disabled", func() {
			By("updating PDB maxUnavailable to 2")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err := k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Pdb.PdbSizeUnavailable = intstr.FromInt32(2)
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying PDB updated to maxUnavailable=2")
			Eventually(func() bool {
				pdbList := &policyv1.PodDisruptionBudgetList{}
				if err := k8sClient.List(ctx, pdbList, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return false
				}
				if len(pdbList.Items) == 0 {
					return false
				}
				pdb := pdbList.Items[0]
				return pdb.Spec.MaxUnavailable != nil && pdb.Spec.MaxUnavailable.IntValue() == 2
			}, 2*time.Minute, 5*time.Second).Should(BeTrue(),
				"PDB should be updated to maxUnavailable=2")

			By("disabling PDB")
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Pdb.Enabled = false
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying PDB is removed")
			Eventually(func() bool {
				pdbList := &policyv1.PodDisruptionBudgetList{}
				if err := k8sClient.List(ctx, pdbList, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return false
				}
				return len(pdbList.Items) == 0
			}, 2*time.Minute, 5*time.Second).Should(BeTrue(),
				"PDB should be deleted when disabled")
		})
	})

	Context("purgeKeysOnRebalance=false preserves data", func() {
		const clusterName = "additional-purge-false"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should preserve keys during rebalance when purgeKeysOnRebalance is false", func() {
			By("creating an ephemeral cluster with purgeKeysOnRebalance=false")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.PurgeKeysOnRebalance = new(false)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data")
			err = framework.InsertKeys(clusterNs, podNames[0], 30)
			Expect(err).NotTo(HaveOccurred())
			initialSize, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(initialSize).To(Equal(30))

			By("creating imbalance to trigger rebalance")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())

			var sourcePrimary, targetPrimary *framework.ClusterNode
			for i := range nodes {
				if nodes[i].IsPrimary() {
					if sourcePrimary == nil {
						sourcePrimary = &nodes[i]
					} else if targetPrimary == nil {
						targetPrimary = &nodes[i]
						break
					}
				}
			}
			Expect(sourcePrimary).NotTo(BeNil())
			Expect(targetPrimary).NotTo(BeNil())

			err = framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs,
				redkeyv1beta1.ClusterStatusMaintenance)
			Expect(err).NotTo(HaveOccurred())

			reshardCmd := fmt.Sprintf(
				"redis-cli --cluster reshard localhost:6379 --cluster-from %s --cluster-to %s --cluster-slots 1000 --cluster-yes",
				sourcePrimary.ID, targetPrimary.ID)
			_, _, _ = framework.ExecInPod(clusterNs, podNames[0], reshardCmd)

			err = framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs,
				redkeyv1beta1.ClusterStatusReady)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Robin to rebalance")
			Eventually(func() bool {
				nodes, err := framework.GetClusterNodes(clusterNs, podNames[0])
				if err != nil {
					return false
				}
				for _, n := range nodes {
					if n.IsPrimary() && len(n.Slots) > 0 {
						slotCount := countSlots(n.Slots)
						if slotCount < 4000 || slotCount > 7000 {
							return false
						}
					}
				}
				return true
			}, framework.HealthTimeout, 15*time.Second).Should(BeTrue())

			By("verifying data is preserved after rebalance")
			Eventually(func() int {
				size, err := framework.GetDBSize(clusterNs, podNames)
				if err != nil {
					return -1
				}
				return size
			}, 2*time.Minute, 5*time.Second).Should(Equal(30),
				"All 30 keys should be preserved when purgeKeysOnRebalance=false")
		})
	})
})
