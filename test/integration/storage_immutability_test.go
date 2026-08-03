// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	redisv1 "github.com/inditextech/redkey-operator/api/v1beta1"
)

// These tests exercise the CEL immutability rules declared on RedkeySpec
// (ephemeral, storage, storageClassName, accessModes). The rules are enforced by the
// envtest API server, so attempts to change those fields after creation must be rejected.
var _ = Describe("Redkey storage immutability (CEL)", func() {
	const namespace = "default"

	ctx := context.Background()

	newPersistentCluster := func(name string) *redisv1.Redkey {
		purgeKeys := false
		return &redisv1.Redkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: redisv1.RedkeySpec{
				Primaries:            3,
				Ephemeral:            false,
				Storage:              "1Gi",
				StorageClassName:     "standard",
				AccessModes:          []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				PurgeKeysOnRebalance: &purgeKeys,
				Robin:                redisv1.RobinSpec{Image: "redkey-robin:latest"},
			},
		}
	}

	It("rejects changing the storage size", func() {
		const name = "immutable-storage"
		cluster := newPersistentCluster(name)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

		var fetched redisv1.Redkey
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &fetched)).To(Succeed())
		fetched.Spec.Storage = "2Gi"
		err := k8sClient.Update(ctx, &fetched)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Changing the storage size is not allowed"))
	})

	It("rejects changing the ephemeral field", func() {
		const name = "immutable-ephemeral"
		cluster := newPersistentCluster(name)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

		var fetched redisv1.Redkey
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &fetched)).To(Succeed())
		fetched.Spec.Ephemeral = true
		err := k8sClient.Update(ctx, &fetched)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Changing the ephemeral field is not allowed"))
	})

	It("rejects changing the storage class name", func() {
		const name = "immutable-storageclass"
		cluster := newPersistentCluster(name)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

		var fetched redisv1.Redkey
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &fetched)).To(Succeed())
		fetched.Spec.StorageClassName = "fast"
		err := k8sClient.Update(ctx, &fetched)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Changing the storage class name is not allowed"))
	})

	It("rejects changing the storage access modes", func() {
		const name = "immutable-accessmodes"
		cluster := newPersistentCluster(name)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

		var fetched redisv1.Redkey
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &fetched)).To(Succeed())
		fetched.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
		err := k8sClient.Update(ctx, &fetched)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Changing the storage access modes is not allowed"))
	})

	It("allows changing mutable fields such as primaries", func() {
		const name = "immutable-mutable-ok"
		cluster := newPersistentCluster(name)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func() { deleteCluster(ctx, name, namespace) })

		var fetched redisv1.Redkey
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &fetched)).To(Succeed())
		fetched.Spec.Primaries = 5
		Expect(k8sClient.Update(ctx, &fetched)).To(Succeed())
	})
})
