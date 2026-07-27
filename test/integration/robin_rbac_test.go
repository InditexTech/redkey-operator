// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/internal/controller"
)

var _ = Describe("Robin RBAC Reconciliation", func() {
	const clusterName = "rbac-test-cluster"
	const namespace = "default"

	ctx := context.Background()
	namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}
	rbacName := types.NamespacedName{Name: clusterName + "-robin", Namespace: namespace}

	var reconciler *controller.RedkeyReconciler

	reconcileIt := func() {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}

	BeforeEach(func() {
		reconciler = newReconciler()
	})

	Context("When creating a new Redkey", Ordered, func() {
		BeforeAll(func() {
			cluster := &redisv1.Redkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeySpec{
					Ephemeral: true,
					Primaries: 3,
					Robin:     redisv1.RobinSpec{Image: "redkey-robin:latest"},
				},
			}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		})

		AfterAll(func() {
			deleteCluster(ctx, clusterName, namespace)
		})

		It("should create ServiceAccount, Role and RoleBinding after reconciliation", func() {
			reconcileIt()

			By("verifying the ServiceAccount exists with correct owner reference")
			var sa corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, rbacName, &sa)).To(Succeed())
			Expect(sa.OwnerReferences).NotTo(BeEmpty())
			Expect(sa.OwnerReferences[0].Kind).To(Equal("Redkey"))
			Expect(sa.OwnerReferences[0].Name).To(Equal(clusterName))

			By("verifying the Role exists with correct rules and owner reference")
			var role rbacv1.Role
			Expect(k8sClient.Get(ctx, rbacName, &role)).To(Succeed())
			Expect(role.Rules).To(Equal(controller.DesiredRobinRules()))
			Expect(role.OwnerReferences).NotTo(BeEmpty())
			Expect(role.OwnerReferences[0].Kind).To(Equal("Redkey"))

			By("verifying the RoleBinding exists with correct subjects, roleRef and owner reference")
			var rb rbacv1.RoleBinding
			Expect(k8sClient.Get(ctx, rbacName, &rb)).To(Succeed())
			Expect(rb.Subjects).To(HaveLen(1))
			Expect(rb.Subjects[0].Kind).To(Equal("ServiceAccount"))
			Expect(rb.Subjects[0].Name).To(Equal(clusterName + "-robin"))
			Expect(rb.RoleRef.Kind).To(Equal("Role"))
			Expect(rb.RoleRef.Name).To(Equal(clusterName + "-robin"))
			Expect(rb.OwnerReferences).NotTo(BeEmpty())
			Expect(rb.OwnerReferences[0].Kind).To(Equal("Redkey"))
		})
	})

	Context("When an RBAC resource is deleted", Ordered, func() {
		BeforeAll(func() {
			cluster := &redisv1.Redkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeySpec{
					Ephemeral: true,
					Primaries: 3,
					Robin:     redisv1.RobinSpec{Image: "redkey-robin:latest"},
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

		It("should recreate the ServiceAccount when deleted", func() {
			By("deleting the ServiceAccount")
			sa := &corev1.ServiceAccount{}
			Expect(k8sClient.Get(ctx, rbacName, sa)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sa)).To(Succeed())

			By("verifying the ServiceAccount is gone")
			Expect(errors.IsNotFound(k8sClient.Get(ctx, rbacName, &corev1.ServiceAccount{}))).To(BeTrue())

			By("reconciling and verifying the ServiceAccount is recreated")
			reconcileIt()
			var recreated corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, rbacName, &recreated)).To(Succeed())
			Expect(recreated.OwnerReferences).NotTo(BeEmpty())
			Expect(recreated.OwnerReferences[0].Name).To(Equal(clusterName))
		})

		It("should recreate the Role when deleted", func() {
			By("deleting the Role")
			role := &rbacv1.Role{}
			Expect(k8sClient.Get(ctx, rbacName, role)).To(Succeed())
			Expect(k8sClient.Delete(ctx, role)).To(Succeed())

			By("verifying the Role is gone")
			Expect(errors.IsNotFound(k8sClient.Get(ctx, rbacName, &rbacv1.Role{}))).To(BeTrue())

			By("reconciling and verifying the Role is recreated with correct rules")
			reconcileIt()
			var recreated rbacv1.Role
			Expect(k8sClient.Get(ctx, rbacName, &recreated)).To(Succeed())
			Expect(recreated.Rules).To(Equal(controller.DesiredRobinRules()))
		})

		It("should recreate the RoleBinding when deleted", func() {
			By("deleting the RoleBinding")
			rb := &rbacv1.RoleBinding{}
			Expect(k8sClient.Get(ctx, rbacName, rb)).To(Succeed())
			Expect(k8sClient.Delete(ctx, rb)).To(Succeed())

			By("verifying the RoleBinding is gone")
			Expect(errors.IsNotFound(k8sClient.Get(ctx, rbacName, &rbacv1.RoleBinding{}))).To(BeTrue())

			By("reconciling and verifying the RoleBinding is recreated")
			reconcileIt()
			var recreated rbacv1.RoleBinding
			Expect(k8sClient.Get(ctx, rbacName, &recreated)).To(Succeed())
			Expect(recreated.Subjects).To(HaveLen(1))
			Expect(recreated.Subjects[0].Name).To(Equal(clusterName + "-robin"))
			Expect(recreated.RoleRef.Name).To(Equal(clusterName + "-robin"))
		})
	})

	Context("When an RBAC resource is modified", Ordered, func() {
		BeforeAll(func() {
			cluster := &redisv1.Redkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: namespace,
				},
				Spec: redisv1.RedkeySpec{
					Ephemeral: true,
					Primaries: 3,
					Robin:     redisv1.RobinSpec{Image: "redkey-robin:latest"},
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

		It("should correct a modified Role's rules", func() {
			By("modifying the Role to remove a verb")
			var role rbacv1.Role
			Expect(k8sClient.Get(ctx, rbacName, &role)).To(Succeed())
			role.Rules[0].Verbs = []string{"get", "list", "watch"}
			Expect(k8sClient.Update(ctx, &role)).To(Succeed())

			By("verifying the Role has been tampered with")
			Expect(k8sClient.Get(ctx, rbacName, &role)).To(Succeed())
			Expect(role.Rules[0].Verbs).NotTo(ContainElement("delete"))

			By("reconciling and verifying the Role rules are restored")
			reconcileIt()
			var corrected rbacv1.Role
			Expect(k8sClient.Get(ctx, rbacName, &corrected)).To(Succeed())
			Expect(corrected.Rules).To(Equal(controller.DesiredRobinRules()))
		})

		It("should correct a modified RoleBinding's subjects", func() {
			By("modifying the RoleBinding subject name")
			var rb rbacv1.RoleBinding
			Expect(k8sClient.Get(ctx, rbacName, &rb)).To(Succeed())
			rb.Subjects = []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      "tampered-sa",
					Namespace: namespace,
				},
			}
			Expect(k8sClient.Update(ctx, &rb)).To(Succeed())

			By("verifying the RoleBinding has been tampered with")
			Expect(k8sClient.Get(ctx, rbacName, &rb)).To(Succeed())
			Expect(rb.Subjects[0].Name).To(Equal("tampered-sa"))

			By("reconciling and verifying the RoleBinding subjects are restored")
			reconcileIt()
			var corrected rbacv1.RoleBinding
			Expect(k8sClient.Get(ctx, rbacName, &corrected)).To(Succeed())
			Expect(corrected.Subjects).To(HaveLen(1))
			Expect(corrected.Subjects[0].Name).To(Equal(clusterName + "-robin"))
		})

		It("should correct a modified ServiceAccount's labels", func() {
			By("adding unexpected labels to the ServiceAccount")
			var sa corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, rbacName, &sa)).To(Succeed())
			originalUID := sa.UID

			By("reconciling and verifying the ServiceAccount still exists with correct owner")
			reconcileIt()
			var corrected corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, rbacName, &corrected)).To(Succeed())
			Expect(corrected.UID).To(Equal(originalUID))
			Expect(corrected.OwnerReferences).NotTo(BeEmpty())
			Expect(corrected.OwnerReferences[0].Name).To(Equal(clusterName))
		})
	})
})
