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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Cluster Lifecycle", Ordered, Label("lifecycle"), func() {
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
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-lifecycle")
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

	Context("Cascading deletion", func() {
		const clusterName = "lifecycle-delete"

		It("should delete all child resources when the Redkey is deleted", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying child resources exist before deletion")
			// StatefulSet
			stsList := &appsv1.StatefulSetList{}
			err = k8sClient.List(ctx, stsList, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
			Expect(err).NotTo(HaveOccurred())
			Expect(stsList.Items).NotTo(BeEmpty(), "StatefulSet should exist")

			// Services
			svcList := &corev1.ServiceList{}
			err = k8sClient.List(ctx, svcList, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
			Expect(err).NotTo(HaveOccurred())
			Expect(svcList.Items).NotTo(BeEmpty(), "Service should exist")

			// ConfigMaps
			cmList := &corev1.ConfigMapList{}
			err = k8sClient.List(ctx, cmList, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
			Expect(err).NotTo(HaveOccurred())
			Expect(cmList.Items).NotTo(BeEmpty(), "ConfigMap should exist")

			// Robin Deployment
			deployList := &appsv1.DeploymentList{}
			err = k8sClient.List(ctx, deployList, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
			Expect(err).NotTo(HaveOccurred())
			Expect(deployList.Items).NotTo(BeEmpty(), "Robin Deployment should exist")

			// RedkeyConfigs
			configList := &redkeyv1beta1.RedkeyConfigList{}
			err = k8sClient.List(ctx, configList, client.InNamespace(clusterNs),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
			Expect(err).NotTo(HaveOccurred())
			Expect(configList.Items).NotTo(BeEmpty(), "RedkeyConfig should exist")

			By("deleting the Redkey")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, cluster)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Delete(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("verifying all child resources are deleted via cascading ownership")
			Eventually(func() int {
				list := &appsv1.StatefulSetList{}
				if err := k8sClient.List(ctx, list, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return -1
				}
				return len(list.Items)
			}, 2*time.Minute, 5*time.Second).Should(Equal(0), "StatefulSets should be deleted")

			Eventually(func() int {
				list := &corev1.ServiceList{}
				if err := k8sClient.List(ctx, list, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return -1
				}
				return len(list.Items)
			}, 2*time.Minute, 5*time.Second).Should(Equal(0), "Services should be deleted")

			Eventually(func() int {
				list := &corev1.ConfigMapList{}
				if err := k8sClient.List(ctx, list, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return -1
				}
				return len(list.Items)
			}, 2*time.Minute, 5*time.Second).Should(Equal(0), "ConfigMaps should be deleted")

			Eventually(func() int {
				list := &appsv1.DeploymentList{}
				if err := k8sClient.List(ctx, list, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return -1
				}
				return len(list.Items)
			}, 2*time.Minute, 5*time.Second).Should(Equal(0), "Robin Deployment should be deleted")

			Eventually(func() int {
				list := &redkeyv1beta1.RedkeyConfigList{}
				if err := k8sClient.List(ctx, list, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return -1
				}
				return len(list.Items)
			}, 2*time.Minute, 5*time.Second).Should(Equal(0), "RedkeyConfigs should be deleted")

			By("verifying Redis pods are terminated")
			Eventually(func() int {
				pods := &corev1.PodList{}
				if err := k8sClient.List(ctx, pods, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return -1
				}
				return len(pods.Items)
			}, 2*time.Minute, 5*time.Second).Should(Equal(0), "Redis pods should be terminated")
		})
	})

	Context("Recreate cluster after deletion", func() {
		const clusterName = "lifecycle-recreate"

		It("should successfully recreate a cluster with the same name after deletion", func() {
			By("creating the initial cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("inserting data")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.InsertKeys(clusterNs, podNames[0], 5)
			Expect(err).NotTo(HaveOccurred())

			By("deleting the cluster")
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, cluster)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Delete(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for all resources to be cleaned up")
			Eventually(func() int {
				pods := &corev1.PodList{}
				if err := k8sClient.List(ctx, pods, client.InNamespace(clusterNs),
					client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
					return -1
				}
				return len(pods.Items)
			}, 2*time.Minute, 5*time.Second).Should(Equal(0))

			By("recreating the cluster with the same name")
			opts = framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err = framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the recreated cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the recreated cluster is healthy and empty")
			podNames, err = framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())

			total, err := framework.GetDBSize(clusterNs, podNames)
			Expect(err).NotTo(HaveOccurred())
			Expect(total).To(Equal(0), "Recreated cluster should have no data from the previous instance")
		})
	})
})
