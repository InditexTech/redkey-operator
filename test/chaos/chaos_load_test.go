// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package chaos

import (
	"fmt"
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Chaos Under Load (PurgeKeysOnRebalance=true)", Label("chaos", "load", "purge"), func() {
	var (
		namespace *corev1.Namespace
		k6DepName string
		rng       *rand.Rand
	)

	BeforeEach(func() {
		rng = rand.New(rand.NewSource(chaosSeed))
		GinkgoWriter.Printf("Using random seed: %d\n", chaosSeed)
		namespace = setupChaosNamespace(fmt.Sprintf("chaos-%d", GinkgoParallelProcess()), defaultPrimaries, true)
	})

	AfterEach(func() {
		teardownChaosNamespace(namespace, k6DepName)
	})

	It("survives continuous scaling and pod deletion while handling traffic", func() {
		k6DepName = runScalingChaos(rng, namespace.Name, clusterName, true)
	})

	It("recovers when operator pod is deleted during chaos", func() {
		k6DepName = runOperatorDeletionChaos(rng, namespace.Name, clusterName)
	})

	It("recovers when robin pods are deleted during chaos", func() {
		k6DepName = runRobinDeletionChaos(rng, namespace.Name, clusterName)
	})

	It("recovers from full chaos deleting operator, robin, and redis pods", func() {
		k6DepName = runFullChaos(rng, namespace.Name, clusterName)
	})
})

var _ = Describe("Chaos Under Load (PurgeKeysOnRebalance=false)", Label("chaos", "load", "nopurge"), func() {
	var (
		namespace *corev1.Namespace
		k6DepName string
		rng       *rand.Rand
	)

	BeforeEach(func() {
		rng = rand.New(rand.NewSource(chaosSeed))
		GinkgoWriter.Printf("Using random seed: %d\n", chaosSeed)
		namespace = setupChaosNamespace(fmt.Sprintf("chaos-np-%d", GinkgoParallelProcess()), defaultPrimaries, false)
	})

	AfterEach(func() {
		teardownChaosNamespace(namespace, k6DepName)
	})

	It("survives continuous scaling and pod deletion while handling traffic without purge", func() {
		k6DepName = runScalingChaos(rng, namespace.Name, clusterName, false)
	})

	It("recovers when operator pod is deleted during chaos without purge", func() {
		k6DepName = runOperatorDeletionChaos(rng, namespace.Name, clusterName)
	})

	It("recovers when robin pods are deleted during chaos without purge", func() {
		k6DepName = runRobinDeletionChaos(rng, namespace.Name, clusterName)
	})

	It("recovers from full chaos deleting operator, robin, and redis pods without purge", func() {
		k6DepName = runFullChaos(rng, namespace.Name, clusterName)
	})
})
