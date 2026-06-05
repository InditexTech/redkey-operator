// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// getRedisUpgradeImage returns the target image for upgrade tests.
// Use REDIS_IMAGE_UPGRADE env var to set a custom target, otherwise defaults to "redis:8-alpine"
// (a compatible but different image tag from the default "redis:8-bookworm").
func getRedisUpgradeImage() string {
	if img := os.Getenv("REDIS_IMAGE_UPGRADE"); img != "" {
		return img
	}
	return "redis:8-alpine"
}

var _ = Describe("Redis Image Upgrade", Ordered, Label("upgrade"), func() {
	var (
		ctx       context.Context
		cancel    context.CancelFunc
		ns        *corev1.Namespace
		clusterNs string
	)

	BeforeAll(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 60*time.Minute)

		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-upgrade")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)
		_, _ = fmt.Fprintf(GinkgoWriter, "Upgrade target image: %s\n", getRedisUpgradeImage())
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

	// updateClusterImage updates the cluster's Redis image with conflict-retry.
	// After the update, it waits for the operator to create a new config (higher sequence)
	// to avoid race conditions where waitForUpgradeComplete would pass on the old config.
	updateClusterImage := func(clusterName, newImage string) {
		key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

		// Get current highest sequence before update
		configs := &redkeyv1beta1.RedkeyClusterConfigList{}
		Expect(k8sClient.List(ctx, configs, client.InNamespace(clusterNs),
			client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})).To(Succeed())
		prevMaxSeq := 0
		for i := range configs.Items {
			if configs.Items[i].Spec.Sequence > prevMaxSeq {
				prevMaxSeq = configs.Items[i].Spec.Sequence
			}
		}

		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			cluster := &redkeyv1beta1.RedkeyCluster{}
			if err := k8sClient.Get(ctx, key, cluster); err != nil {
				return err
			}
			cluster.Spec.Image = newImage
			return k8sClient.Update(ctx, cluster)
		})).To(Succeed())

		// Wait for the operator to create a new config with a higher sequence
		Eventually(func() bool {
			cfgs := &redkeyv1beta1.RedkeyClusterConfigList{}
			if err := k8sClient.List(ctx, cfgs, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
				return false
			}
			for i := range cfgs.Items {
				if cfgs.Items[i].Spec.Sequence > prevMaxSeq {
					return true
				}
			}
			return false
		}, 30*time.Second, 1*time.Second).Should(BeTrue(), "new config should be created after image update")
	}

	// waitForUpgradeComplete waits for the active config to be Applied and the cluster Ready.
	waitForUpgradeComplete := func(clusterName string, expectedNodes int, timeout ...time.Duration) []string {
		key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		t := framework.HealthTimeout
		if len(timeout) > 0 {
			t = timeout[0]
		}

		By("waiting for the active config to be Applied")
		_, err := framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, t)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the cluster to reach Ready")
		_, err = framework.WaitForClusterReady(ctx, k8sClient, key, t)
		Expect(err).NotTo(HaveOccurred())

		By(fmt.Sprintf("waiting for %d Redis pods to be ready", expectedNodes))
		err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
			framework.RedisPodLabels(clusterName), expectedNodes, t)
		Expect(err).NotTo(HaveOccurred())

		podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedNodes)
		Expect(err).NotTo(HaveOccurred())

		By("verifying cluster health after upgrade")
		Eventually(func() error {
			return framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedNodes)
		}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())

		return podNames
	}

	// verifyAllPodsRunImage asserts that all Redis pods run the expected image.
	verifyAllPodsRunImage := func(clusterName string, expectedImage string, expectedNodes int) {
		By(fmt.Sprintf("verifying all pods run image %s", expectedImage))
		Eventually(func() bool {
			pods := &corev1.PodList{}
			if err := k8sClient.List(ctx, pods, client.InNamespace(clusterNs),
				client.MatchingLabels{
					"redkey.inditex.dev/cluster":   clusterName,
					"redkey.inditex.dev/component": "redis",
				}); err != nil {
				return false
			}
			if len(pods.Items) != expectedNodes {
				return false
			}
			for _, pod := range pods.Items {
				found := false
				for _, c := range pod.Spec.Containers {
					if c.Name == "redis" {
						if !strings.Contains(c.Image, expectedImage) && c.Image != expectedImage {
							_, _ = fmt.Fprintf(GinkgoWriter,
								"Pod %s has image %s, expected %s\n", pod.Name, c.Image, expectedImage)
							return false
						}
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
			return true
		}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
			fmt.Sprintf("All %d pods should run image %s", expectedNodes, expectedImage))
	}

	// --- Fast Upgrade (ephemeral, no replicas, purgeKeysOnRebalance=true) ---

	Context("Fast upgrade (ephemeral, no replicas, purgeKeysOnRebalance=true)", func() {
		const clusterName = "upgrade-fast-ephemeral"

		It("creates a 3-primary cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0
			opts.PurgeKeysOnRebalance = ptr.To(true)

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForUpgradeComplete(clusterName, 3)

			By("verifying initial image")
			verifyAllPodsRunImage(clusterName, framework.GetRedisImage(), 3)

			By("inserting some keys (will be lost after fast upgrade)")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 20)).To(Succeed())
		})

		It("upgrades the Redis image via fast path (data loss expected)", func() {
			newImage := getRedisUpgradeImage()

			By(fmt.Sprintf("updating cluster image to %s", newImage))
			updateClusterImage(clusterName, newImage)

			By("waiting for upgrade to complete")
			waitForUpgradeComplete(clusterName, 3)

			By("verifying all pods run the new image")
			verifyAllPodsRunImage(clusterName, newImage, 3)

			By("verifying cluster is functional (insert new data)")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			Expect(framework.InsertKeys(clusterNs, podNames[0], 10)).To(Succeed())
		})
	})

	// --- Rolling N+1 Upgrade (purgeKeysOnRebalance=false) ---

	// rollingUpgradeContext defines a Context that verifies a rolling N+1 upgrade
	// preserves all data (zero data loss) for the given topology. When persistent is
	// true the cluster is backed by a PVC, which exercises the flush+persist (SAVE)
	// path on drained nodes during the rolling upgrade.
	rollingUpgradeContext := func(
		description, clusterName string, replicasPerPrimary int32, totalNodes, keyCount int, persistent bool,
	) {
		Context(description, func() {
			It("creates the cluster and inserts data", func() {
				opts := framework.DefaultClusterOptions(clusterName, clusterNs)
				if persistent {
					opts = opts.WithPVC("100Mi")
				}
				opts.Primaries = 3
				opts.ReplicasPerPrimary = replicasPerPrimary
				opts.PurgeKeysOnRebalance = ptr.To(false) // rolling upgrade

				_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
				Expect(err).NotTo(HaveOccurred())

				podNames := waitForUpgradeComplete(clusterName, totalNodes)

				By("inserting keys that must survive the upgrade")
				Expect(framework.InsertKeys(clusterNs, podNames[0], keyCount)).To(Succeed())

				By("recording key count before upgrade")
				dbSize, err := framework.GetDBSize(clusterNs, podNames)
				Expect(err).NotTo(HaveOccurred())
				Expect(dbSize).To(BeNumerically(">=", keyCount))
				_, _ = fmt.Fprintf(GinkgoWriter, "Keys before rolling upgrade (%s): %d\n", clusterName, dbSize)
			})

			It("upgrades the Redis image preserving all data", func() {
				newImage := getRedisUpgradeImage()

				By(fmt.Sprintf("updating cluster image to %s", newImage))
				updateClusterImage(clusterName, newImage)

				By("waiting for rolling upgrade to complete")
				podNames := waitForUpgradeComplete(clusterName, totalNodes, framework.UpgradeTimeout)

				By("verifying all pods run the new image")
				verifyAllPodsRunImage(clusterName, newImage, totalNodes)

				By("verifying data was preserved (zero data loss)")
				dbSize, err := framework.GetDBSize(clusterNs, podNames)
				Expect(err).NotTo(HaveOccurred())
				Expect(dbSize).To(BeNumerically(">=", keyCount),
					"Rolling N+1 upgrade must preserve all data")
				_, _ = fmt.Fprintf(GinkgoWriter, "Keys after rolling upgrade (%s): %d\n", clusterName, dbSize)
			})
		})
	}

	rollingUpgradeContext("Rolling N+1 upgrade (ephemeral, no replicas)",
		"upgrade-rolling-noreplica", 0, 3, 100, false)

	rollingUpgradeContext("Rolling N+1 upgrade (ephemeral, with replicas)",
		"upgrade-rolling-replicas", 1, 6, 50, false)

	// Multi-replica topology (3 primaries × 2 replicas = 9 nodes) exercises the
	// per-primary replica recycling loop with more than one replica per primary.
	rollingUpgradeContext("Rolling N+1 upgrade (ephemeral, 2 replicas per primary)",
		"upgrade-rolling-multireplica", 2, 9, 50, false)

	// Persistent topologies (PVC-backed) exercise the FLUSHALL+SAVE path on drained
	// nodes. These previously had 0% coverage in the upgrade suite.
	rollingUpgradeContext("Rolling N+1 upgrade (persistent, no replicas)",
		"upgrade-rolling-persistent-noreplica", 0, 3, 80, true)

	rollingUpgradeContext("Rolling N+1 upgrade (persistent, with replicas)",
		"upgrade-rolling-persistent-replicas", 1, 6, 40, true)
})
