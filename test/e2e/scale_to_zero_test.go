// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// waitForScaledToZero waits until the cluster has Phase=Ready, 0 replicas, no Redis pods,
// no StatefulSet, and no Robin deployment.
func waitForScaledToZero(ctx context.Context, clusterName, clusterNs string) {
	key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

	By("waiting for the cluster to reach Ready with 0 replicas")
	Eventually(func(g Gomega) {
		cluster := &redkeyv1beta1.RedkeyCluster{}
		g.Expect(k8sClient.Get(ctx, key, cluster)).To(Succeed())
		g.Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))
		g.Expect(cluster.Status.Replicas).To(Equal(int32(0)))
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())

	By("verifying no Redis pods exist")
	Eventually(func(g Gomega) {
		pods := &corev1.PodList{}
		g.Expect(k8sClient.List(ctx, pods,
			client.InNamespace(clusterNs),
			client.MatchingLabels(framework.RedisPodLabels(clusterName)),
		)).To(Succeed())
		g.Expect(pods.Items).To(BeEmpty())
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())

	By("verifying StatefulSet is gone")
	Eventually(func() bool {
		var sts appsv1.StatefulSet
		err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, &sts)
		return errors.IsNotFound(err)
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(BeTrue())

	By("verifying Robin deployment is gone")
	Eventually(func() bool {
		var deploy appsv1.Deployment
		err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterName + "-robin", Namespace: clusterNs}, &deploy)
		return errors.IsNotFound(err)
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(BeTrue())

	By("verifying no RedkeyClusterConfigs remain")
	Eventually(func(g Gomega) {
		configs := &redkeyv1beta1.RedkeyClusterConfigList{}
		g.Expect(k8sClient.List(ctx, configs,
			client.InNamespace(clusterNs),
			client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName},
		)).To(Succeed())
		g.Expect(configs.Items).To(BeEmpty())
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())
}

// waitForScaledToZeroConditions verifies the status conditions after scaling to zero.
func waitForScaledToZeroConditions(ctx context.Context, clusterName, clusterNs string) {
	key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

	By("verifying Ready condition with ScaledToZero reason")
	cluster := &redkeyv1beta1.RedkeyCluster{}
	Expect(k8sClient.Get(ctx, key, cluster)).To(Succeed())

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
}

var _ = Describe("Scale to Zero", Ordered, Label("scale-to-zero"), func() {
	var (
		ctx       context.Context
		cancel    context.CancelFunc
		ns        *corev1.Namespace
		clusterNs string
	)

	BeforeAll(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 25*time.Minute)

		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-scale-zero")
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

	// --- Creation with 0 primaries: operator creates nothing (total no-op) ---

	Context("Creation with 0 primaries - ephemeral", func() {
		const clusterName = "create-zero-ephemeral"

		It("creates a cluster with 0 primaries and stays empty", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 0

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledToZero(ctx, clusterName, clusterNs)
			waitForScaledToZeroConditions(ctx, clusterName, clusterNs)
		})
	})

	Context("Creation with 0 primaries - ephemeral with replicas", func() {
		const clusterName = "create-zero-ephemeral-rep"

		It("creates a cluster with 0 primaries (replicas configured) and stays empty", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 0

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})
	})

	Context("Creation with 0 primaries - storage with deletePVC=true", func() {
		const clusterName = "create-zero-storage-del"

		It("creates a cluster with 0 primaries (storage mode, deletePVC=true) and stays empty", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 0

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})
	})

	Context("Creation with 0 primaries - storage with deletePVC=false", func() {
		const clusterName = "create-zero-storage-keep"

		It("creates a cluster with 0 primaries (storage mode, deletePVC=false) and stays empty", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 0
			// Override deletePVC to false
			cluster := opts.BuildRedkeyCluster()
			cluster.Spec.DeletePVC = ptr.To(false)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})
	})

	// --- Scale from 0 to >0: operator creates everything from scratch ---

	Context("Scale 0→3 - ephemeral", func() {
		const clusterName = "zero-to-three-ephemeral"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a cluster with 0 primaries", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 0

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})

		It("scales up from 0 to 3 primaries", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 3
			})

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})
	})

	Context("Scale 0→3 - ephemeral with replicas", func() {
		const clusterName = "zero-to-three-eph-rep"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a cluster with 0 primaries and configured replicas", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 0

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})

		It("scales up from 0 to 3 primaries with replicas", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 3
			})

			// 3 primaries + 3 replicas = 6 nodes
			waitForScaledCluster(ctx, clusterName, clusterNs, 6)
		})
	})

	Context("Scale 0→3 - storage with deletePVC=true", func() {
		const clusterName = "zero-to-three-stor-del"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a cluster with 0 primaries (storage, deletePVC=true)", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 0

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})

		It("scales up from 0 to 3 primaries with storage", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 3
			})

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})
	})

	Context("Scale 0→3 - storage with deletePVC=false", func() {
		const clusterName = "zero-to-three-stor-keep"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a cluster with 0 primaries (storage, deletePVC=false)", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 0
			cluster := opts.BuildRedkeyCluster()
			cluster.Spec.DeletePVC = ptr.To(false)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})

		It("scales up from 0 to 3 primaries with storage (PVCs preserved)", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 3
			})

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})
	})

	// --- Scale from >0 to 0: Robin deletes its objects, operator cleans up ---

	Context("Scale 3→0 - ephemeral", func() {
		const clusterName = "three-to-zero-ephemeral"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a 3-primary ephemeral cluster", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})

		It("scales down to 0 primaries", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)
			waitForScaledToZeroConditions(ctx, clusterName, clusterNs)
		})
	})

	Context("Scale 3→0 - ephemeral with replicas", func() {
		const clusterName = "three-to-zero-eph-rep"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a 3-primary/1-replica ephemeral cluster", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			opts.Primaries = 3

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledCluster(ctx, clusterName, clusterNs, 6)
		})

		It("scales down to 0 primaries", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})
	})

	Context("Scale 3→0 - storage with deletePVC=true", func() {
		const clusterName = "three-to-zero-stor-del"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a 3-primary cluster with persistent storage", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 3

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})

		It("scales down to 0 primaries and deletes PVCs", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)

			By("verifying PVCs are deleted")
			Eventually(func(g Gomega) {
				pvcs := &corev1.PersistentVolumeClaimList{}
				g.Expect(k8sClient.List(ctx, pvcs,
					client.InNamespace(clusterNs),
					client.MatchingLabels(framework.RedisPodLabels(clusterName)),
				)).To(Succeed())
				g.Expect(pvcs.Items).To(BeEmpty())
			}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())
		})
	})

	Context("Scale 3→0 - storage with deletePVC=false", func() {
		const clusterName = "three-to-zero-stor-keep"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a 3-primary cluster with persistent storage (deletePVC=false)", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi")
			opts.Primaries = 3
			cluster := opts.BuildRedkeyCluster()
			cluster.Spec.DeletePVC = ptr.To(false)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})

		It("scales down to 0 primaries but preserves PVCs", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)

			By("verifying PVCs are preserved")
			pvcs := &corev1.PersistentVolumeClaimList{}
			Expect(k8sClient.List(ctx, pvcs,
				client.InNamespace(clusterNs),
				client.MatchingLabels(framework.RedisPodLabels(clusterName)),
			)).To(Succeed())
			Expect(pvcs.Items).NotTo(BeEmpty(), "PVCs should be preserved when deletePVC=false")
		})
	})

	Context("Scale 3→0 - replicas with storage", func() {
		const clusterName = "three-to-zero-rep-stor"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a 3-primary/1-replica cluster with storage", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi").WithReplicas(1)
			opts.Primaries = 3

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledCluster(ctx, clusterName, clusterNs, 6)
		})

		It("scales down to 0 primaries", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})
	})

	// --- Full cycle tests ---

	Context("Full cycle: 0→3→0", func() {
		const clusterName = "cycle-zero-three-zero"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a cluster with 0 primaries", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 0

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})

		It("scales up from 0 to 3 primaries", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 3
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("inserting keys to verify cluster is functional")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 20)).To(Succeed())
		})

		It("scales back down from 3 to 0 primaries", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)
			waitForScaledToZeroConditions(ctx, clusterName, clusterNs)
		})
	})

	Context("Full cycle: 3→0→3", func() {
		const clusterName = "cycle-three-zero-three"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		It("creates a 3-primary cluster", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("inserting keys")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 30)).To(Succeed())
		})

		It("scales down from 3 to 0 primaries", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})

		It("scales back up from 0 to 3 primaries (fresh cluster)", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 3
			})

			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, 3)

			By("verifying cluster is functional after re-creation")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 10)).To(Succeed())
			size, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			// Data from before scale-to-zero is gone (ephemeral) — only new 10 keys
			Expect(size).To(Equal(10))
		})
	})
})
