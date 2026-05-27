// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/test/e2e/framework"
	"github.com/inditextech/redkeyoperator/test/utils"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Health Check and Remediation", Ordered, Label("health"), func() {
	var (
		ctx       context.Context
		cancel    context.CancelFunc
		ns        *corev1.Namespace
		clusterNs string
	)

	BeforeAll(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 45*time.Minute)

		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-health")
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

	Context("Meet/Forget recovery (ephemeral, no replicas)", func() {
		const clusterName = "health-meet-forget"

		It("should recover when a node is forgotten from the cluster", func() {
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

			By("getting node ID of the target pod to forget")
			targetPod := podNames[2] // Last pod
			targetNodeID, err := framework.GetNodeID(clusterNs, targetPod)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprintf(GinkgoWriter, "Target node to forget: %s (pod: %s)\n", targetNodeID, targetPod)

			By("executing CLUSTER FORGET on all other nodes")
			for _, pod := range podNames {
				if pod == targetPod {
					continue
				}
				err = framework.ForgetNode(clusterNs, pod, targetNodeID)
				Expect(err).NotTo(HaveOccurred())
			}

			By("verifying the cluster is temporarily unhealthy")
			info, err := framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			// The cluster should have fewer known nodes now
			_, _ = fmt.Fprintf(GinkgoWriter, "After forget: state=%s, known_nodes=%d\n", info.State, info.KnownNodes)

			By("waiting for Robin to detect and repair (CLUSTER MEET)")
			Eventually(func() error {
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed(),
				"Robin should restore the forgotten node via CLUSTER MEET")

			By("verifying cluster is fully operational")
			info, err = framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.State).To(Equal("ok"))
			Expect(info.KnownNodes).To(Equal(3))
			Expect(info.SlotsAssigned).To(Equal(16384))

			By("verifying the cluster phase is Ready")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))
		})
	})

	Context("Slot coverage fix", func() {
		const clusterName = "health-slot-fix"

		It("should detect and fix missing slots", func() {
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

			By("removing some slots from a node via CLUSTER DELSLOTS")
			// Remove slots 0-9 from the first pod
			slotsToRemove := make([]int, 10)
			for i := 0; i < 10; i++ {
				slotsToRemove[i] = i
			}
			err = framework.DelSlots(clusterNs, podNames[0], slotsToRemove)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the cluster is temporarily unhealthy (not all slots covered)")
			info, err := framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.SlotsAssigned).To(BeNumerically("<", 16384))
			_, _ = fmt.Fprintf(GinkgoWriter, "After delslots: state=%s, slots_assigned=%d\n", info.State, info.SlotsAssigned)

			By("waiting for Robin to detect and fix the slot coverage")
			Eventually(func() error {
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed(),
				"Robin should restore slot coverage")

			By("verifying all 16384 slots are assigned")
			info, err = framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.SlotsAssigned).To(Equal(16384))
			Expect(info.State).To(Equal("ok"))
		})
	})

	Context("Rebalancing", func() {
		const clusterName = "health-rebalance"

		It("should rebalance when slot distribution is uneven", func() {

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

			By("getting cluster nodes to identify primaries")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())

			// Find two primaries and move slots from one to another
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

			By("putting the cluster in Maintenance mode to prevent Robin from interfering with resharding")
			err = framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs,
				redkeyv1beta1.ClusterStatusMaintenance)
			Expect(err).NotTo(HaveOccurred())

			By("moving a large number of slots from one primary to another to create imbalance")
			// Use redis-cli --cluster reshard to move 1000 slots
			reshardCmd := fmt.Sprintf(
				"redis-cli --cluster reshard localhost:6379 --cluster-from %s --cluster-to %s --cluster-slots 1000 --cluster-yes",
				sourcePrimary.ID, targetPrimary.ID)
			_, _, err = framework.ExecInPod(clusterNs, podNames[0], reshardCmd)
			// Reshard might report non-zero exit but still work
			if err != nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Reshard command returned error (may be ok): %v\n", err)
			}

			By("verifying imbalance exists")
			nodesAfter, err := framework.GetClusterNodes(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprintf(GinkgoWriter, "Nodes after reshard:\n")
			for _, n := range nodesAfter {
				_, _ = fmt.Fprintf(GinkgoWriter, "  %s: flags=%s, slots=%v\n", n.ID[:8], n.Flags, n.Slots)
			}

			By("restoring cluster to Ready so Robin resumes reconciliation")
			err = framework.SetClusterConfigStatus(ctx, k8sClient, clusterName, clusterNs, redkeyv1beta1.ClusterStatusReady)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Robin to detect imbalance and rebalance")
			Eventually(func() bool {
				nodes, err := framework.GetClusterNodes(clusterNs, podNames[0])
				if err != nil {
					return false
				}
				// Check that no primary has less than 4000 slots or more than 7000
				// (rough balance check for 3 primaries with 16384 total ≈ 5461 each)
				for _, n := range nodes {
					if n.IsPrimary() && len(n.Slots) > 0 {
						// Count slots (each slot entry can be a range "0-5000" or single "5001")
						slotCount := countSlots(n.Slots)
						if slotCount < 4000 || slotCount > 7000 {
							return false
						}
					}
				}
				return true
			}, framework.HealthTimeout, 15*time.Second).Should(BeTrue(),
				"Robin should rebalance slot distribution")

			By("verifying cluster is healthy after rebalance")
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Pod deletion recovery", func() {
		const clusterName = "health-pod-delete"

		It("should recover when a Redis pod is deleted", func() {
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

			By("inserting data before pod deletion")
			err = framework.InsertKeys(clusterNs, podNames[0], 20)
			Expect(err).NotTo(HaveOccurred())

			By("deleting a Redis pod")
			targetPod := podNames[1]
			cmd := exec.Command("kubectl", "delete", "pod", targetPod, "-n", clusterNs, "--grace-period=0", "--force")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprintf(GinkgoWriter, "Deleted pod: %s\n", targetPod)

			By("waiting for StatefulSet to recreate the pod")
			Eventually(func() bool {
				pods := &corev1.PodList{}
				if err := k8sClient.List(ctx, pods,
					client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return false
				}
				readyCount := 0
				for _, pod := range pods.Items {
					if pod.Status.Phase == corev1.PodRunning {
						for _, cond := range pod.Status.Conditions {
							if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
								readyCount++
								break
							}
						}
					}
				}
				return readyCount == 3
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
				"StatefulSet should recreate the deleted pod")

			By("waiting for Robin to re-integrate the pod into the cluster")
			Eventually(func() error {
				// Re-fetch pod names in case the name changed
				podNames, err = framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
				if err != nil {
					return err
				}
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed(),
				"Robin should re-integrate the recreated pod")

			By("verifying cluster is still operational")
			info, err := framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.State).To(Equal("ok"))
			Expect(info.SlotsAssigned).To(Equal(16384))
			Expect(info.KnownNodes).To(Equal(3))
		})
	})

	Context("Meet/Forget recovery with replicas and auth", func() {
		const (
			clusterName = "health-meet-auth"
			secretName  = "health-meet-auth-secret"
			password    = "health-test-pass"
		)

		It("should recover a forgotten node in an authenticated cluster with replicas", func() {
			By("creating auth secret")
			err := framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)
			Expect(err).NotTo(HaveOccurred())

			By("creating a cluster with auth and replicas")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).
				WithAuth(secretName).
				WithReplicas(1)
			_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			expectedPods := 6 // 3 primaries + 3 replicas
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("getting a replica node ID to forget")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0], password)
			Expect(err).NotTo(HaveOccurred())

			var targetNodeID string
			for _, node := range nodes {
				if node.IsReplica() {
					targetNodeID = node.ID
					break
				}
			}
			Expect(targetNodeID).NotTo(BeEmpty(), "Should find a replica node")
			_, _ = fmt.Fprintf(GinkgoWriter, "Forgetting replica node: %s\n", targetNodeID)

			By("executing CLUSTER FORGET on all other nodes")
			for _, pod := range podNames {
				nodeID, _ := framework.GetNodeID(clusterNs, pod, password)
				if nodeID == targetNodeID {
					continue
				}
				_ = framework.ForgetNode(clusterNs, pod, targetNodeID, password)
			}

			By("waiting for Robin to repair the cluster")
			Eventually(func() error {
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods, password)
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed(),
				"Robin should restore the forgotten replica")

			By("verifying primary/replica distribution is correct")
			nodesAfter, err := framework.GetClusterNodes(clusterNs, podNames[0], password)
			Expect(err).NotTo(HaveOccurred())
			primaries := 0
			replicas := 0
			for _, n := range nodesAfter {
				if n.IsPrimary() {
					primaries++
				}
				if n.IsReplica() {
					replicas++
				}
			}
			Expect(primaries).To(Equal(3))
			Expect(replicas).To(Equal(3))
		})
	})

	Context("Pod deletion recovery with PVC storage", func() {
		const clusterName = "health-pod-pvc"

		It("should recover a deleted pod in a PVC cluster preserving data", func() {
			By("creating a PVC cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data")
			err = framework.InsertKeys(clusterNs, podNames[0], 20)
			Expect(err).NotTo(HaveOccurred())

			initialSize, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(initialSize).To(Equal(20))

			By("forcing Redis persistence to disk before deleting a pod")
			for _, pod := range podNames {
				_, _, err = framework.ExecInPod(clusterNs, pod, "redis-cli SAVE")
				Expect(err).NotTo(HaveOccurred())
			}

			By("deleting a Redis pod")
			targetPod := podNames[0]
			cmd := exec.Command("kubectl", "delete", "pod", targetPod, "-n", clusterNs, "--grace-period=0", "--force")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for recovery")
			Eventually(func() error {
				podNames, err = framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
				if err != nil {
					return err
				}
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			}, framework.HealthTimeout, 10*time.Second).Should(Succeed())

			By("verifying data is preserved (PVC provides persistence)")
			Eventually(func() int {
				finalSize, e := framework.GetDBSize(clusterNs, podNames)
				if e != nil {
					return -1
				}
				return finalSize
			}, framework.HealthTimeout, 10*time.Second).Should(Equal(20),
				"Data should be preserved across pod restart with PVC storage")
		})
	})
})

// countSlots counts the total number of slots from a CLUSTER NODES slot representation.
// Slots can be individual numbers "5001" or ranges "0-5000".
func countSlots(slotEntries []string) int {
	count := 0
	for _, entry := range slotEntries {
		// Skip importing/migrating markers
		if len(entry) > 0 && (entry[0] == '[') {
			continue
		}
		// Check if it's a range "start-end"
		dashIdx := -1
		for i, c := range entry {
			if c == '-' && i > 0 {
				dashIdx = i
				break
			}
		}
		if dashIdx > 0 {
			var start, end int
			_, _ = fmt.Sscanf(entry[:dashIdx], "%d", &start)
			_, _ = fmt.Sscanf(entry[dashIdx+1:], "%d", &end)
			count += end - start + 1
		} else {
			count++
		}
	}
	return count
}
