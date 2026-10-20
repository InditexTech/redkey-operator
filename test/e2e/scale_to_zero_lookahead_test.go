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

	redkeyv1beta1 "github.com/inditextech/redkey-operator/api/v1beta1"
	"github.com/inditextech/redkey-operator/test/e2e/framework"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// nonExistentRedisImage is an image tag that cannot be pulled, used to wedge a config InProgress.
const nonExistentRedisImage = "redis:0.0.0-redkey-e2e-does-not-exist"

// wedgedObservationWindow is how long a config must stay InProgress to be considered wedged.
const wedgedObservationWindow = 30 * time.Second

// waitForWedgedConfig waits until the cluster's latest RedkeyConfig uses nonExistentRedisImage,
// is InProgress with the given cluster status, and stays InProgress (it cannot complete).
func waitForWedgedConfig(ctx context.Context, clusterName, clusterNs, status string) {
	latestConfig := func(g Gomega) redkeyv1beta1.RedkeyConfig {
		configs, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(configs).NotTo(BeEmpty())
		return configs[len(configs)-1]
	}

	By(fmt.Sprintf("waiting for the config with image %s to be InProgress/%s", nonExistentRedisImage, status))
	Eventually(func(g Gomega) {
		cfg := latestConfig(g)
		g.Expect(cfg.Spec.Image).To(Equal(nonExistentRedisImage))
		g.Expect(cfg.Status.ConfigPhase).To(Equal(redkeyv1beta1.ConfigPhaseInProgress))
		g.Expect(cfg.Status.Status).To(Equal(status))
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())

	By("verifying the config is wedged and cannot complete")
	Consistently(func(g Gomega) {
		cfg := latestConfig(g)
		g.Expect(cfg.Spec.Image).To(Equal(nonExistentRedisImage))
		g.Expect(cfg.Status.ConfigPhase).To(Equal(redkeyv1beta1.ConfigPhaseInProgress))
	}, wedgedObservationWindow, framework.DefaultPollInterval).Should(Succeed())
}

// expectNoOrphanedClusterObjects verifies that the Service and ConfigMap managed by Robin
// are gone (StatefulSet, pods, Robin and configs are checked by waitForScaledToZero).
func expectNoOrphanedClusterObjects(ctx context.Context, clusterName, clusterNs string) {
	key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

	By("verifying the cluster Service is gone")
	Eventually(func() bool {
		return errors.IsNotFound(k8sClient.Get(ctx, key, &corev1.Service{}))
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(BeTrue())

	By("verifying the cluster ConfigMap is gone")
	Eventually(func() bool {
		return errors.IsNotFound(k8sClient.Get(ctx, key, &corev1.ConfigMap{}))
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(BeTrue())
}

// expectInProgressNotAbandoned verifies that a pending scale-to-zero config does not supersede
// the wedged InProgress config with nonExistentRedisImage: both stay in place and the StatefulSet is kept.
func expectInProgressNotAbandoned(ctx context.Context, clusterName, clusterNs string) {
	// Robin initialises the config phase to Pending in the same cycle, right before evaluating the
	// lookahead, so seeing Pending proves Robin has already considered the scale-to-zero config.
	By("waiting for Robin to pick up the scale-to-zero config")
	Eventually(func(g Gomega) {
		configs, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(configs).NotTo(BeEmpty())
		latest := configs[len(configs)-1]
		g.Expect(latest.Spec.Primaries).To(BeZero())
		g.Expect(latest.Status.ConfigPhase).To(Equal(redkeyv1beta1.ConfigPhasePending))
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())

	By("verifying the wedged config is not abandoned and the cluster is kept")
	Consistently(func(g Gomega) {
		configs, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
		g.Expect(err).NotTo(HaveOccurred())
		var wedged, zero *redkeyv1beta1.RedkeyConfig
		for i := range configs {
			switch {
			case configs[i].Spec.Primaries == 0:
				zero = &configs[i]
			case configs[i].Spec.Image == nonExistentRedisImage:
				wedged = &configs[i]
			}
		}
		g.Expect(wedged).NotTo(BeNil())
		g.Expect(wedged.Status.ConfigPhase).To(Equal(redkeyv1beta1.ConfigPhaseInProgress))
		g.Expect(zero).NotTo(BeNil())
		g.Expect(zero.Status.ConfigPhase).To(Equal(redkeyv1beta1.ConfigPhasePending))
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs},
			&appsv1.StatefulSet{})).To(Succeed())
	}, wedgedObservationWindow, framework.DefaultPollInterval).Should(Succeed())
}

// listClusterPVCs returns the PVCs of the cluster's Redis pods.
func listClusterPVCs(ctx context.Context, g Gomega, clusterName, clusterNs string) []corev1.PersistentVolumeClaim {
	pvcs := &corev1.PersistentVolumeClaimList{}
	g.Expect(k8sClient.List(ctx, pvcs,
		client.InNamespace(clusterNs),
		client.MatchingLabels(framework.RedisPodLabels(clusterName)),
	)).To(Succeed())
	return pvcs.Items
}

// Robin jumps to a pending scale-to-zero config, superseding intermediate configs. A wedged
// InProgress config is only superseded when scaling to zero discards the data (ephemeral or
// deletePVC=true). Requires a Robin image that includes the scale-to-zero lookahead.
var _ = Describe("Scale-to-zero lookahead", Ordered, Label("scale-to-zero-lookahead"), func() {
	var (
		suiteCtx  context.Context
		ctx       context.Context
		ns        *corev1.Namespace
		clusterNs string
	)

	framework.SetupSpecContexts(&suiteCtx, &ctx, 15*time.Minute)

	BeforeAll(func() {
		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-scale-zero-lookahead")
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

	Context("Upgrade blocked by an invalid image", func() {
		const clusterName = "lookahead-wedged-upgrade"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary ephemeral cluster", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})

		It("gets stuck upgrading to a non-existent image", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Image = nonExistentRedisImage
			})

			waitForWedgedConfig(ctx, clusterName, clusterNs, redkeyv1beta1.ClusterStatusUpgrading)
		})

		It("recovers by scaling to zero", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)
			waitForScaledToZeroConditions(ctx, clusterName, clusterNs)
			expectNoOrphanedClusterObjects(ctx, clusterName, clusterNs)
		})
	})

	Context("Storage cluster deleting its PVCs, upgrade blocked by an invalid image", func() {
		const clusterName = "lookahead-storage-delete"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary storage cluster with deletePVC=true", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi").WithDeletePVC(true)
			opts.Primaries = 3

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})

		It("gets stuck upgrading to a non-existent image", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Image = nonExistentRedisImage
			})

			waitForWedgedConfig(ctx, clusterName, clusterNs, redkeyv1beta1.ClusterStatusUpgrading)
		})

		It("recovers by scaling to zero and deletes the PVCs", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)
			expectNoOrphanedClusterObjects(ctx, clusterName, clusterNs)

			By("verifying PVCs are deleted")
			Eventually(func(g Gomega) {
				g.Expect(listClusterPVCs(ctx, g, clusterName, clusterNs)).To(BeEmpty())
			}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())
		})
	})

	Context("Storage cluster keeping its PVCs, upgrade blocked by an invalid image", func() {
		const clusterName = "lookahead-storage-keep"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary storage cluster with deletePVC=false", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithPVC("100Mi").WithDeletePVC(false)
			opts.Primaries = 3

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForScaledCluster(ctx, clusterName, clusterNs, 3)
		})

		It("gets stuck upgrading to a non-existent image", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Image = nonExistentRedisImage
			})

			waitForWedgedConfig(ctx, clusterName, clusterNs, redkeyv1beta1.ClusterStatusUpgrading)
		})

		It("does not abandon the in-progress upgrade when scaling to zero", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 0
			})

			expectInProgressNotAbandoned(ctx, clusterName, clusterNs)

			By("verifying PVCs are preserved")
			Expect(listClusterPVCs(ctx, Default, clusterName, clusterNs)).NotTo(BeEmpty())
		})
	})

	Context("Creation blocked by an invalid image", func() {
		const clusterName = "lookahead-wedged-creation"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("gets stuck initializing a cluster created with a non-existent image", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			opts.RedisImage = nonExistentRedisImage

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			waitForWedgedConfig(ctx, clusterName, clusterNs, redkeyv1beta1.ClusterStatusInitializing)
		})

		It("recovers by scaling to zero without orphaned objects", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)
			waitForScaledToZeroConditions(ctx, clusterName, clusterNs)
			expectNoOrphanedClusterObjects(ctx, clusterName, clusterNs)
		})
	})
})
