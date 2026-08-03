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

	redkeyv1beta1 "github.com/inditextech/redkey-operator/api/v1beta1"
	"github.com/inditextech/redkey-operator/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// healthRemediationMatrix tests all combinations of cluster type × problem type.
// Cluster types tested here complement the existing health_remediation_test.go which covers:
//   - Ephemeral, no replicas, no auth (meet/forget, slot fix, rebalance, pod deletion)
//   - Ephemeral, replicas, auth (meet/forget)
//   - PVC, no replicas, no auth (pod deletion with data persistence)
//
// This file covers the remaining matrix cells:
//   - Ephemeral, replicas, no auth
//   - Ephemeral, no replicas, auth
//   - Ephemeral, replicas, auth (slot fix, rebalance — meet/forget already tested)

var _ = Describe("Health Remediation Matrix - Replicas without auth", Ordered, Label("health"), func() {
	var (
		suiteCtx  context.Context
		ctx       context.Context
		ns        *corev1.Namespace
		clusterNs string
	)

	framework.SetupSpecContexts(&suiteCtx, &ctx, 20*time.Minute)

	const (
		clusterName  = "health-matrix-replicas"
		expectedPods = 6 // 3 primaries + 3 replicas
	)

	BeforeAll(func() {
		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-health-matrix-rep")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)

		By("creating an ephemeral cluster with replicas, no auth")
		opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
		_, err = framework.CreateRedkey(suiteCtx, k8sClient, opts)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the cluster to reach Ready")
		_, err = framework.WaitForClusterReady(suiteCtx, k8sClient,
			types.NamespacedName{Name: clusterName, Namespace: clusterNs},
			framework.DefaultTimeout)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		if ns != nil {
			_ = framework.DeleteNamespace(suiteCtx, k8sClient, ns)
		}
	})

	AfterEach(func() {
		framework.CollectDebugInfoOnFailure(k8sClient, clusterNs)
		// Wait for cluster to stabilize between tests
		_, _ = framework.WaitForClusterReady(suiteCtx, k8sClient,
			types.NamespacedName{Name: clusterName, Namespace: clusterNs},
			framework.HealthTimeout)
	})

	Context("Meet/Forget recovery", func() {
		It("should recover when a node is forgotten from the cluster with replicas", func() {
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("getting a replica node ID to forget")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())

			var targetNodeID string
			for _, node := range nodes {
				if node.IsReplica() {
					targetNodeID = node.ID
					break
				}
			}
			Expect(targetNodeID).NotTo(BeEmpty())
			_, _ = fmt.Fprintf(GinkgoWriter, "Forgetting replica node: %s\n", targetNodeID)

			By("executing CLUSTER FORGET on all other nodes")
			for _, pod := range podNames {
				nodeID, _ := framework.GetNodeID(clusterNs, pod)
				if nodeID == targetNodeID {
					continue
				}
				_ = framework.ForgetNode(clusterNs, pod, targetNodeID)
			}

			By("waiting for Robin to repair the cluster")
			// Until Redis' ~60s CLUSTER FORGET blacklist expires the re-met node flaps in and out
			// of the cluster view. Assert health and distribution together in one retried block so
			// a transient flap-up is not observed as stable recovery.
			Eventually(func(g Gomega) {
				g.Expect(framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods)).To(Succeed())
				nodesAfter, err := framework.GetClusterNodes(clusterNs, podNames[0])
				g.Expect(err).NotTo(HaveOccurred())
				primaries, replicas := countPrimariesAndReplicas(nodesAfter)
				g.Expect(primaries).To(Equal(3))
				g.Expect(replicas).To(Equal(3))
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed(),
				"Robin should restore the forgotten replica")
		})
	})

	Context("Slot coverage fix", func() {
		It("should fix missing slots in a cluster with replicas", func() {
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("removing slots from a primary node")
			slotsToRemove := make([]int, 10)
			for i := range slotsToRemove {
				slotsToRemove[i] = i
			}
			err = framework.DelSlots(clusterNs, podNames[0], slotsToRemove)
			Expect(err).NotTo(HaveOccurred())

			By("verifying slots are missing")
			info, err := framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.SlotsAssigned).To(BeNumerically("<", 16384))

			By("waiting for Robin to fix slot coverage")
			Eventually(func() error {
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods)
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed())

			By("verifying all slots are covered")
			info, err = framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.SlotsAssigned).To(Equal(16384))
			Expect(info.State).To(Equal("ok"))
		})
	})

	Context("Rebalancing", func() {
		It("should rebalance slots in a cluster with replicas", func() {
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("getting cluster nodes to identify primaries")
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

			By("putting the cluster in Maintenance mode")
			err = framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs,
				redkeyv1beta1.ClusterStatusMaintenance)
			Expect(err).NotTo(HaveOccurred())

			By("resharding slots to create imbalance")
			reshardCmd := fmt.Sprintf(
				"redis-cli --cluster reshard localhost:6379 --cluster-from %s --cluster-to %s --cluster-slots 1000 --cluster-yes",
				sourcePrimary.ID, targetPrimary.ID)
			_, _, _ = framework.ExecInPod(clusterNs, podNames[0], reshardCmd)

			By("restoring cluster to Ready")
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

			By("verifying cluster is healthy after rebalance")
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("Health Remediation Matrix - No replicas with auth", Ordered, Label("health"), func() {
	var (
		suiteCtx  context.Context
		ctx       context.Context
		ns        *corev1.Namespace
		clusterNs string
	)

	framework.SetupSpecContexts(&suiteCtx, &ctx, 20*time.Minute)

	const (
		clusterName  = "health-matrix-auth"
		secretName   = "health-matrix-auth-secret"
		password     = "health-matrix-pass"
		expectedPods = 3
	)

	BeforeAll(func() {
		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-health-matrix-auth")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)

		By("creating auth secret")
		err = framework.CreateAuthSecret(suiteCtx, k8sClient, clusterNs, secretName, password)
		Expect(err).NotTo(HaveOccurred())

		By("creating an ephemeral cluster with auth, no replicas")
		opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithAuth(secretName)
		_, err = framework.CreateRedkey(suiteCtx, k8sClient, opts)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the cluster to reach Ready")
		_, err = framework.WaitForClusterReady(suiteCtx, k8sClient,
			types.NamespacedName{Name: clusterName, Namespace: clusterNs},
			framework.DefaultTimeout)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		if ns != nil {
			_ = framework.DeleteNamespace(suiteCtx, k8sClient, ns)
		}
	})

	AfterEach(func() {
		framework.CollectDebugInfoOnFailure(k8sClient, clusterNs)
		_, _ = framework.WaitForClusterReady(suiteCtx, k8sClient,
			types.NamespacedName{Name: clusterName, Namespace: clusterNs},
			framework.HealthTimeout)
	})

	Context("Meet/Forget recovery with auth", func() {
		It("should recover a forgotten node in an authenticated cluster without replicas", func() {
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("getting node ID of the target pod to forget")
			targetPod := podNames[2]
			targetNodeID, err := framework.GetNodeID(clusterNs, targetPod, password)
			Expect(err).NotTo(HaveOccurred())

			By("executing CLUSTER FORGET on all other nodes")
			for _, pod := range podNames {
				if pod == targetPod {
					continue
				}
				_ = framework.ForgetNode(clusterNs, pod, targetNodeID, password)
			}

			By("waiting for Robin to repair")
			// Until Redis' ~60s CLUSTER FORGET blacklist expires the re-met node flaps in and out
			// of the cluster view. Assert the full recovered state in one retried block so a
			// transient flap-up is not observed as success while a one-shot check later catches a
			// flap-down.
			Eventually(func(g Gomega) {
				g.Expect(framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods, password)).To(Succeed())
				info, err := framework.GetClusterInfo(clusterNs, podNames[0], password)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(info.State).To(Equal("ok"))
				g.Expect(info.KnownNodes).To(Equal(3))
				g.Expect(info.SlotsAssigned).To(Equal(16384))
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed())
		})
	})

	Context("Slot coverage fix with auth", func() {
		It("should fix missing slots in an authenticated cluster", func() {
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("removing slots")
			slotsToRemove := make([]int, 10)
			for i := range slotsToRemove {
				slotsToRemove[i] = i
			}
			err = framework.DelSlots(clusterNs, podNames[0], slotsToRemove, password)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Robin to fix slot coverage")
			Eventually(func() error {
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods, password)
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed())

			info, err := framework.GetClusterInfo(clusterNs, podNames[0], password)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.SlotsAssigned).To(Equal(16384))
		})
	})

	Context("Rebalancing with auth", func() {
		It("should rebalance slots in an authenticated cluster", func() {
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0], password)
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

			By("putting the cluster in Maintenance mode")
			err = framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs,
				redkeyv1beta1.ClusterStatusMaintenance)
			Expect(err).NotTo(HaveOccurred())

			By("resharding slots to create imbalance")
			reshardFmt := "redis-cli -a %s --cluster reshard localhost:6379" +
				" --cluster-from %s --cluster-to %s --cluster-slots 1000 --cluster-yes"
			reshardCmd := fmt.Sprintf(reshardFmt, password, sourcePrimary.ID, targetPrimary.ID)
			_, _, _ = framework.ExecInPod(clusterNs, podNames[0], reshardCmd)

			By("restoring cluster to Ready")
			err = framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs,
				redkeyv1beta1.ClusterStatusReady)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Robin to rebalance")
			Eventually(func() bool {
				nodes, err := framework.GetClusterNodes(clusterNs, podNames[0], password)
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

			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods, password)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("Health Remediation Matrix - Replicas with auth", Ordered, Label("health"), func() {
	var (
		suiteCtx  context.Context
		ctx       context.Context
		ns        *corev1.Namespace
		clusterNs string
	)

	framework.SetupSpecContexts(&suiteCtx, &ctx, 20*time.Minute)

	const (
		clusterName  = "health-matrix-rep-auth"
		secretName   = "health-matrix-rep-auth-secret"
		password     = "health-matrix-rep-pass"
		expectedPods = 6
	)

	BeforeAll(func() {
		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-health-matrix-ra")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)

		By("creating auth secret")
		err = framework.CreateAuthSecret(suiteCtx, k8sClient, clusterNs, secretName, password)
		Expect(err).NotTo(HaveOccurred())

		By("creating an ephemeral cluster with replicas and auth")
		opts := framework.DefaultClusterOptions(clusterName, clusterNs).
			WithReplicas(1).
			WithAuth(secretName)
		_, err = framework.CreateRedkey(suiteCtx, k8sClient, opts)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the cluster to reach Ready")
		_, err = framework.WaitForClusterReady(suiteCtx, k8sClient,
			types.NamespacedName{Name: clusterName, Namespace: clusterNs},
			framework.DefaultTimeout)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		if ns != nil {
			_ = framework.DeleteNamespace(suiteCtx, k8sClient, ns)
		}
	})

	AfterEach(func() {
		framework.CollectDebugInfoOnFailure(k8sClient, clusterNs)
		_, _ = framework.WaitForClusterReady(suiteCtx, k8sClient,
			types.NamespacedName{Name: clusterName, Namespace: clusterNs},
			framework.HealthTimeout)
	})

	Context("Slot coverage fix with replicas and auth", func() {
		It("should fix missing slots in a cluster with replicas and auth", func() {
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("removing slots from a primary")
			slotsToRemove := make([]int, 10)
			for i := range slotsToRemove {
				slotsToRemove[i] = i
			}
			err = framework.DelSlots(clusterNs, podNames[0], slotsToRemove, password)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Robin to fix slot coverage")
			Eventually(func() error {
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods, password)
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed())

			info, err := framework.GetClusterInfo(clusterNs, podNames[0], password)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.SlotsAssigned).To(Equal(16384))
			Expect(info.State).To(Equal("ok"))
		})
	})

	Context("Rebalancing with replicas and auth", func() {
		It("should rebalance slots in a cluster with replicas and auth", func() {
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0], password)
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

			By("putting the cluster in Maintenance mode")
			err = framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs,
				redkeyv1beta1.ClusterStatusMaintenance)
			Expect(err).NotTo(HaveOccurred())

			By("resharding slots to create imbalance")
			reshardFmt := "redis-cli -a %s --cluster reshard localhost:6379" +
				" --cluster-from %s --cluster-to %s --cluster-slots 1000 --cluster-yes"
			reshardCmd := fmt.Sprintf(reshardFmt, password, sourcePrimary.ID, targetPrimary.ID)
			_, _, _ = framework.ExecInPod(clusterNs, podNames[0], reshardCmd)

			By("restoring cluster to Ready")
			err = framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs,
				redkeyv1beta1.ClusterStatusReady)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Robin to rebalance")
			Eventually(func() bool {
				nodes, err := framework.GetClusterNodes(clusterNs, podNames[0], password)
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

			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods, password)
			Expect(err).NotTo(HaveOccurred())

			By("verifying primary/replica distribution after rebalance")
			nodesAfter, err := framework.GetClusterNodes(clusterNs, podNames[0], password)
			Expect(err).NotTo(HaveOccurred())
			primaries, replicas := countPrimariesAndReplicas(nodesAfter)
			Expect(primaries).To(Equal(3))
			Expect(replicas).To(Equal(3))
		})
	})
})

// countPrimariesAndReplicas counts primary and replica nodes from a list of ClusterNodes.
func countPrimariesAndReplicas(nodes []framework.ClusterNode) (int, int) {
	primaries := 0
	replicas := 0
	for _, n := range nodes {
		if n.IsPrimary() {
			primaries++
		}
		if n.IsReplica() {
			replicas++
		}
	}
	return primaries, replicas
}
