// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/test/e2e/framework"
	"github.com/inditextech/redkeyoperator/test/utils"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Resilience", Ordered, Label("resilience"), func() {
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
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-resilience")
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

	Context("Robin pod restart", func() {
		const clusterName = "resilience-robin-restart"

		It("should resume reconciliation after Robin pod is deleted and recreated", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data before Robin restart")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.InsertKeys(clusterNs, podNames[0], 10)
			Expect(err).NotTo(HaveOccurred())

			By("finding and deleting the Robin pod")
			robinPods := &corev1.PodList{}
			err = k8sClient.List(ctx, robinPods, client.InNamespace(clusterNs),
				client.MatchingLabels{
					"redkey.inditex.dev/cluster":   clusterName,
					"redkey.inditex.dev/component": "robin",
				})
			Expect(err).NotTo(HaveOccurred())
			Expect(robinPods.Items).NotTo(BeEmpty(), "Robin pod should exist")

			robinPodName := robinPods.Items[0].Name
			_, _ = fmt.Fprintf(GinkgoWriter, "Deleting Robin pod: %s\n", robinPodName)
			cmd := exec.Command("kubectl", "delete", "pod", robinPodName, "-n", clusterNs, "--grace-period=0", "--force")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the Robin Deployment to recreate the pod")
			Eventually(func() bool {
				pods := &corev1.PodList{}
				if err := k8sClient.List(ctx, pods, client.InNamespace(clusterNs),
					client.MatchingLabels{
						"redkey.inditex.dev/cluster":   clusterName,
						"redkey.inditex.dev/component": "robin",
					}); err != nil {
					return false
				}
				for _, pod := range pods.Items {
					if pod.Name != robinPodName && pod.Status.Phase == corev1.PodRunning {
						for _, cond := range pod.Status.Conditions {
							if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
								_, _ = fmt.Fprintf(GinkgoWriter, "New Robin pod running: %s\n", pod.Name)
								return true
							}
						}
					}
				}
				return false
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(), "Robin pod should be recreated by Deployment")

			By("verifying the cluster remains healthy after Robin restart")
			Eventually(func() error {
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			}, framework.DefaultTimeout, 10*time.Second).Should(Succeed(),
				"Cluster should remain healthy after Robin restart")

			By("verifying cluster status is still Ready")
			cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

			By("verifying data integrity after Robin restart")
			total, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(10))
		})
	})

	Context("Operator pod restart", func() {
		const clusterName = "resilience-operator-restart"

		It("should resume management without duplicate configs after operator restart", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("counting existing configs before operator restart")
			configsBefore, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
			Expect(err).NotTo(HaveOccurred())
			configCountBefore := len(configsBefore)
			_, _ = fmt.Fprintf(GinkgoWriter, "Configs before restart: %d\n", configCountBefore)

			By("deleting the operator pod to trigger restart")
			cmd := exec.Command("kubectl", "delete", "pod", "-l", "control-plane=controller-manager",
				"-n", "redkey-operator", "--grace-period=0", "--force")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the operator to be ready again")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "-l", "control-plane=controller-manager",
					"-n", "redkey-operator", "-o", "jsonpath={.items[0].status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed(), "Operator pod should restart")

			By("waiting for the cluster to remain Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying no duplicate configs were created")
			configsAfter, err := framework.ListConfigs(ctx, k8sClient, clusterName, clusterNs)
			Expect(err).NotTo(HaveOccurred())
			Expect(configsAfter).To(HaveLen(configCountBefore),
				"Operator restart should not create duplicate configs")

			By("verifying the cluster remains healthy")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Robin deployment recreation", func() {
		const clusterName = "resilience-robin-deploy"

		It("should recreate the Robin Deployment if manually deleted", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkeyCluster(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("deleting the Robin Deployment")
			deploys := &appsv1.DeploymentList{}
			err = k8sClient.List(ctx, deploys, client.InNamespace(clusterNs),
				client.MatchingLabels{
					"redkey.inditex.dev/cluster":   clusterName,
					"redkey.inditex.dev/component": "robin",
				})
			Expect(err).NotTo(HaveOccurred())
			Expect(deploys.Items).NotTo(BeEmpty())

			err = k8sClient.Delete(ctx, &deploys.Items[0])
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the operator to recreate the Robin Deployment")
			Eventually(func() bool {
				list := &appsv1.DeploymentList{}
				if err := k8sClient.List(ctx, list, client.InNamespace(clusterNs),
					client.MatchingLabels{
						"redkey.inditex.dev/cluster":   clusterName,
						"redkey.inditex.dev/component": "robin",
					}); err != nil {
					return false
				}
				if len(list.Items) == 0 {
					return false
				}
				return list.Items[0].Status.ReadyReplicas > 0
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
				"Operator should recreate the deleted Robin Deployment")

			By("verifying the cluster remains healthy")
			Eventually(func() error {
				podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
				if err != nil {
					return err
				}
				return framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			}, framework.DefaultTimeout, 10*time.Second).Should(Succeed())
		})
	})
})
