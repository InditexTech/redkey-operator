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
	"k8s.io/client-go/util/retry"
)

// updatePrimaries refetches the cluster and updates its primary count, retrying
// on optimistic-concurrency conflicts caused by the operator mutating the object
// concurrently.
func updatePrimaries(ctx context.Context, key types.NamespacedName, primaries int32) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &redkeyv1beta1.RedkeyCluster{}
		if err := k8sClient.Get(ctx, key, cluster); err != nil {
			return err
		}
		cluster.Spec.Primaries = primaries
		return k8sClient.Update(ctx, cluster)
	})
}

var _ = Describe("Config Superseding", Ordered, Label("superseding"), func() {
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
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-superseding")
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

	Context("With skipIfSuperseded enabled", func() {
		const clusterName = "superseding-skip"

		It("should skip intermediate configs and apply only the final one", func() {
			By("creating a cluster with skipIfSuperseded=true")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).
				WithSkipIfSuperseded(true)
			opts.Primaries = 3

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase with 3 primaries")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("rapidly applying 3 config changes (primaries 3→5→7→9)")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

			// Change to 5 primaries
			Expect(updatePrimaries(ctx, key, 5)).NotTo(HaveOccurred())

			// Immediately change to 7 primaries
			Expect(updatePrimaries(ctx, key, 7)).NotTo(HaveOccurred())

			// Immediately change to 9 primaries
			Expect(updatePrimaries(ctx, key, 9)).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready with final config applied")
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.HealthTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying only the final config remains Applied (intermediate configs were skipped and cleaned up)")
			// When skipIfSuperseded=true, Robin marks intermediate configs as Superseded
			// instead of applying them one by one, and the operator then prunes every
			// config older than the last Applied one. The observable end state is therefore
			// a single, final config (highest sequence) that is Applied with the final
			// topology — the intermediate 5- and 7-primary configs are never Applied.
			Eventually(func() bool {
				configs, listErr := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
				if listErr != nil || len(configs) == 0 {
					return false
				}

				var finalIdx = -1
				for i := range configs {
					if finalIdx == -1 || configs[i].Spec.Sequence > configs[finalIdx].Spec.Sequence {
						finalIdx = i
					}
				}

				finalConfig := configs[finalIdx]
				if finalConfig.Status.ConfigPhase != redkeyv1beta1.ConfigPhaseApplied || finalConfig.Spec.Primaries != int32(9) {
					return false
				}

				// No intermediate config (lower sequence than the final) must have reached
				// the Applied phase: they were skipped via superseding.
				for _, cfg := range configs {
					if cfg.Spec.Sequence < finalConfig.Spec.Sequence && cfg.Status.ConfigPhase == redkeyv1beta1.ConfigPhaseApplied {
						return false
					}
				}
				return true
			}, framework.HealthTimeout, framework.DefaultPollInterval).Should(BeTrue(),
				"Expected only the final config (9 primaries) to be Applied with no intermediate config Applied")

			By("verifying the cluster has the correct number of pods")
			expectedPods := 9 // 9 primaries, 0 replicas
			err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName),
				expectedPods, framework.HealthTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Redis cluster is healthy with final configuration")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Without skipIfSuperseded", func() {
		const clusterName = "superseding-noskip"

		It("should apply all configs sequentially when skipIfSuperseded is false", func() {
			By("creating a cluster with skipIfSuperseded=false")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).
				WithSkipIfSuperseded(false)
			opts.Primaries = 3

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("applying two changes (primaries 3→5→7)")
			Expect(updatePrimaries(ctx, key, 5)).NotTo(HaveOccurred())

			// Ensure the operator created the config for primaries=5 before applying the
			// next change, so the two configs are applied sequentially.
			Eventually(func() bool {
				configs, listErr := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
				if listErr != nil {
					return false
				}
				for i := range configs {
					if configs[i].Spec.Primaries == 5 {
						return true
					}
				}
				return false
			}, framework.CreationTimeout, framework.DefaultPollInterval).Should(BeTrue())

			Expect(updatePrimaries(ctx, key, 7)).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready with all configs applied")
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.HealthTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the final config reached Applied with the final topology")
			// With skipIfSuperseded=false each config is applied in turn. Once a newer
			// config becomes Applied the operator prunes the older Applied ones, so the
			// lasting end state is the final config (highest sequence) Applied with 7
			// primaries. No config must end up Superseded.
			Eventually(func() bool {
				configs, listErr := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
				if listErr != nil || len(configs) == 0 {
					return false
				}

				var finalIdx = -1
				for i := range configs {
					if finalIdx == -1 || configs[i].Spec.Sequence > configs[finalIdx].Spec.Sequence {
						finalIdx = i
					}
				}

				finalConfig := configs[finalIdx]
				if finalConfig.Status.ConfigPhase != redkeyv1beta1.ConfigPhaseApplied || finalConfig.Spec.Primaries != int32(7) {
					return false
				}

				// No remaining config must be Superseded when superseding is disabled.
				for _, cfg := range configs {
					if cfg.Status.ConfigPhase == redkeyv1beta1.ConfigPhaseSuperseded {
						return false
					}
				}
				return true
			}, framework.HealthTimeout, framework.DefaultPollInterval).Should(BeTrue(),
				"Expected the final config (7 primaries) to be Applied with no config Superseded")

			By("verifying the cluster has the correct final state")
			expectedPods := 7 // 7 primaries, 0 replicas
			err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName),
				expectedPods, framework.HealthTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
