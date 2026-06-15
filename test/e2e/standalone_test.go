// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/test/e2e/framework"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// verifyStandaloneNode asserts that the single Redis node responds to PING and has
// Redis cluster mode disabled (cluster-enabled no).
func verifyStandaloneNode(ctx context.Context, clusterName, clusterNs string) {
	podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 1)
	Expect(err).NotTo(HaveOccurred())
	Expect(podNames).To(HaveLen(1))

	By("verifying the node responds to PING")
	stdout, _, err := framework.ExecInPod(clusterNs, podNames[0], framework.RedisCliPing)
	Expect(err).NotTo(HaveOccurred())
	Expect(strings.TrimSpace(stdout)).To(Equal("PONG"))

	By("verifying Redis cluster mode is disabled")
	stdout, _, err = framework.ExecInPod(clusterNs, podNames[0], "redis-cli CONFIG GET cluster-enabled")
	Expect(err).NotTo(HaveOccurred())
	Expect(strings.ToLower(stdout)).To(ContainSubstring("no"))
}

var _ = Describe("Standalone", Ordered, Label("standalone"), func() {
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
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-standalone")
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

	Context("Ephemeral standalone", func() {
		const clusterName = "standalone-ephemeral"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a single-node standalone instance and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithMode(redkeyv1beta1.ModeStandalone)
			opts.Primaries = 1
			opts.ReplicasPerPrimary = 0

			By("creating the standalone RedkeyCluster")
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

			By("verifying exactly one Redis pod is running")
			err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName), 1, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying no PVCs were created (ephemeral)")
			pvcs := &corev1.PersistentVolumeClaimList{}
			Expect(k8sClient.List(ctx, pvcs, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})).To(Succeed())
			Expect(pvcs.Items).To(BeEmpty())

			By("verifying no PodDisruptionBudget was created")
			pdbList := &policyv1.PodDisruptionBudgetList{}
			Expect(k8sClient.List(ctx, pdbList, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})).To(Succeed())
			Expect(pdbList.Items).To(BeEmpty())

			verifyStandaloneNode(ctx, clusterName, clusterNs)

			By("verifying the active config reached Applied/Ready")
			config, err := framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(config.Status.ConfigPhase).To(Equal(redkeyv1beta1.ConfigPhaseApplied))
			Expect(config.Status.Status).To(Equal(redkeyv1beta1.ClusterStatusReady))
		})
	})

	Context("Persistent standalone", func() {
		const clusterName = "standalone-storage"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a single-node standalone instance with a PVC and reaches Ready", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).
				WithMode(redkeyv1beta1.ModeStandalone).
				WithPVC("100Mi")
			opts.Primaries = 1
			opts.ReplicasPerPrimary = 0

			By("creating the standalone RedkeyCluster with storage")
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

			By("verifying exactly one Redis pod is running")
			err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName), 1, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying exactly one PVC was created")
			Eventually(func(g Gomega) {
				pvcs := &corev1.PersistentVolumeClaimList{}
				g.Expect(k8sClient.List(ctx, pvcs, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})).To(Succeed())
				g.Expect(pvcs.Items).To(HaveLen(1))
			}, framework.CreationTimeout, framework.DefaultPollInterval).Should(Succeed())

			verifyStandaloneNode(ctx, clusterName, clusterNs)
		})
	})

	Context("Scale to zero and back", func() {
		const clusterName = "standalone-scale"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a single-node standalone instance", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithMode(redkeyv1beta1.ModeStandalone)
			opts.Primaries = 1
			opts.ReplicasPerPrimary = 0

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key(), framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName), 1, framework.CreationTimeout)).To(Succeed())
		})

		It("scales the standalone instance down to zero", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 0
			})

			waitForScaledToZero(ctx, clusterName, clusterNs)
		})

		It("scales the standalone instance back up to one", func() {
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Primaries = 1
			})

			_, err := framework.WaitForClusterReady(ctx, k8sClient, key(), framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName), 1, framework.CreationTimeout)).To(Succeed())

			verifyStandaloneNode(ctx, clusterName, clusterNs)
		})
	})

	Context("Configuration upgrade", func() {
		const clusterName = "standalone-upgrade"
		key := func() types.NamespacedName {
			return types.NamespacedName{Name: clusterName, Namespace: clusterNs}
		}

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates a single-node standalone instance", func() {
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithMode(redkeyv1beta1.ModeStandalone)
			opts.Primaries = 1
			opts.ReplicasPerPrimary = 0

			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key(), framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName), 1, framework.CreationTimeout)).To(Succeed())
		})

		It("applies a Redis configuration change by recycling the pod", func() {
			By("changing the Redis configuration")
			updateClusterTopology(ctx, key(), func(c *redkeyv1beta1.RedkeyCluster) {
				c.Spec.Config = "maxmemory 64mb"
			})

			By("waiting for the cluster to return to Ready phase")
			_, err := framework.WaitForClusterReady(ctx, k8sClient, key(), framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName), 1, framework.CreationTimeout)).To(Succeed())

			By("verifying the active config reached Applied/Ready")
			config, err := framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs,
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(config.Status.ConfigPhase).To(Equal(redkeyv1beta1.ConfigPhaseApplied))

			By("verifying the new configuration is applied on the running node")
			Eventually(func(g Gomega) {
				podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 1)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(podNames).To(HaveLen(1))
				stdout, _, err := framework.ExecInPod(clusterNs, podNames[0],
					"redis-cli CONFIG GET maxmemory")
				g.Expect(err).NotTo(HaveOccurred())
				// 64mb == 67108864 bytes.
				g.Expect(stdout).To(ContainSubstring("67108864"))
			}, framework.CreationTimeout, framework.DefaultPollInterval).Should(Succeed())

			verifyStandaloneNode(ctx, clusterName, clusterNs)
		})
	})

	Context("Authenticated standalone", func() {
		const (
			clusterName = "standalone-auth"
			secretName  = "standalone-auth-secret"
			password    = "standalone-pass-123"
		)

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("creates an authenticated single-node standalone instance and reaches Ready", func() {
			By("creating the auth secret")
			Expect(framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)).To(Succeed())

			By("creating the standalone RedkeyCluster with auth")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).
				WithMode(redkeyv1beta1.ModeStandalone).
				WithAuth(secretName)
			opts.Primaries = 1
			opts.ReplicasPerPrimary = 0
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

			By("verifying exactly one Redis pod is running")
			Expect(framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
				framework.RedisPodLabels(clusterName), 1, framework.CreationTimeout)).To(Succeed())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(podNames).To(HaveLen(1))

			By("verifying the node responds to an authenticated PING")
			Expect(framework.PingRedis(clusterNs, podNames[0], password)).To(BeTrue(),
				"Pod %s should respond to authenticated PING", podNames[0])

			By("verifying the node rejects unauthenticated PING")
			Expect(framework.CheckAuthRequired(clusterNs, podNames[0])).To(BeTrue(),
				"Pod %s should require authentication", podNames[0])

			By("verifying Redis cluster mode is disabled")
			stdout, _, err := framework.ExecInPod(clusterNs, podNames[0],
				fmt.Sprintf("redis-cli -a %s CONFIG GET cluster-enabled", password))
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.ToLower(stdout)).To(ContainSubstring("no"))

			By("verifying the auth secret is propagated to the active config")
			activeCfg, err := framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs,
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(activeCfg.Spec.Auth.SecretName).To(Equal(secretName))
		})
	})

	Context("CEL validation", func() {
		standaloneSpec := func(name string, primaries, replicas int32) *redkeyv1beta1.RedkeyCluster {
			return &redkeyv1beta1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: clusterNs},
				Spec: redkeyv1beta1.RedkeyClusterSpec{
					Mode:               redkeyv1beta1.ModeStandalone,
					Primaries:          primaries,
					ReplicasPerPrimary: replicas,
					Ephemeral:          true,
					Image:              framework.GetRedisImage(),
					Robin:              redkeyv1beta1.RobinSpec{Image: framework.GetRobinImage()},
					DeletePVC:          new(false),
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			}
		}

		It("rejects a standalone cluster with more than one primary", func() {
			err := k8sClient.Create(ctx, standaloneSpec("standalone-bad-primaries", 2, 0))
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err) || errors.IsForbidden(err)).To(BeTrue(),
				"Error should be validation-related, got: %v", err)
		})

		It("rejects a standalone cluster with replicas", func() {
			err := k8sClient.Create(ctx, standaloneSpec("standalone-bad-replicas", 1, 1))
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err) || errors.IsForbidden(err)).To(BeTrue(),
				"Error should be validation-related, got: %v", err)
		})

		It("rejects changing the mode field after creation", func() {
			cluster := standaloneSpec("standalone-immutable-mode", 1, 0)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			DeferCleanup(func() {
				_ = framework.DeleteRedkeyCluster(ctx, k8sClient, "standalone-immutable-mode", clusterNs)
			})

			fetched := &redkeyv1beta1.RedkeyCluster{}
			key := types.NamespacedName{Name: "standalone-immutable-mode", Namespace: clusterNs}
			Expect(k8sClient.Get(ctx, key, fetched)).To(Succeed())
			fetched.Spec.Mode = redkeyv1beta1.ModeCluster
			err := k8sClient.Update(ctx, fetched)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err) || errors.IsForbidden(err)).To(BeTrue(),
				"Error should be validation-related, got: %v", err)
		})
	})
})
