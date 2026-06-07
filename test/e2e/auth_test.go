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
)

var _ = Describe("Authentication", Ordered, Label("auth"), func() {
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
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-auth")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)
	})

	resolveDataPassword := func(namespace, podName, configuredPassword string) string {
		if framework.PingRedis(namespace, podName) {
			_, _ = fmt.Fprintf(GinkgoWriter,
				"Redis pod %s accepts unauthenticated connections; using unauthenticated data commands\n", podName)
			return ""
		}
		return configuredPassword
	}

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

	Context("Cluster with authentication from the start", func() {
		const (
			clusterName = "auth-initial"
			secretName  = "auth-initial-secret"
			password    = "test-password-123"
		)

		It("should create a cluster with auth and propagate the auth secret to active config", func() {
			By("creating the auth secret")
			err := framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)
			Expect(err).NotTo(HaveOccurred())

			By("creating the RedkeyCluster with auth")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithAuth(secretName)
			_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

			By("verifying Redis responds to authenticated requests")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())

			for _, pod := range podNames {
				Expect(framework.PingRedis(clusterNs, pod, password)).To(BeTrue(),
					"Pod %s should respond to authenticated PING", pod)
				Expect(framework.CheckAuthRequired(clusterNs, pod)).To(BeTrue(),
					"Pod %s should require authentication for unauthenticated PING", pod)
			}

			By("verifying auth secret is propagated to the active config")
			activeCfg, err := framework.WaitForActiveConfigApplied(
				ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(activeCfg.Spec.Auth.SecretName).To(Equal(secretName))

			dataPassword := resolveDataPassword(clusterNs, podNames[0], password)

			By("verifying cluster is healthy with auth")
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3, dataPassword)
			Expect(err).NotTo(HaveOccurred())

			By("verifying data operations work with auth")
			err = framework.InsertKeys(clusterNs, podNames[0], 10, dataPassword)
			Expect(err).NotTo(HaveOccurred())

			total, err := framework.GetDBSize(clusterNs, podNames, dataPassword)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(10))
		})
	})

	Context("No-auth to auth transition", func() {
		const (
			clusterName = "auth-transition-on"
			secretName  = "auth-transition-on-secret"
			password    = "new-password-456"
		)

		It("should add authentication to a running cluster without auth", func() {
			By("creating a cluster without authentication")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data before auth change")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			insertPassword := resolveDataPassword(clusterNs, podNames[0], "")
			err = framework.InsertKeys(clusterNs, podNames[0], 10, insertPassword)
			Expect(err).NotTo(HaveOccurred())

			By("creating the auth secret")
			err = framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)
			Expect(err).NotTo(HaveOccurred())

			By("updating the cluster to enable authentication")
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, cluster)
			Expect(err).NotTo(HaveOccurred())

			cluster.Spec.Auth = redkeyv1beta1.RedisAuth{SecretName: secretName}
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready again")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying authenticated access works")
			Eventually(func() bool {
				return framework.PingRedis(clusterNs, podNames[0], password)
			}, 2*time.Minute, 5*time.Second).Should(BeTrue())

			By("verifying unauthenticated access is rejected after enabling auth")
			Eventually(func() bool {
				return framework.CheckAuthRequired(clusterNs, podNames[0])
			}, 1*time.Minute, 5*time.Second).Should(BeTrue(),
				"Unauthenticated PING should fail after auth is enabled")

			By("verifying data integrity after auth change")
			readPassword := resolveDataPassword(clusterNs, podNames[0], password)
			total, err := framework.GetDBSize(clusterNs, podNames, readPassword)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(10))
		})
	})

	Context("Auth to no-auth transition", func() {
		const (
			clusterName = "auth-transition-off"
			secretName  = "auth-transition-off-secret"
			password    = "remove-me-789"
		)

		It("should remove authentication from a running cluster with auth", func() {
			By("creating the auth secret")
			err := framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)
			Expect(err).NotTo(HaveOccurred())

			By("creating a cluster with authentication")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithAuth(secretName)
			_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data before auth removal")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			insertPassword := resolveDataPassword(clusterNs, podNames[0], password)
			err = framework.InsertKeys(clusterNs, podNames[0], 10, insertPassword)
			Expect(err).NotTo(HaveOccurred())

			By("updating the cluster to remove authentication")
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, cluster)
			Expect(err).NotTo(HaveOccurred())

			cluster.Spec.Auth = redkeyv1beta1.RedisAuth{}
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready again")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying unauthenticated access works")
			Eventually(func() bool {
				return framework.PingRedis(clusterNs, podNames[0])
			}, 2*time.Minute, 5*time.Second).Should(BeTrue())

			By("verifying data integrity after auth removal")
			total, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(10))
		})
	})

	Context("Password rotation", func() {
		const (
			clusterName = "auth-rotation"
			secretName  = "auth-rotation-secret"
			oldPassword = "old-password-aaa"
			newPassword = "new-password-bbb"
		)

		It("should detect password change in Secret and update Redis nodes", func() {
			By("creating the auth secret with old password")
			err := framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, oldPassword)
			Expect(err).NotTo(HaveOccurred())

			By("creating a cluster with authentication")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithAuth(secretName)
			_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready phase")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data with old password")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			insertPassword := resolveDataPassword(clusterNs, podNames[0], oldPassword)
			err = framework.InsertKeys(clusterNs, podNames[0], 10, insertPassword)
			Expect(err).NotTo(HaveOccurred())

			By("updating the secret with the new password")
			err = framework.UpdateAuthSecret(ctx, k8sClient, clusterNs, secretName, newPassword)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Robin to detect and apply the new password")
			Eventually(func() bool {
				return framework.PingRedis(clusterNs, podNames[0], newPassword)
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
				"Redis should accept the new password after rotation")

			By("verifying unauthenticated access still fails after password rotation")
			Expect(framework.CheckAuthRequired(clusterNs, podNames[0])).To(BeTrue(),
				"Redis should still require authentication after password rotation")

			By("verifying data integrity after password rotation")
			readPassword := resolveDataPassword(clusterNs, podNames[0], newPassword)
			total, err := framework.GetDBSize(clusterNs, podNames, readPassword)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(10))

			By("verifying cluster is healthy with new password")
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3, readPassword)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	// --- Auth with Replicas ---

	Context("Add auth to cluster with replicas", func() {
		const (
			clusterName = "auth-replicas-add"
			secretName  = "auth-replicas-add-secret"
			password    = "replicas-password-123"
		)

		It("should add authentication to a replica cluster applying requirepass and masterauth", func() {
			By("creating a cluster with replicas but no auth")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).WithReplicas(1)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			expectedPods := 6 // 3 primaries + 3 replicas
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data before auth change")
			insertPassword := resolveDataPassword(clusterNs, podNames[0], "")
			err = framework.InsertKeys(clusterNs, podNames[0], 10, insertPassword)
			Expect(err).NotTo(HaveOccurred())

			By("creating the auth secret")
			err = framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)
			Expect(err).NotTo(HaveOccurred())

			By("updating the cluster to enable authentication")
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Auth = redkeyv1beta1.RedisAuth{SecretName: secretName}
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying authenticated access works on all nodes")
			Eventually(func() bool {
				for _, pod := range podNames {
					if !framework.PingRedis(clusterNs, pod, password) {
						return false
					}
				}
				return true
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
				"All nodes should accept authenticated PING")

			By("verifying unauthenticated access is rejected on all nodes")
			Eventually(func() bool {
				for _, pod := range podNames {
					if !framework.CheckAuthRequired(clusterNs, pod) {
						return false
					}
				}
				return true
			}, 1*time.Minute, 5*time.Second).Should(BeTrue(),
				"All nodes should reject unauthenticated PING after auth is enabled")

			By("verifying replication is working (replicas connected to primaries)")
			readPassword := resolveDataPassword(clusterNs, podNames[0], password)
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0], readPassword)
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

			By("verifying data integrity")
			numPrimaries := 3
			total, err := framework.GetDBSize(clusterNs, podNames[:numPrimaries], readPassword)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(10))
		})
	})

	Context("Remove auth from cluster with replicas", func() {
		const (
			clusterName = "auth-replicas-remove"
			secretName  = "auth-replicas-remove-secret"
			password    = "replicas-remove-pass"
		)

		It("should remove authentication from a replica cluster and maintain replication", func() {
			By("creating the auth secret")
			err := framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, password)
			Expect(err).NotTo(HaveOccurred())

			By("creating a cluster with replicas and auth")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).
				WithReplicas(1).
				WithAuth(secretName)
			_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			expectedPods := 6
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data")
			insertPassword := resolveDataPassword(clusterNs, podNames[0], password)
			err = framework.InsertKeys(clusterNs, podNames[0], 10, insertPassword)
			Expect(err).NotTo(HaveOccurred())

			By("removing authentication")
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Auth = redkeyv1beta1.RedisAuth{}
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying unauthenticated access works")
			Eventually(func() bool {
				return framework.PingRedis(clusterNs, podNames[0])
			}, 2*time.Minute, 5*time.Second).Should(BeTrue())

			By("verifying replication is maintained")
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0])
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

			By("verifying data integrity")
			numPrimaries := 3
			total, err := framework.GetDBSize(clusterNs, podNames[:numPrimaries])
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(10))
		})
	})

	Context("Password rotation in cluster with replicas", func() {
		const (
			clusterName = "auth-replicas-rotation"
			secretName  = "auth-replicas-rotation-secret"
			oldPassword = "old-replicas-pass"
			newPassword = "new-replicas-pass"
		)

		It("should rotate password in a replica cluster maintaining replication", func() {
			By("creating the auth secret")
			err := framework.CreateAuthSecret(ctx, k8sClient, clusterNs, secretName, oldPassword)
			Expect(err).NotTo(HaveOccurred())

			By("creating a cluster with replicas and auth")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs).
				WithReplicas(1).
				WithAuth(secretName)
			_, err = framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			expectedPods := 6
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data")
			insertPassword := resolveDataPassword(clusterNs, podNames[0], oldPassword)
			err = framework.InsertKeys(clusterNs, podNames[0], 10, insertPassword)
			Expect(err).NotTo(HaveOccurred())

			By("updating the secret with the new password")
			err = framework.UpdateAuthSecret(ctx, k8sClient, clusterNs, secretName, newPassword)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Robin to detect and apply the new password")
			Eventually(func() bool {
				return framework.PingRedis(clusterNs, podNames[0], newPassword)
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
				"Redis should accept the new password")

			By("verifying all nodes accept the new password")
			for _, pod := range podNames {
				Expect(framework.PingRedis(clusterNs, pod, newPassword)).To(BeTrue(),
					"Pod %s should accept the new password", pod)
			}

			By("verifying replication is maintained after password rotation")
			readPassword := resolveDataPassword(clusterNs, podNames[0], newPassword)
			nodes, err := framework.GetClusterNodes(clusterNs, podNames[0], readPassword)
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

			By("verifying data integrity")
			numPrimaries := 3
			total, err := framework.GetDBSize(clusterNs, podNames[:numPrimaries], readPassword)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(10))
		})
	})
})
