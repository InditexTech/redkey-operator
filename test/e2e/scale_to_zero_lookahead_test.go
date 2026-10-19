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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

// nonExistentRedisImage is an image tag that cannot be pulled, used to wedge a config InProgress.
const nonExistentRedisImage = "redis:0.0.0-redkey-e2e-does-not-exist"

// wedgedObservationWindow is how long a config must stay InProgress to be considered wedged.
const wedgedObservationWindow = 30 * time.Second

// waitForWedgedConfig waits until the cluster's latest RedkeyConfig uses the given image,
// is InProgress with the given cluster status, and stays InProgress (it cannot complete).
func waitForWedgedConfig(ctx context.Context, clusterName, clusterNs, image, status string) {
	latestConfig := func(g Gomega) redkeyv1beta1.RedkeyConfig {
		configs, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(configs).NotTo(BeEmpty())
		return configs[len(configs)-1]
	}

	By(fmt.Sprintf("waiting for the config with image %s to be InProgress/%s", image, status))
	Eventually(func(g Gomega) {
		cfg := latestConfig(g)
		g.Expect(cfg.Spec.Image).To(Equal(image))
		g.Expect(cfg.Status.ConfigPhase).To(Equal(redkeyv1beta1.ConfigPhaseInProgress))
		g.Expect(cfg.Status.Status).To(Equal(status))
	}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())

	By("verifying the config is wedged and cannot complete")
	Consistently(func(g Gomega) {
		cfg := latestConfig(g)
		g.Expect(cfg.Spec.Image).To(Equal(image))
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

// Robin jumps to a pending scale-to-zero config, superseding intermediate (even wedged
// InProgress) configs. Requires a Robin image that includes the scale-to-zero lookahead.
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

			waitForWedgedConfig(ctx, clusterName, clusterNs, nonExistentRedisImage, redkeyv1beta1.ClusterStatusUpgrading)
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

			waitForWedgedConfig(ctx, clusterName, clusterNs, nonExistentRedisImage, redkeyv1beta1.ClusterStatusInitializing)
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
