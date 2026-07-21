// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/test/e2e/framework"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Operator Resources", Ordered, Label("operator-resources"), func() {
	var (
		suiteCtx  context.Context
		ctx       context.Context
		ns        *corev1.Namespace
		clusterNs string
	)

	framework.SetupSpecContexts(&suiteCtx, &ctx, 20*time.Minute)

	BeforeAll(func() {
		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-resources")
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

	Context("Robin Deployment and RBAC", func() {
		const clusterName = "resources-robin"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should create Robin Deployment with correct labels and RBAC resources", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			saName := fmt.Sprintf("%s-robin", clusterName)

			By("verifying Robin Deployment exists with correct labels")
			deploys := &appsv1.DeploymentList{}
			err = k8sClient.List(ctx, deploys, client.InNamespace(clusterNs),
				client.MatchingLabels{
					"redkey.inditex.dev/cluster":   clusterName,
					"redkey.inditex.dev/component": "robin",
				})
			Expect(err).NotTo(HaveOccurred())
			Expect(deploys.Items).To(HaveLen(1), "Exactly one Robin Deployment should exist")

			deploy := deploys.Items[0]
			Expect(deploy.Labels["redkey.inditex.dev/component"]).To(Equal("robin"))
			Expect(deploy.Spec.Template.Labels["redkey.inditex.dev/component"]).To(Equal("robin"))
			Expect(*deploy.Spec.Replicas).To(Equal(int32(1)))

			By("verifying Robin Deployment has owner reference to RedkeyCluster")
			Expect(deploy.OwnerReferences).NotTo(BeEmpty())
			ownerFound := false
			for _, ref := range deploy.OwnerReferences {
				if ref.Kind == "RedkeyCluster" && ref.Name == clusterName {
					ownerFound = true
					break
				}
			}
			Expect(ownerFound).To(BeTrue(), "Robin Deployment should have OwnerReference to RedkeyCluster")

			By("verifying Robin container has correct environment variables")
			containers := deploy.Spec.Template.Spec.Containers
			Expect(containers).NotTo(BeEmpty())
			robinContainer := containers[0]
			envMap := make(map[string]string)
			for _, env := range robinContainer.Env {
				if env.Value != "" {
					envMap[env.Name] = env.Value
				}
			}
			// Check for cluster name and namespace env vars
			hasClusterEnv := false
			hasNamespaceEnv := false
			for _, env := range robinContainer.Env {
				if env.Name == "CLUSTER_NAME" {
					hasClusterEnv = true
				}
				if env.Name == "NAMESPACE" {
					hasNamespaceEnv = true
				}
			}
			Expect(hasClusterEnv || contains(robinContainer.Args, clusterName)).To(BeTrue(),
				"Robin should know its cluster name via env or args")
			_ = hasNamespaceEnv

			By("verifying ServiceAccount exists with owner reference")
			sa := &corev1.ServiceAccount{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: saName, Namespace: clusterNs}, sa)
			Expect(err).NotTo(HaveOccurred())
			Expect(sa.OwnerReferences).NotTo(BeEmpty())

			By("verifying Role exists with correct RBAC rules")
			role := &rbacv1.Role{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: saName, Namespace: clusterNs}, role)
			Expect(err).NotTo(HaveOccurred())
			Expect(role.Rules).NotTo(BeEmpty())

			// Check that the role includes essential permissions
			hasStatefulSetRule := false
			hasConfigRule := false
			hasSecretRule := false
			for _, rule := range role.Rules {
				for _, resource := range rule.Resources {
					if resource == "statefulsets" {
						hasStatefulSetRule = true
					}
					if resource == "redkeyclusterconfigs" || resource == "redkeyclusterconfigs/status" {
						hasConfigRule = true
					}
					if resource == "secrets" {
						hasSecretRule = true
					}
				}
			}
			Expect(hasStatefulSetRule).To(BeTrue(), "Role should grant statefulsets access")
			Expect(hasConfigRule).To(BeTrue(), "Role should grant redkeyclusterconfigs access")
			Expect(hasSecretRule).To(BeTrue(), "Role should grant secrets access")

			By("verifying RoleBinding exists and links SA to Role")
			rb := &rbacv1.RoleBinding{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: saName, Namespace: clusterNs}, rb)
			Expect(err).NotTo(HaveOccurred())
			Expect(rb.RoleRef.Name).To(Equal(saName))
			Expect(rb.RoleRef.Kind).To(Equal("Role"))
			Expect(rb.Subjects).To(HaveLen(1))
			Expect(rb.Subjects[0].Name).To(Equal(saName))
			Expect(rb.Subjects[0].Kind).To(Equal("ServiceAccount"))
		})
	})

	Context("Config generation on spec change", func() {
		const clusterName = "resources-config-gen"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should create a new RedkeyClusterConfig when the cluster spec changes", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("counting initial configs")
			configsBefore, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
			Expect(err).NotTo(HaveOccurred())
			countBefore := len(configsBefore)
			Expect(countBefore).To(BeNumerically(">=", 1))

			maxSeqBefore := configsBefore[len(configsBefore)-1].Spec.Sequence
			_, _ = fmt.Fprintf(GinkgoWriter, "Initial configs: %d, max sequence: %d\n", countBefore, maxSeqBefore)

			By("updating the cluster spec (change redis config)")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			cluster := &redkeyv1beta1.RedkeyCluster{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			cluster.Spec.Config = cluster.Spec.Config + "\ntcp-keepalive 60"
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for a new config to be created with higher sequence")
			Eventually(func() bool {
				configs, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
				if err != nil || len(configs) == 0 {
					return false
				}
				maxSeq := configs[len(configs)-1].Spec.Sequence
				return maxSeq > maxSeqBefore
			}, 2*time.Minute, 5*time.Second).Should(BeTrue(),
				"A new RedkeyClusterConfig with higher sequence should be created")

			By("waiting for the new config to reach Applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the cluster is healthy after config change")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Config cleanup", func() {
		const clusterName = "resources-config-cleanup"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should clean up old superseded configs keeping only the last Applied and newer", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("applying multiple spec changes to generate configs")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

			for i := range 3 {
				err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
					cluster := &redkeyv1beta1.RedkeyCluster{}
					if getErr := k8sClient.Get(ctx, key, cluster); getErr != nil {
						return getErr
					}
					cluster.Spec.Config = fmt.Sprintf("%s\ntcp-keepalive %d", cluster.Spec.Config, 60+i*10)
					return k8sClient.Update(ctx, cluster)
				})
				Expect(err).NotTo(HaveOccurred())

				// Wait for the config to be created and applied before next change
				_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
				Expect(err).NotTo(HaveOccurred())
			}

			By("verifying old configs have been cleaned up by the operator")
			Eventually(func() bool {
				configs, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
				if err != nil {
					return false
				}
				// The operator should keep only the last Applied + any newer
				// After 3 changes all reaching Applied, there should be at most 2 configs
				// (the last Applied one and possibly one being cleaned)
				_, _ = fmt.Fprintf(GinkgoWriter, "Current config count: %d\n", len(configs))
				for _, cfg := range configs {
					_, _ = fmt.Fprintf(GinkgoWriter, "  Config %s: seq=%d, phase=%s\n",
						cfg.Name, cfg.Spec.Sequence, cfg.Status.ConfigPhase)
				}
				return len(configs) <= 2
			}, 3*time.Minute, 10*time.Second).Should(BeTrue(),
				"Operator should clean up old configs, keeping at most the last Applied and newer")
		})
	})

	Context("Multi-config queue processing", func() {
		const clusterName = "resources-multiconfig"

		AfterAll(func() {
			_ = framework.DeleteRedkeyCluster(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should create configs with monotonically increasing sequences for rapid spec changes", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("recording initial config state")
			initialConfigs, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
			Expect(err).NotTo(HaveOccurred())
			initialMaxSeq := initialConfigs[len(initialConfigs)-1].Spec.Sequence

			By("applying 3 rapid spec changes without waiting between them")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			for i := range 3 {
				cluster := &redkeyv1beta1.RedkeyCluster{}
				err = k8sClient.Get(ctx, key, cluster)
				Expect(err).NotTo(HaveOccurred())
				cluster.Spec.Config = fmt.Sprintf("%s\nslowlog-log-slower-than %d", cluster.Spec.Config, (i+1)*10000)
				err = k8sClient.Update(ctx, cluster)
				Expect(err).NotTo(HaveOccurred())
			}

			By("verifying new configs were created with increasing sequences")
			Eventually(func() bool {
				configs, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
				if err != nil {
					return false
				}
				// There should be at least one new config beyond the initial
				newConfigs := 0
				for _, cfg := range configs {
					if cfg.Spec.Sequence > initialMaxSeq {
						newConfigs++
					}
				}
				return newConfigs >= 1
			}, 2*time.Minute, 5*time.Second).Should(BeTrue(),
				"At least one new config should be created for the spec changes")

			By("verifying sequences are monotonically increasing")
			configs, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
			Expect(err).NotTo(HaveOccurred())
			for i := 1; i < len(configs); i++ {
				Expect(configs[i].Spec.Sequence).To(BeNumerically(">", configs[i-1].Spec.Sequence),
					"Config sequences should be monotonically increasing")
			}

			By("waiting for the final config to reach Applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying cluster is healthy after processing all configs")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

// contains checks if a string slice contains a given string.
func contains(slice []string, s string) bool {
	return slices.Contains(slice, s)
}
