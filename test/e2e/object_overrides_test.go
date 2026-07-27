// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
)

const tolerationValueRedis = "redis"

var _ = Describe("Object Overrides", Ordered, Label("overrides"), func() {
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
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-overrides")
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

	Context("StatefulSet override", func() {
		const clusterName = "overrides-sts"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("applies pod template and metadata overrides while preserving identity", func() {
			By("creating a cluster with a StatefulSet override")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts = opts.WithStatefulSetOverride(&redkeyv1beta1.PartialStatefulSet{
				Metadata: metav1.ObjectMeta{
					Annotations: map[string]string{"backup.io/enabled": "true"},
					Labels:      map[string]string{"tier": "cache"},
				},
				Spec: &redkeyv1beta1.PartialStatefulSetSpec{
					Template: &redkeyv1beta1.PartialPodTemplateSpec{
						Metadata: metav1.ObjectMeta{
							Annotations: map[string]string{"sidecar.io/inject": "false"},
						},
						Spec: redkeyv1beta1.PartialPodSpec{
							Tolerations: []corev1.Toleration{
								{
									Key:      "dedicated",
									Operator: corev1.TolerationOpEqual,
									Value:    tolerationValueRedis,
									Effect:   corev1.TaintEffectNoSchedule,
								},
							},
						},
					},
				},
			})
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the StatefulSet reflects the override")
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, sts)).To(Succeed())

			Expect(sts.Annotations).To(HaveKeyWithValue("backup.io/enabled", "true"))
			Expect(sts.Labels).To(HaveKeyWithValue("tier", "cache"))
			Expect(sts.Spec.Template.Annotations).To(HaveKeyWithValue("sidecar.io/inject", "false"))

			tolerationFound := false
			for _, t := range sts.Spec.Template.Spec.Tolerations {
				if t.Key == "dedicated" && t.Value == tolerationValueRedis {
					tolerationFound = true
					break
				}
			}
			Expect(tolerationFound).To(BeTrue(), "expected the dedicated toleration to be applied")

			By("verifying StatefulSet identity is preserved")
			Expect(*sts.Spec.Replicas).To(Equal(int32(3)))
			Expect(sts.Spec.ServiceName).To(Equal(clusterName))
			Expect(sts.Spec.Template.Spec.Containers[0].Name).To(Equal("redis"))
			Expect(sts.Labels).To(HaveKeyWithValue("redkey.inditex.dev/cluster", clusterName))

			By("verifying the cluster remains healthy")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Service override", func() {
		const clusterName = "overrides-svc"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("applies extra ports and annotations while remaining headless", func() {
			By("creating a cluster with a Service override")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			opts = opts.WithServiceOverride(&redkeyv1beta1.PartialService{
				Metadata: metav1.ObjectMeta{
					Annotations: map[string]string{"team": "infra"},
				},
				Spec: &redkeyv1beta1.PartialServiceSpec{
					Ports: []corev1.ServicePort{
						{Name: "metrics", Port: 9121, TargetPort: intstr.FromInt(9121)},
					},
				},
			})
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for the cluster to reach Ready")
			_, err = framework.WaitForClusterReady(ctx, k8sClient,
				types.NamespacedName{Name: clusterName, Namespace: clusterNs},
				framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the Service reflects the override")
			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, svc)).To(Succeed())

			Expect(svc.Annotations).To(HaveKeyWithValue("team", "infra"))

			portNames := map[string]bool{}
			for _, p := range svc.Spec.Ports {
				portNames[p.Name] = true
			}
			Expect(portNames).To(HaveKey("metrics"))
			Expect(portNames).To(HaveKey("client"))
			Expect(portNames).To(HaveKey("gossip"))

			By("verifying the Service identity is preserved (headless + selector)")
			Expect(svc.Spec.ClusterIP).To(Equal("None"))
			Expect(svc.Spec.Selector).To(HaveKeyWithValue("redkey.inditex.dev/cluster", clusterName))

			By("verifying the cluster remains healthy")
			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Override transitions", func() {
		const clusterName = "overrides-transition"

		AfterAll(func() {
			_ = framework.DeleteRedkey(ctx, k8sClient, clusterName, clusterNs)
		})

		It("adds overrides on update and reverts cleanly when removed", func() {
			key := types.NamespacedName{Name: clusterName, Namespace: clusterNs}

			By("creating a cluster without any override")
			opts := framework.DefaultClusterOptions(clusterName, clusterNs)
			_, err := framework.CreateRedkey(ctx, k8sClient, opts)
			Expect(err).NotTo(HaveOccurred())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.CreationTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("verifying the generated objects carry no override fields")
			sts := &appsv1.StatefulSet{}
			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())
			Expect(hasToleration(sts, "dedicated")).To(BeFalse())

			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())
			Expect(hasServicePort(svc, "metrics")).To(BeFalse())

			By("updating the cluster to add StatefulSet and Service overrides")
			Expect(updateOverride(ctx, key, &redkeyv1beta1.RedkeyOverrideSpec{
				StatefulSet: &redkeyv1beta1.PartialStatefulSet{
					Metadata: metav1.ObjectMeta{
						Annotations: map[string]string{"backup.io/enabled": "true"},
						Labels:      map[string]string{"tier": "cache"},
					},
					Spec: &redkeyv1beta1.PartialStatefulSetSpec{
						Template: &redkeyv1beta1.PartialPodTemplateSpec{
							Metadata: metav1.ObjectMeta{
								Annotations: map[string]string{"sidecar.io/inject": "false"},
								Labels:      map[string]string{"pod-tier": "cache"},
							},
							Spec: redkeyv1beta1.PartialPodSpec{
								Tolerations: []corev1.Toleration{
									{
										Key:      "dedicated",
										Operator: corev1.TolerationOpEqual,
										Value:    tolerationValueRedis,
										Effect:   corev1.TaintEffectNoSchedule,
									},
								},
							},
						},
					},
				},
				Service: &redkeyv1beta1.PartialService{
					Spec: &redkeyv1beta1.PartialServiceSpec{
						Ports: []corev1.ServicePort{
							{Name: "metrics", Port: 9121, TargetPort: intstr.FromInt(9121)},
						},
					},
				},
			})).To(Succeed())

			By("waiting until both objects reflect the override")
			Eventually(func(g Gomega) {
				current := &appsv1.StatefulSet{}
				g.Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
				g.Expect(hasToleration(current, "dedicated")).To(BeTrue())
			}, framework.DefaultTimeout, 5*time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				current := &corev1.Service{}
				g.Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
				g.Expect(hasServicePort(current, "metrics")).To(BeTrue())
			}, framework.DefaultTimeout, 5*time.Second).Should(Succeed())

			By("verifying top-level StatefulSet metadata is applied on update (the regression)")
			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())
			Expect(sts.Annotations).To(HaveKeyWithValue("backup.io/enabled", "true"))
			Expect(sts.Labels).To(HaveKeyWithValue("tier", "cache"))
			Expect(sts.Spec.Template.Annotations).To(HaveKeyWithValue("sidecar.io/inject", "false"))

			By("verifying pod-template metadata is merged, not replaced")
			Expect(sts.Spec.Template.Labels).To(HaveKeyWithValue("pod-tier", "cache"))
			Expect(sts.Spec.Template.Labels).To(HaveKeyWithValue("redkey.inditex.dev/cluster", clusterName),
				"the cluster selector label must survive the pod-template metadata merge")

			By("verifying identity is still preserved and the cluster stays healthy")
			Expect(*sts.Spec.Replicas).To(Equal(int32(3)))
			Expect(sts.Spec.ServiceName).To(Equal(clusterName))
			Expect(sts.Labels).To(HaveKeyWithValue("redkey.inditex.dev/cluster", clusterName))
			Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())
			Expect(svc.Spec.ClusterIP).To(Equal("None"))

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			By("removing the override and verifying both objects revert")
			Expect(updateOverride(ctx, key, nil)).To(Succeed())

			Eventually(func(g Gomega) {
				current := &appsv1.StatefulSet{}
				g.Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
				g.Expect(hasToleration(current, "dedicated")).To(BeFalse())
			}, framework.DefaultTimeout, 5*time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				current := &corev1.Service{}
				g.Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
				g.Expect(hasServicePort(current, "metrics")).To(BeFalse())
			}, framework.DefaultTimeout, 5*time.Second).Should(Succeed())

			By("verifying top-level StatefulSet metadata reverts while the cluster label survives")
			Expect(k8sClient.Get(ctx, key, sts)).To(Succeed())
			Expect(sts.Annotations).NotTo(HaveKey("backup.io/enabled"))
			Expect(sts.Labels).NotTo(HaveKey("tier"))
			Expect(sts.Labels).To(HaveKeyWithValue("redkey.inditex.dev/cluster", clusterName))
			Expect(sts.Spec.Template.Labels).NotTo(HaveKey("pod-tier"))
			Expect(sts.Spec.Template.Labels).To(HaveKeyWithValue("redkey.inditex.dev/cluster", clusterName))

			By("verifying the default ports survive the revert and the cluster stays healthy")
			Expect(k8sClient.Get(ctx, key, svc)).To(Succeed())
			Expect(hasServicePort(svc, "client")).To(BeTrue())
			Expect(hasServicePort(svc, "gossip")).To(BeTrue())

			_, err = framework.WaitForClusterReady(ctx, k8sClient, key, framework.DefaultTimeout)
			Expect(err).NotTo(HaveOccurred())

			podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, 3)
			Expect(err).NotTo(HaveOccurred())
			err = framework.VerifyClusterHealthy(clusterNs, podNames[0], 3)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

// updateOverride refetches the cluster and sets spec.override, retrying on
// optimistic-concurrency conflicts caused by the operator mutating the object.
func updateOverride(
	ctx context.Context, key types.NamespacedName, override *redkeyv1beta1.RedkeyOverrideSpec,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &redkeyv1beta1.Redkey{}
		if err := k8sClient.Get(ctx, key, cluster); err != nil {
			return err
		}
		cluster.Spec.Override = override
		return k8sClient.Update(ctx, cluster)
	})
}

// hasToleration reports whether the StatefulSet pod template has a toleration with the given key.
func hasToleration(sts *appsv1.StatefulSet, key string) bool {
	for _, t := range sts.Spec.Template.Spec.Tolerations {
		if t.Key == key {
			return true
		}
	}
	return false
}

// hasServicePort reports whether the Service exposes a port with the given name.
func hasServicePort(svc *corev1.Service, name string) bool {
	for _, p := range svc.Spec.Ports {
		if p.Name == name {
			return true
		}
	}
	return false
}
