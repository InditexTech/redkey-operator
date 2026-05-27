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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Cluster Creation", Ordered, Label("creation"), func() {
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
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-creation")
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

	Context("Ephemeral cluster without replicas", func() {
		const clusterName = "ephemeral-noreplica"

		It("should create a cluster with 3 primaries and reach Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0

			By("creating the RedkeyCluster")
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

			By("verifying the correct number of pods are running")
			expectedPods := 3
			err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName),
				expectedPods, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying that no PVCs were created")
			pvcs := &corev1.PersistentVolumeClaimList{}
			err = k8sClient.List(ctx, pvcs, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
			Expect(err).NotTo(HaveOccurred())
			Expect(pvcs.Items).To(BeEmpty())

			By("verifying the Redis cluster is healthy via CLUSTER INFO")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())
			Expect(podNames).To(HaveLen(expectedPods))

			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("verifying CLUSTER INFO details")
			info, err := framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.State).To(Equal("ok"))
			Expect(info.SlotsAssigned).To(Equal(16384))
			Expect(info.KnownNodes).To(Equal(expectedPods))
			Expect(info.ClusterSize).To(Equal(3))

			By("verifying the active config reached Applied phase")
			config, err := framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(config.Status.ConfigPhase).To(Equal(redkeyv1beta1.ConfigPhaseApplied))
			Expect(config.Status.Status).To(Equal(redkeyv1beta1.ClusterStatusReady))
		})
	})

	Context("Ephemeral cluster with replicas", func() {
		const clusterName = "ephemeral-replica"

		It("should create a cluster with 3 primaries and 1 replica each and reach Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)

			By("creating the RedkeyCluster")
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

			By("verifying 6 pods are running (3 primaries + 3 replicas)")
			expectedPods := 6
			err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName),
				expectedPods, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying that no PVCs were created")
			pvcs := &corev1.PersistentVolumeClaimList{}
			err = k8sClient.List(ctx, pvcs, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
			Expect(err).NotTo(HaveOccurred())
			Expect(pvcs.Items).To(BeEmpty())

			By("verifying the Redis cluster is healthy")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("verifying correct primary/replica distribution via CLUSTER NODES")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(nodes).To(HaveLen(expectedPods))

			primaries := 0
			replicas := 0
			for _, node := range nodes {
				if node.IsPrimary() {
					primaries++
				}
				if node.IsReplica() {
					replicas++
				}
			}
			Expect(primaries).To(Equal(3))
			Expect(replicas).To(Equal(3))
		})
	})

	Context("PVC cluster without replicas", func() {
		const clusterName = "pvc-noreplica"

		It("should create a cluster with persistent storage and reach Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")

			By("creating the RedkeyCluster")
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

			By("verifying 3 pods are running")
			expectedPods := 3
			err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName),
				expectedPods, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying PVCs were created")
			pvcs := &corev1.PersistentVolumeClaimList{}
			err = k8sClient.List(ctx, pvcs, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
			Expect(err).NotTo(HaveOccurred())
			Expect(pvcs.Items).To(HaveLen(expectedPods))

			By("verifying the Redis cluster is healthy")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("PVC cluster with replicas", func() {
		const clusterName = "pvc-replica"

		It("should create a cluster with PVC storage and replicas and reach Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi").WithReplicas(1)

			By("creating the RedkeyCluster")
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

			By("verifying 6 pods are running (3 primaries + 3 replicas)")
			expectedPods := 6
			err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName),
				expectedPods, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying PVCs were created for all pods")
			pvcs := &corev1.PersistentVolumeClaimList{}
			err = k8sClient.List(ctx, pvcs, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
			Expect(err).NotTo(HaveOccurred())
			Expect(pvcs.Items).To(HaveLen(expectedPods))

			By("verifying the Redis cluster is healthy")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("verifying correct primary/replica distribution")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())

			primaries := 0
			replicas := 0
			for _, node := range nodes {
				if node.IsPrimary() {
					primaries++
				}
				if node.IsReplica() {
					replicas++
				}
			}
			Expect(primaries).To(Equal(3))
			Expect(replicas).To(Equal(3))
		})
	})

	Context("Replica spread verification", func() {
		const clusterName = "replica-spread"

		It("should distribute replicas across different primaries", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)

			By("creating the RedkeyCluster")
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			expectedPods := 6
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("verifying each primary has exactly one replica")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())

			// Count replicas per primary
			replicasPerPrimary := make(map[string]int)
			for _, node := range nodes {
				if node.IsPrimary() {
					if _, ok := replicasPerPrimary[node.ID]; !ok {
						replicasPerPrimary[node.ID] = 0
					}
				}
			}
			for _, node := range nodes {
				if node.IsReplica() && node.MasterID != "-" {
					replicasPerPrimary[node.MasterID]++
				}
			}

			_, _ = fmt.Fprintf(GinkgoWriter, "Replica distribution:\n")
			for primaryID, count := range replicasPerPrimary {
				_, _ = fmt.Fprintf(GinkgoWriter, "  Primary %s: %d replicas\n", primaryID[:8], count)
				Expect(count).To(Equal(1),
					"Each primary should have exactly 1 replica, primary %s has %d", primaryID[:8], count)
			}
			Expect(replicasPerPrimary).To(HaveLen(3), "Should have 3 primaries")
		})
	})

	Context("Large cluster with 5 primaries", func() {
		const clusterName = "large-cluster"

		It("should create a cluster with 5 primaries and reach Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 5

			By("creating the RedkeyCluster with 5 primaries")
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

			By("verifying 5 pods are running")
			expectedPods := 5
			err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName),
				expectedPods, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying CLUSTER INFO")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			info, err := framework.GetClusterInfo(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(info.State).To(Equal("ok"))
			Expect(info.SlotsAssigned).To(Equal(16384))
			Expect(info.KnownNodes).To(Equal(5))
			Expect(info.ClusterSize).To(Equal(5))

			By("verifying reasonable slot distribution across 5 primaries")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0])
			Expect(err).NotTo(HaveOccurred())

			for _, n := range nodes {
				if n.IsPrimary() && len(n.Slots) > 0 {
					slotCount := countSlotsFromEntries(n.Slots)
					// 16384/5 ≈ 3277 each, allow range 2500-4200
					Expect(slotCount).To(BeNumerically(">=", 2500),
						"Primary %s has too few slots: %d", n.ID[:8], slotCount)
					Expect(slotCount).To(BeNumerically("<=", 4200),
						"Primary %s has too many slots: %d", n.ID[:8], slotCount)
				}
			}

			By("verifying data operations work on 5-primary cluster")
			err = framework.InsertKeys(clusterNs, podNames[0], 20)
			Expect(err).NotTo(HaveOccurred())

			total, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(20))
		})
	})
})

// countSlotsFromEntries counts the total number of slots from a CLUSTER NODES slot representation.
func countSlotsFromEntries(slotEntries []string) int {
	count := 0
	for _, entry := range slotEntries {
		if len(entry) > 0 && entry[0] == '[' {
			continue
		}
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
