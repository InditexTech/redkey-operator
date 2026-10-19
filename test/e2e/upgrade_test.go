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

	redkeyv1beta1 "github.com/inditextech/redkey-operator/api/v1beta1"
	"github.com/inditextech/redkey-operator/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// getRedisUpgradeImage returns the target image for upgrade tests.
// Use REDIS_IMAGE_UPGRADE env var to set a custom target, otherwise defaults to "redis:8-alpine"
// (a compatible but different image tag from the default "redis:8.10").
func getRedisUpgradeImage() string {
	if img := os.Getenv("REDIS_IMAGE_UPGRADE"); img != "" {
		return img
	}
	return "redis:8-alpine"
}

var _ = Describe("Redis Image Upgrade", Ordered, Label("upgrade"), func() {
	var (
		suiteCtx  context.Context
		ctx       context.Context
		ns        *corev1.Namespace
		clusterNs string
	)

	framework.SetupSpecContexts(&suiteCtx, &ctx, 30*time.Minute)

	BeforeAll(func() {
		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-upgrade")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)
		_, _ = fmt.Fprintf(GinkgoWriter, "Upgrade target image: %s\n", getRedisUpgradeImage())
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

	// updateClusterImage updates the cluster's Redis image with conflict-retry.
	// After the update, it waits for the operator to create a new config (higher sequence)
	// to avoid race conditions where waitForUpgradeComplete would pass on the old config.
	//nolint:dupl // shared utility closure, cannot extract from Describe block
	updateClusterImage := func(clusterName, newImage string) {
		key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

		// Get current highest sequence before update
		configs := &redkeyv1beta1.RedkeyConfigList{}
		Expect(k8sClient.List(ctx, configs, client.InNamespace(clusterNs),
			client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})).To(Succeed())
		prevMaxSeq := 0
		for i := range configs.Items {
			if configs.Items[i].Spec.Sequence > prevMaxSeq {
				prevMaxSeq = configs.Items[i].Spec.Sequence
			}
		}

		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			cluster := &redkeyv1beta1.Redkey{}
			if err := k8sClient.Get(ctx, key, cluster); err != nil {
				return err
			}
			cluster.Spec.Image = newImage
			return k8sClient.Update(ctx, cluster)
		})).To(Succeed())

		// Wait for the operator to create a new config with a higher sequence
		Eventually(func() bool {
			cfgs := &redkeyv1beta1.RedkeyConfigList{}
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
	//nolint:dupl // shared utility closure, cannot extract from Describe block
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

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a 3-primary cluster and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.Primaries = 3
			opts.ReplicasPerPrimary = 0
			opts.PurgeKeysOnRebalance = new(true)

			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
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
			AfterAll(func() {
				_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
			})

			It("creates the cluster and inserts data", func() {
				opts := framework.DefaultClusterOptions(clusterName, clusterNs)
				if persistent {
					opts = opts.WithPVC("100Mi")
				}
				opts.Primaries = 3
				opts.ReplicasPerPrimary = replicasPerPrimary
				opts.PurgeKeysOnRebalance = new(false) // rolling upgrade

				_, err := framework.CreateRedkey(ctx, k8sClient, opts)
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

	// --- Consecutive Rolling N+1 upgrades on persistent clusters with deletePVC=false ---
	//
	// With deletePVC=false (the CRD default) the StatefulSet retains the PVCs of the extra (N+1)
	// ordinals after each upgrade's scale-down. The next upgrade re-creates the extra pods on those
	// volumes; before the fix they resumed the stale identity persisted in nodes.conf (the extra
	// primary as a replica of node 0) and the second upgrade stalled forever. The default E2E
	// options use deletePVC=true, which deletes those PVCs and hides the problem.

	// extraPVCNames returns the data PVC names of the extra ordinals added by a Rolling N+1 upgrade.
	extraPVCNames := func(clusterName string, primaries, replicasPerPrimary int32) []string {
		first := primaries + primaries*replicasPerPrimary
		names := make([]string, 0, 1+replicasPerPrimary)
		for i := range 1 + replicasPerPrimary {
			names = append(names, fmt.Sprintf("data-%s-%d", clusterName, first+i))
		}
		return names
	}

	expectPVCsRetained := func(names []string) {
		By(fmt.Sprintf("verifying the extra ordinals' PVCs are retained: %v", names))
		for _, name := range names {
			pvc := &corev1.PersistentVolumeClaim{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: clusterNs}, pvc)).
				To(Succeed(), "PVC %s should be retained after the upgrade scale-down (deletePVC=false)", name)
		}
	}

	// rollingUpgradeTo upgrades the cluster image and verifies image, health and data preservation.
	rollingUpgradeTo := func(clusterName, image string, totalNodes, keyCount int) {
		By(fmt.Sprintf("updating cluster image to %s", image))
		updateClusterImage(clusterName, image)

		By("waiting for rolling upgrade to complete")
		podNames := waitForUpgradeComplete(clusterName, totalNodes, framework.UpgradeTimeout)

		verifyAllPodsRunImage(clusterName, image, totalNodes)

		By("verifying data was preserved (zero data loss)")
		dbSize, err := framework.GetDBSize(clusterNs, podNames)
		Expect(err).NotTo(HaveOccurred())
		Expect(dbSize).To(BeNumerically(">=", keyCount), "Rolling N+1 upgrade must preserve all data")
	}

	createPersistentKeepPVCCluster := func(clusterName string, replicasPerPrimary int32, totalNodes, keyCount int) {
		opts := framework.DefaultClusterOptions(clusterName, clusterNs).
			WithPVC("100Mi").WithDeletePVC(false).WithPersistence()
		opts.Primaries = 3
		opts.ReplicasPerPrimary = replicasPerPrimary

		_, err := framework.CreateRedkey(ctx, k8sClient, opts)
		Expect(err).NotTo(HaveOccurred())

		podNames := waitForUpgradeComplete(clusterName, totalNodes)

		By("inserting keys that must survive the upgrades")
		Expect(framework.InsertKeys(clusterNs, podNames[0], keyCount)).To(Succeed())
	}

	doubleRollingUpgradeContext := func(
		description, clusterName string, replicasPerPrimary int32, totalNodes, keyCount int,
	) {
		Context(description, func() {
			AfterAll(func() {
				_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
			})

			It("creates a persistent cluster with deletePVC=false and inserts data", func() {
				createPersistentKeepPVCCluster(clusterName, replicasPerPrimary, totalNodes, keyCount)
			})

			It("completes a first rolling upgrade and retains the extra ordinals' PVCs", func() {
				rollingUpgradeTo(clusterName, getRedisUpgradeImage(), totalNodes, keyCount)
				expectPVCsRetained(extraPVCNames(clusterName, 3, replicasPerPrimary))
			})

			It("completes a second rolling upgrade reusing the retained extra PVCs", func() {
				rollingUpgradeTo(clusterName, framework.GetRedisImage(), totalNodes, keyCount)
			})
		})
	}

	doubleRollingUpgradeContext("Two consecutive rolling N+1 upgrades (persistent, deletePVC=false, no replicas)",
		"upgrade-twice-keeppvc-noreplica", 0, 3, 60)

	doubleRollingUpgradeContext("Two consecutive rolling N+1 upgrades (persistent, deletePVC=false, with replicas)",
		"upgrade-twice-keeppvc-replicas", 1, 6, 40)

	// Scale-to-zero restores a persistent deletePVC=false cluster from its persisted nodes.conf and
	// data, while the retained extra PVC (not mounted by the restored topology) is reused by the
	// following upgrade. Both mechanisms rely on persisted state and must not interfere.
	Context("Rolling upgrade, scale 3→0→3 and rolling upgrade again (persistent, deletePVC=false)", func() {
		const (
			clusterName = "upgrade-zero-upgrade-keeppvc"
			totalNodes  = 3
			keyCount    = 50
		)
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a persistent cluster with deletePVC=false and inserts data", func() {
			createPersistentKeepPVCCluster(clusterName, 0, totalNodes, keyCount)
		})

		It("completes a first rolling upgrade", func() {
			rollingUpgradeTo(clusterName, getRedisUpgradeImage(), totalNodes, keyCount)
			expectPVCsRetained(extraPVCNames(clusterName, 3, 0))
		})

		It("scales to zero and back to 3 primaries restoring the persisted data", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 0
			})
			waitForScaledToZero(ctx, clusterName, clusterNs)

			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.Redkey) {
				c.Spec.Primaries = 3
			})
			podNames := waitForScaledCluster(ctx, clusterName, clusterNs, totalNodes)

			dbSize, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(dbSize).To(BeNumerically(">=", keyCount), "scale 0→3 must restore the persisted data")
		})

		It("completes a second rolling upgrade reusing the retained extra PVC", func() {
			rollingUpgradeTo(clusterName, framework.GetRedisImage(), totalNodes, keyCount)
		})
	})
})
