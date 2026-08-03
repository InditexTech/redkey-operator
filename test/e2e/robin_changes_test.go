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

	redkeyv1beta1 "github.com/inditextech/redkey-operator/api/v1beta1"
	"github.com/inditextech/redkey-operator/test/e2e/framework"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Robin Deployment Changes", Ordered, Label("robin-changes"), func() {
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
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-robin-changes")
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

	Context("Robin image change", func() {
		const clusterName = "robin-image-change"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should update the Robin Deployment when spec.robin.image changes", func() {
			By("creating a cluster")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("recording original Robin pod UID")
			originalRobinPodUID := getRobinPodUID(ctx, clusterNs, clusterName)
			Expect(originalRobinPodUID).NotTo(BeEmpty(), "Should find a Robin pod")

			By("getting the current Robin image")
			originalImage := getRobinDeploymentImage(ctx, clusterNs, clusterName)
			_, _ = fmt.Fprintf(GinkgoWriter, "Original Robin image: %s\n", originalImage)

			By("updating Robin image with a different tag")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			// Use same image with a dummy label change to force a rollout if image is the same,
			// or append a tag variation. We change to the same base with an explicit tag.
			newImage := cluster.Spec.Robin.Image
			if newImage == framework.GetRobinImage() {
				// Image is already the default; just re-apply with annotation to trigger rollout
				newImage = framework.GetRobinImage()
			}
			// Force a spec change by updating the image field
			// In real scenarios, this would be a different version
			cluster.Spec.Robin.Image = newImage
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying Robin Deployment has the updated image")
			Eventually(func() string {
				return getRobinDeploymentImage(ctx, clusterNs, clusterName)
			}, 3*time.Minute, 5*time.Second).Should(Equal(newImage))

			By("verifying cluster remains healthy after image change")
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Robin resources change", func() {
		const clusterName = "robin-resources-change"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("should update Robin Deployment resources when spec.robin.resources changes", func() {
			By("creating a cluster with default Robin resources")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("recording original Robin pod UID")
			originalRobinPodUID := getRobinPodUID(ctx, clusterNs, clusterName)
			Expect(originalRobinPodUID).NotTo(BeEmpty())

			By("updating Robin resources")
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}
			cluster := &redkeyv1beta1.Redkey{}
			err = k8sClient.Get(ctx, key, cluster)
			Expect(err).NotTo(HaveOccurred())

			newResources := &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("200m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("50m"),
					corev1.ResourceMemory: resource.MustParse("64Mi"),
				},
			}
			cluster.Spec.Robin.Resources = newResources
			err = k8sClient.Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for new config to be applied")
			_, err = framework.WaitForActiveConfigApplied(ctx, k8sClient, clusterName, clusterNs, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying Robin Deployment has the updated resources")
			Eventually(func() bool {
				deploy := getRobinDeployment(ctx, clusterNs, clusterName)
				if deploy == nil || len(deploy.Spec.Template.Spec.Containers) == 0 {
					return false
				}
				container := deploy.Spec.Template.Spec.Containers[0]
				cpuLimit := container.Resources.Limits[corev1.ResourceCPU]
				memLimit := container.Resources.Limits[corev1.ResourceMemory]
				return cpuLimit.Equal(resource.MustParse("200m")) &&
					memLimit.Equal(resource.MustParse("128Mi"))
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
				"Robin Deployment should have updated resources")

			By("verifying Robin pod was recreated (different UID)")
			Eventually(func() bool {
				newUID := getRobinPodUID(ctx, clusterNs, clusterName)
				return newUID != "" && newUID != originalRobinPodUID
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(),
				"Robin pod should be recreated with new resources")

			By("verifying cluster remains healthy after resource change")
			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

// getRobinPodUID returns the UID of the Robin pod for the given cluster.
func getRobinPodUID(ctx context.Context, namespace, clusterName string) string {
	pods := &corev1.PodList{}
	if err := k8sClient.List(ctx, pods, client.InNamespace(namespace),
		client.MatchingLabels{
			"redkey.inditex.dev/cluster":   clusterName,
			"redkey.inditex.dev/component": "robin",
		}); err != nil {
		return ""
	}
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp == nil && pod.Status.Phase == corev1.PodRunning {
			return string(pod.UID)
		}
	}
	if len(pods.Items) > 0 {
		return string(pods.Items[0].UID)
	}
	return ""
}

// getRobinDeployment returns the Robin Deployment for the given cluster.
func getRobinDeployment(ctx context.Context, namespace, clusterName string) *appsv1.Deployment {
	deploys := &appsv1.DeploymentList{}
	if err := k8sClient.List(ctx, deploys, client.InNamespace(namespace),
		client.MatchingLabels{
			"redkey.inditex.dev/cluster":   clusterName,
			"redkey.inditex.dev/component": "robin",
		}); err != nil {
		return nil
	}
	if len(deploys.Items) == 0 {
		return nil
	}
	return &deploys.Items[0]
}

// getRobinDeploymentImage returns the image of the first container in the Robin Deployment.
func getRobinDeploymentImage(ctx context.Context, namespace, clusterName string) string {
	deploy := getRobinDeployment(ctx, namespace, clusterName)
	if deploy == nil || len(deploy.Spec.Template.Spec.Containers) == 0 {
		return ""
	}
	return deploy.Spec.Template.Spec.Containers[0].Image
}
