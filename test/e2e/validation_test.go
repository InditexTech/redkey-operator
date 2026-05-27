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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

var _ = Describe("Validation", Ordered, Label("validation"), func() {
	var (
		ctx       context.Context
		cancel    context.CancelFunc
		ns        *corev1.Namespace
		clusterNs string
	)

	BeforeAll(func() {
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Minute)

		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(ctx, k8sClient, "e2e-validation")
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

	Context("Invalid spec: ephemeral and storage both set", func() {
		It("should reject a RedkeyCluster with both ephemeral=true and storage set", func() {
			cluster := &redkeyv1beta1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-both-storage",
					Namespace: clusterNs,
				},
				Spec: redkeyv1beta1.RedkeyClusterSpec{
					Primaries:            3,
					ReplicasPerPrimary:   0,
					Ephemeral:            true,
					Storage:              "100Mi",
					Image:                framework.GetRedisImage(),
					Robin:                redkeyv1beta1.RobinSpec{Image: framework.GetRobinImage()},
					PurgeKeysOnRebalance: ptr.To(true),
					DeletePVC:            ptr.To(false),
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			}
			err := k8sClient.Create(ctx, cluster)
			Expect(err).To(HaveOccurred(), "Should reject cluster with both ephemeral and storage")
			Expect(errors.IsInvalid(err) || errors.IsForbidden(err)).To(BeTrue(),
				"Error should be validation-related, got: %v", err)
		})
	})

	Context("Invalid spec: neither ephemeral nor storage", func() {
		It("should reject a RedkeyCluster with neither ephemeral nor storage", func() {
			cluster := &redkeyv1beta1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-no-storage",
					Namespace: clusterNs,
				},
				Spec: redkeyv1beta1.RedkeyClusterSpec{
					Primaries:          3,
					ReplicasPerPrimary: 0,
					Ephemeral:          false,
					Image:              framework.GetRedisImage(),
					Robin:              redkeyv1beta1.RobinSpec{Image: framework.GetRobinImage()},
					DeletePVC:          ptr.To(false),
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			}
			err := k8sClient.Create(ctx, cluster)
			Expect(err).To(HaveOccurred(), "Should reject cluster with neither ephemeral nor storage")
			Expect(errors.IsInvalid(err) || errors.IsForbidden(err)).To(BeTrue(),
				"Error should be validation-related, got: %v", err)
		})
	})

	Context("Invalid spec: purgeKeysOnRebalance on non-ephemeral", func() {
		It("should reject purgeKeysOnRebalance=true on a PVC cluster", func() {
			cluster := &redkeyv1beta1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-purge-pvc",
					Namespace: clusterNs,
				},
				Spec: redkeyv1beta1.RedkeyClusterSpec{
					Primaries:            3,
					ReplicasPerPrimary:   0,
					Ephemeral:            false,
					Storage:              "100Mi",
					AccessModes:          []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					PurgeKeysOnRebalance: ptr.To(true),
					Image:                framework.GetRedisImage(),
					Robin:                redkeyv1beta1.RobinSpec{Image: framework.GetRobinImage()},
					DeletePVC:            ptr.To(false),
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			}
			err := k8sClient.Create(ctx, cluster)
			Expect(err).To(HaveOccurred(), "Should reject purgeKeysOnRebalance=true on PVC cluster")
			Expect(errors.IsInvalid(err) || errors.IsForbidden(err)).To(BeTrue(),
				"Error should be validation-related, got: %v", err)
		})
	})

	Context("Immutable fields", func() {
		const clusterName = "validation-immutable"

		It("should reject changes to immutable storage fields", func() {
			By("creating a valid PVC cluster")
			cluster := &redkeyv1beta1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNs,
				},
				Spec: redkeyv1beta1.RedkeyClusterSpec{
					Primaries:            3,
					ReplicasPerPrimary:   0,
					Ephemeral:            false,
					Storage:              "100Mi",
					AccessModes:          []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Image:                framework.GetRedisImage(),
					Robin:                redkeyv1beta1.RobinSpec{Image: framework.GetRobinImage()},
					DeletePVC:            ptr.To(true),
					PurgeKeysOnRebalance: ptr.To(false),
					Resources: &corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			}
			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			By("attempting to change ephemeral (immutable)")
			err = k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNs}, cluster)
			Expect(err).NotTo(HaveOccurred())
			cluster.Spec.Ephemeral = true
			err = k8sClient.Update(ctx, cluster)
			Expect(err).To(HaveOccurred(), "Should reject change to immutable ephemeral field")
		})
	})
})
