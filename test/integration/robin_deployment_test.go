// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/internal/controller"
)

var _ = Describe("Robin Deployment Reconciliation", func() {
	const clusterName = "deploy-test-cluster"
	const namespace = "default"

	ctx := context.Background()
	namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}
	deployName := types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}

	var reconciler *controller.RedkeyClusterReconciler

	reconcileIt := func() {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}

	BeforeEach(func() {
		reconciler = newReconciler()
	})

	Context("When creating a new RedkeyCluster", Ordered, func() {
		BeforeAll(func() {
			cluster := &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeyClusterSpec{
					Ephemeral: true,
					Primaries: 3,
					Robin: redisv1.RobinSpec{
						Image: "custom-robin:v1.0.0",
						Template: &redisv1.PartialPodTemplateSpec{
							Spec: redisv1.PartialPodSpec{
								Containers: []corev1.Container{
									{
										Image: "custom-robin:v1.0.0",
										Resources: corev1.ResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("100m"),
												corev1.ResourceMemory: resource.MustParse("128Mi"),
											},
											Limits: corev1.ResourceList{
												corev1.ResourceCPU:    resource.MustParse("200m"),
												corev1.ResourceMemory: resource.MustParse("256Mi"),
											},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should create the Deployment with the specified configuration", func() {
			reconcileIt()

			By("verifying the Deployment exists")
			var deploy appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &deploy)).To(Succeed())

			By("verifying OwnerReference is set")
			Expect(deploy.OwnerReferences).NotTo(BeEmpty())
			Expect(deploy.OwnerReferences[0].Kind).To(Equal("RedkeyCluster"))
			Expect(deploy.OwnerReferences[0].Name).To(Equal(clusterName))

			By("verifying replicas default to 1")
			Expect(deploy.Spec.Replicas).NotTo(BeNil())
			Expect(*deploy.Spec.Replicas).To(Equal(int32(1)))

			By("verifying labels")
			Expect(deploy.Labels[controller.ClusterLabel]).To(Equal(clusterName))
			Expect(deploy.Labels["app"]).To(Equal("redkey-robin"))
			Expect(deploy.Spec.Template.Labels[controller.ClusterLabel]).To(Equal(clusterName))
			Expect(deploy.Spec.Template.Labels["app"]).To(Equal("redkey-robin"))

			By("verifying the container image from spec override")
			Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(deploy.Spec.Template.Spec.Containers[0].Name).To(Equal("robin"))
			Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal("custom-robin:v1.0.0"))

			By("verifying resource requests and limits")
			resources := deploy.Spec.Template.Spec.Containers[0].Resources
			Expect(resources.Requests[corev1.ResourceCPU]).To(Equal(resource.MustParse("100m")))
			Expect(resources.Requests[corev1.ResourceMemory]).To(Equal(resource.MustParse("128Mi")))
			Expect(resources.Limits[corev1.ResourceCPU]).To(Equal(resource.MustParse("200m")))
			Expect(resources.Limits[corev1.ResourceMemory]).To(Equal(resource.MustParse("256Mi")))

			By("verifying the ServiceAccount name")
			Expect(deploy.Spec.Template.Spec.ServiceAccountName).To(Equal(clusterName + "-robin"))
		})
	})

	Context("When the Deployment drifts from the desired state", Ordered, func() {
		BeforeAll(func() {
			cluster := &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeyClusterSpec{
					Ephemeral: true,
					Primaries: 3,
					Robin: redisv1.RobinSpec{
						Image: "robin:v2.0.0",
						Template: &redisv1.PartialPodTemplateSpec{
							Spec: redisv1.PartialPodSpec{
								Containers: []corev1.Container{
									{Image: "robin:v2.0.0"},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			r := newReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should recreate the Deployment when deleted", func() {
			By("deleting the Deployment")
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, deployName, deploy)).To(Succeed())
			Expect(k8sClient.Delete(ctx, deploy)).To(Succeed())

			By("verifying the Deployment is gone")
			Expect(errors.IsNotFound(k8sClient.Get(ctx, deployName, &appsv1.Deployment{}))).To(BeTrue())

			By("reconciling and verifying the Deployment is recreated")
			reconcileIt()
			var recreated appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &recreated)).To(Succeed())
			Expect(recreated.Spec.Template.Spec.Containers[0].Image).To(Equal("robin:v2.0.0"))
		})

		It("should correct a drifted container image", func() {
			By("tampering with the Deployment image")
			var deploy appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &deploy)).To(Succeed())
			base := deploy.DeepCopy()
			deploy.Spec.Template.Spec.Containers[0].Image = "hacked-robin:evil"
			Expect(k8sClient.Patch(ctx, &deploy, client.MergeFrom(base))).To(Succeed())

			By("verifying the Deployment has been tampered with")
			Expect(k8sClient.Get(ctx, deployName, &deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal("hacked-robin:evil"))

			By("reconciling and verifying the image is restored")
			reconcileIt()
			var corrected appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &corrected)).To(Succeed())
			Expect(corrected.Spec.Template.Spec.Containers[0].Image).To(Equal("robin:v2.0.0"))
		})

		It("should correct drifted replicas", func() {
			By("tampering with the Deployment replicas")
			var deploy appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &deploy)).To(Succeed())
			base := deploy.DeepCopy()
			badReplicas := int32(5)
			deploy.Spec.Replicas = &badReplicas
			Expect(k8sClient.Patch(ctx, &deploy, client.MergeFrom(base))).To(Succeed())

			By("verifying the Deployment replicas have been tampered with")
			Expect(k8sClient.Get(ctx, deployName, &deploy)).To(Succeed())
			Expect(*deploy.Spec.Replicas).To(Equal(int32(5)))

			By("reconciling and verifying replicas are restored to 1")
			reconcileIt()
			var corrected appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &corrected)).To(Succeed())
			Expect(*corrected.Spec.Replicas).To(Equal(int32(1)))
		})

		It("should correct drifted Deployment-level labels", func() {
			By("tampering with the Deployment-level labels")
			var deploy appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &deploy)).To(Succeed())
			base := deploy.DeepCopy()
			deploy.Labels["app"] = "tampered"
			deploy.Labels["extra"] = "unexpected"
			Expect(k8sClient.Patch(ctx, &deploy, client.MergeFrom(base))).To(Succeed())

			By("verifying the Deployment labels have been tampered with")
			Expect(k8sClient.Get(ctx, deployName, &deploy)).To(Succeed())
			Expect(deploy.Labels["app"]).To(Equal("tampered"))
			Expect(deploy.Labels).To(HaveKey("extra"))

			By("reconciling and verifying Deployment-level labels are restored")
			reconcileIt()
			var corrected appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &corrected)).To(Succeed())
			Expect(corrected.Labels["app"]).To(Equal("redkey-robin"))
			Expect(corrected.Labels[controller.ClusterLabel]).To(Equal(clusterName))
		})
	})

	Context("When the RedkeyCluster spec is updated", Ordered, func() {
		BeforeAll(func() {
			cluster := &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeyClusterSpec{
					Ephemeral: true,
					Primaries: 3,
					Robin: redisv1.RobinSpec{
						Image: "robin:v1.0.0",
						Template: &redisv1.PartialPodTemplateSpec{
							Spec: redisv1.PartialPodSpec{
								Containers: []corev1.Container{
									{Image: "robin:v1.0.0"},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			r := newReconciler()
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should update the Deployment when the Robin image is changed", func() {
			By("verifying initial image")
			var deploy appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal("robin:v1.0.0"))

			By("updating the RedkeyCluster Robin image")
			var cluster redisv1.RedkeyCluster
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			cluster.Spec.Robin.Template.Spec.Containers[0].Image = "robin:v3.0.0"
			Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())

			By("reconciling and verifying the Deployment image is updated")
			reconcileIt()
			var updated appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &updated)).To(Succeed())
			Expect(updated.Spec.Template.Spec.Containers[0].Image).To(Equal("robin:v3.0.0"))
		})

		It("should update the Deployment when resources are added", func() {
			By("updating the RedkeyCluster Robin resources")
			var cluster redisv1.RedkeyCluster
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			cluster.Spec.Robin.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
			}
			Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())

			By("reconciling and verifying the Deployment resources are updated")
			reconcileIt()
			var updated appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &updated)).To(Succeed())
			resources := updated.Spec.Template.Spec.Containers[0].Resources
			Expect(resources.Requests[corev1.ResourceCPU]).To(Equal(resource.MustParse("500m")))
			Expect(resources.Requests[corev1.ResourceMemory]).To(Equal(resource.MustParse("512Mi")))
		})

		It("should update the Deployment when node selector is added", func() {
			By("updating the RedkeyCluster Robin node selector")
			var cluster redisv1.RedkeyCluster
			Expect(k8sClient.Get(ctx, namespacedName, &cluster)).To(Succeed())
			cluster.Spec.Robin.Template.Spec.NodeSelector = map[string]string{
				"node-role": "infra",
			}
			Expect(k8sClient.Update(ctx, &cluster)).To(Succeed())

			By("reconciling and verifying the Deployment node selector is set")
			reconcileIt()
			var updated appsv1.Deployment
			Expect(k8sClient.Get(ctx, deployName, &updated)).To(Succeed())
			Expect(updated.Spec.Template.Spec.NodeSelector).To(HaveKeyWithValue("node-role", "infra"))
		})
	})
})
