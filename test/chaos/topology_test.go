// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package chaos

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/inditextech/redkeyoperator/test/chaos/framework"
)

// Topology Corruption Recovery exercises the new architecture where Robin (not the operator) owns
// Redis topology management via its HealthReconciler. Each scenario stops both the operator and
// Robin, injects a topology fault directly with redis-cli, then brings Robin (and the operator)
// back and asserts that Robin heals the cluster: all slots assigned and no nodes in fail state.
var _ = Describe("Topology Corruption Recovery", Label("chaos", "topology"), func() {
	var namespace *corev1.Namespace

	BeforeEach(func() {
		namespace = setupChaosNamespace(fmt.Sprintf("chaos-topo-%d", GinkgoParallelProcess()), defaultPrimaries, true)
	})

	AfterEach(func() {
		teardownChaosNamespace(namespace, "")
	})

	It("heals slot ownership conflicts when operator and robin restart", func() {
		By("scaling operator to 0")
		Expect(framework.ScaleOperatorDown(ctx, k8sClientset, namespace.Name)).To(Succeed())

		By("scaling robin to 0")
		Expect(framework.ScaleRobinDown(ctx, k8sClientset, namespace.Name, clusterName)).To(Succeed())

		By("corrupting slot ownership via redis-cli")
		Expect(framework.CorruptSlotOwnership(ctx, k8sClientset, namespace.Name, clusterName, 0)).To(Succeed())

		By("scaling robin to 1")
		Expect(framework.ScaleRobinUp(ctx, k8sClientset, namespace.Name, clusterName)).To(Succeed())

		By("scaling operator to 1")
		Expect(framework.ScaleOperatorUp(ctx, k8sClientset, namespace.Name)).To(Succeed())

		By("waiting for Robin to heal the cluster")
		verifyClusterHealthy(namespace.Name, clusterName)
	})

	It("recovers from mid-migration slots when operator and robin restart", func() {
		By("scaling operator to 0")
		Expect(framework.ScaleOperatorDown(ctx, k8sClientset, namespace.Name)).To(Succeed())

		By("scaling robin to 0")
		Expect(framework.ScaleRobinDown(ctx, k8sClientset, namespace.Name, clusterName)).To(Succeed())

		By("setting slot to migrating state via redis-cli")
		Expect(framework.SetSlotMigrating(ctx, k8sClientset, namespace.Name, clusterName, 100)).To(Succeed())

		By("scaling robin to 1")
		Expect(framework.ScaleRobinUp(ctx, k8sClientset, namespace.Name, clusterName)).To(Succeed())

		By("scaling operator to 1")
		Expect(framework.ScaleOperatorUp(ctx, k8sClientset, namespace.Name)).To(Succeed())

		By("waiting for Robin to heal the cluster")
		verifyClusterHealthy(namespace.Name, clusterName)
	})

	It("recovers from forced primary to replica demotion", func() {
		targetPod := clusterName + "-0"

		By("scaling operator to 0")
		Expect(framework.ScaleOperatorDown(ctx, k8sClientset, namespace.Name)).To(Succeed())

		By("scaling robin to 0")
		Expect(framework.ScaleRobinDown(ctx, k8sClientset, namespace.Name, clusterName)).To(Succeed())

		By("forcing primary to become replica via redis-cli")
		Expect(framework.ForcePrimaryToReplica(ctx, k8sClientset, namespace.Name, clusterName, targetPod)).To(Succeed())

		By("scaling robin to 1")
		Expect(framework.ScaleRobinUp(ctx, k8sClientset, namespace.Name, clusterName)).To(Succeed())

		By("scaling operator to 1")
		Expect(framework.ScaleOperatorUp(ctx, k8sClientset, namespace.Name)).To(Succeed())

		By("waiting for Robin to heal the cluster")
		verifyClusterHealthy(namespace.Name, clusterName)
	})
})
