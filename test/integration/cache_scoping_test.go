// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/inditextech/redkey-operator/internal/controller"
)

var _ = Describe("Manager cache scoping", Ordered, func() {
	const namespace = "cache-scoping"

	var (
		cacheCtx    context.Context
		cacheCancel context.CancelFunc
		c           cache.Cache
	)
	robinSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name:      "cache-scope-robin",
		Namespace: namespace,
		Labels:    map[string]string{controller.ComponentLabel: controller.RobinComponent},
	}}
	foreignSA := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name:      "cache-scope-foreign",
		Namespace: namespace,
	}}

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}

	BeforeAll(func() {
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		Expect(k8sClient.Create(ctx, robinSA)).To(Succeed())
		Expect(k8sClient.Create(ctx, foreignSA)).To(Succeed())

		opts := cache.Options{Scheme: scheme.Scheme}
		controller.ConfigureCache(&opts)
		var err error
		c, err = cache.New(cfg, opts)
		Expect(err).NotTo(HaveOccurred())

		cacheCtx, cacheCancel = context.WithCancel(ctx)
		go func() {
			defer GinkgoRecover()
			Expect(c.Start(cacheCtx)).To(Succeed())
		}()
	})

	AfterAll(func() {
		cacheCancel()
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, robinSA))).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, foreignSA))).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ns))).To(Succeed())
	})

	It("caches only objects carrying the Robin component label, without managedFields", func() {
		var sa corev1.ServiceAccount
		Eventually(func() error {
			return c.Get(ctx, types.NamespacedName{Name: robinSA.Name, Namespace: namespace}, &sa)
		}).Should(Succeed())
		Expect(sa.ManagedFields).To(BeEmpty())

		err := c.Get(ctx, types.NamespacedName{Name: foreignSA.Name, Namespace: namespace}, &corev1.ServiceAccount{})
		Expect(errors.IsNotFound(err)).To(BeTrue(), "expected NotFound, got %v", err)

		var list corev1.ServiceAccountList
		Expect(c.List(ctx, &list, client.InNamespace(namespace))).To(Succeed())
		names := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
		Expect(names).To(ConsistOf(robinSA.Name))
	})
})
