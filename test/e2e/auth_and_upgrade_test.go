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

var _ = Describe("Authentication and Upgrade", Ordered, Label("auth", "upgrade"), func() {
	var (
		ctx       context.Context
		cancel    context.CancelFunc
		ns        *corev1.Namespace
		clusterNs string
	)

	BeforeAll(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 90*time.Minute)

		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-auth-upgrade")
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

	getImageForUpgrade := func() string {
		if img := os.Getenv("REDIS_IMAGE_UPGRADE"); img != "" {
			return img
		}
		return "redis:8-alpine"
	}

	//nolint:dupl // shared utility closure, cannot extract from Describe block
	updateClusterImage := func(clusterName, newImage string) {
		key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

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

	updateClusterAuthAndImage := func(clusterName, secretName, newImage string) {
		key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

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
			cluster.Spec.Auth = redkeyv1beta1.RedisAuth{SecretName: secretName}
			return k8sClient.Update(ctx, cluster)
		})).To(Succeed())

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
		}, 30*time.Second, 1*time.Second).Should(BeTrue(), "new config should be created after combined update")
	}

	waitForUpgradeComplete := func(clusterName string, expectedNodes int, password ...string) []string {
		key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		var pass string
		if len(password) > 0 {
			pass = password[0]
		}

		// Rolling upgrades recycle pods one at a time and wait for cluster health
		// agreement between steps, so they need a longer budget than HealthTimeout.
		// Use the dedicated UpgradeTimeout (consistent with upgrade_test.go).
		t := framework.UpgradeTimeout

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
			return framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedNodes, pass)
		}, framework.HealthTimeout, framework.DefaultPollInterval).Should(Succeed())

		return podNames
	}

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

	// --- Scenario 1: Rolling upgrade on a cluster with auth from the start ---

	Context("Rolling upgrade with authentication from start", func() {
		const (
			clusterName = "auth-upgrade-rolling"
			secretName  = "auth-upgrade-rolling-secret"
			password    = "rolling-pass-123"
		)

		It("should create an authenticated cluster and insert data", func() {
			By("creating the auth secret")
			err := framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)
			Expect(err).NotTo(HaveOccurred())

			By("creating an authenticated cluster (rolling upgrade eligible)")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).
				WithAuth(secretName)
			opts.PurgeKeysOnRebalance = ptr.To(false)
			_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForUpgradeComplete(clusterName, 3, password)

			By("verifying auth is required on all nodes")
			for _, pod := range podNames {
				Expect(framework.CheckAuthRequired(clusterNs, pod)).To(BeTrue(),
					"Pod %s should require auth", pod)
			}

			By("inserting keys that survive the rolling upgrade")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 50, password)).To(Succeed())
			_, _ = fmt.Fprintf(GinkgoWriter, "Data inserted before rolling upgrade with auth\n")
		})

		It("should upgrade the Redis image while preserving auth and data", func() {
			newImage := getImageForUpgrade()

			By("triggering the image upgrade")
			updateClusterImage(clusterName, newImage)

			By("waiting for upgrade to complete")
			podNames := waitForUpgradeComplete(clusterName, 3, password)

			By("verifying all pods run the new image")
			verifyAllPodsRunImage(clusterName, newImage, 3)

			By("verifying auth is still required after upgrade")
			for _, pod := range podNames {
				Expect(framework.CheckAuthRequired(clusterNs, pod)).To(BeTrue(),
					"Pod %s should still require auth after upgrade", pod)
				Expect(framework.PingRedis(clusterNs, pod, password)).To(BeTrue(),
					"Pod %s should accept auth with original password", pod)
			}

			By("verifying data integrity through the upgrade")
			dbSize, err := framework.GetDBSize(clusterNs, podNames, password)
			Expect(err).NotTo(HaveOccurred())
			Expect(dbSize).To(BeNumerically(">=", 50),
				"Rolling upgrade with auth must preserve all data")
		})
	})

	// --- Scenario 2: Fast upgrade on a cluster with auth from the start ---

	Context("Fast upgrade with authentication from start", func() {
		const (
			clusterName = "auth-upgrade-fast"
			secretName  = "auth-upgrade-fast-secret"
			password    = "fast-pass-456"
		)

		It("should create an authenticated ephemeral cluster", func() {
			By("creating the auth secret")
			err := framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)
			Expect(err).NotTo(HaveOccurred())

			By("creating an authenticated cluster (fast upgrade eligible)")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).
				WithAuth(secretName)
			opts.PurgeKeysOnRebalance = ptr.To(true)

			_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForUpgradeComplete(clusterName, 3, password)

			By("verifying auth is required on all nodes")
			for _, pod := range podNames {
				Expect(framework.CheckAuthRequired(clusterNs, pod)).To(BeTrue(),
					"Pod %s should require auth", pod)
			}

			By("inserting keys (will be lost during fast upgrade)")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 20, password)).To(Succeed())
		})

		It("should perform fast upgrade while maintaining auth", func() {
			newImage := getImageForUpgrade()

			By("triggering the fast upgrade")
			updateClusterImage(clusterName, newImage)

			By("waiting for fast upgrade to complete")
			podNames := waitForUpgradeComplete(clusterName, 3, password)

			By("verifying all pods run the new image")
			verifyAllPodsRunImage(clusterName, newImage, 3)

			By("verifying auth is still required after fast upgrade")
			for _, pod := range podNames {
				Expect(framework.CheckAuthRequired(clusterNs, pod)).To(BeTrue(),
					"Pod %s should still require auth after fast upgrade", pod)
				Expect(framework.PingRedis(clusterNs, pod, password)).To(BeTrue(),
					"Pod %s should accept auth after fast upgrade", pod)
			}

			By("verifying cluster is functional after fast upgrade")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 10, password)).To(Succeed())
		})
	})

	// --- Scenario 3: Simultaneous auth + image change (rolling) ---

	Context("Simultaneous auth enable and image upgrade (rolling)", func() {
		const (
			clusterName = "auth-simultaneous-rolling"
			secretName  = "auth-simultaneous-rolling-secret"
			password    = "simultaneous-pass"
		)

		It("should create a cluster without auth and insert data", func() {
			By("creating the auth secret")
			err := framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)
			Expect(err).NotTo(HaveOccurred())

			By("creating a cluster without auth")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts.PurgeKeysOnRebalance = ptr.To(false)
			_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForUpgradeComplete(clusterName, 3)

			By("verifying auth is not required initially")
			Expect(framework.CheckAuthDisabled(clusterNs, podNames[0])).To(BeTrue())

			By("inserting keys that survive the upgrade")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 50)).To(Succeed())
		})

		It("should enable auth and upgrade image simultaneously", func() {
			newImage := getImageForUpgrade()

			By("changing auth and image in one update")
			updateClusterAuthAndImage(clusterName, secretName, newImage)

			By("waiting for the upgrade to complete")
			podNames := waitForUpgradeComplete(clusterName, 3, password)

			By("verifying all pods run the new image")
			verifyAllPodsRunImage(clusterName, newImage, 3)

			By("verifying auth is required after combined change")
			for _, pod := range podNames {
				Expect(framework.CheckAuthRequired(clusterNs, pod)).To(BeTrue(),
					"Pod %s should require auth after combined change", pod)
				Expect(framework.PingRedis(clusterNs, pod, password)).To(BeTrue(),
					"Pod %s should accept new auth password", pod)
			}

			By("verifying data integrity through the upgrade")
			dbSize, err := framework.GetDBSize(clusterNs, podNames, password)
			Expect(err).NotTo(HaveOccurred())
			Expect(dbSize).To(BeNumerically(">=", 50),
				"Simultaneous auth+image change must preserve data")
		})
	})

	// --- Scenario 4: Rolling upgrade on auth cluster with replicas ---

	Context("Rolling upgrade with auth and replicas", func() {
		const (
			clusterName = "auth-upgrade-replicas"
			secretName  = "auth-upgrade-replicas-secret"
			password    = "replicas-auth-pass"
		)

		It("should create an authenticated cluster with replicas and insert data", func() {
			By("creating the auth secret")
			err := framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)
			Expect(err).NotTo(HaveOccurred())

			By("creating an authenticated cluster with replicas")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).
				WithReplicas(1).
				WithAuth(secretName)
			_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			podNames := waitForUpgradeComplete(clusterName, 6, password)

			By("verifying auth is required on all nodes")
			for _, pod := range podNames {
				Expect(framework.CheckAuthRequired(clusterNs, pod)).To(BeTrue(),
					"Pod %s should require auth", pod)
			}

			By("verifying replication is intact")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0], password)
			Expect(err).NotTo(HaveOccurred())
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
			Expect(primaries).To(Equal(3))
			Expect(replicas).To(Equal(3))

			By("inserting keys that survive the rolling upgrade")
			Expect(framework.InsertKeys(clusterNs, podNames[0], 50, password)).To(Succeed())
		})

		It("should upgrade image preserving auth, replicas, and data", func() {
			newImage := getImageForUpgrade()

			By("triggering the image upgrade")
			updateClusterImage(clusterName, newImage)

			By("waiting for upgrade to complete")
			podNames := waitForUpgradeComplete(clusterName, 6, password)

			By("verifying all pods run the new image")
			verifyAllPodsRunImage(clusterName, newImage, 6)

			By("verifying auth is still required on all nodes")
			for _, pod := range podNames {
				Expect(framework.CheckAuthRequired(clusterNs, pod)).To(BeTrue(),
					"Pod %s should still require auth after upgrade", pod)
				Expect(framework.PingRedis(clusterNs, pod, password)).To(BeTrue(),
					"Pod %s should accept auth password after upgrade", pod)
			}

			By("verifying replication is maintained")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0], password)
			Expect(err).NotTo(HaveOccurred())
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
			Expect(primaries).To(Equal(3))
			Expect(replicas).To(Equal(3))

			By("verifying data integrity through the upgrade")
			dbSize, err := framework.GetDBSize(clusterNs, podNames, password)
			Expect(err).NotTo(HaveOccurred())
			Expect(dbSize).To(BeNumerically(">=", 50),
				"Rolling upgrade with auth and replicas must preserve all data")
		})
	})
})
