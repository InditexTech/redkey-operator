// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

// Package chaos contains the Redkey operator chaos test suite. The suite deploys a dedicated,
// namespace-scoped operator per chaos namespace (configured with --watch-namespaces) so that
// parallel scenarios are fully isolated, then injects faults (pod deletions, topology corruption)
// under sustained k6 load and verifies the cluster heals.
package chaos

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	redkeyv1beta1 "github.com/inditextech/redkey-operator/api/v1beta1"
	"github.com/inditextech/redkey-operator/test/chaos/framework"
)

var (
	k8sClient           client.Client
	k8sClientset        kubernetes.Interface
	ctx                 context.Context
	cancel              context.CancelFunc
	chaosIterations     int
	chaosSeed           int64
	chaosReadyTimeout   = 15 * time.Minute
	skipDeleteNamespace bool
)

func TestChaos(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Redkey Operator Chaos Test Suite", Label("chaos"))
}

// SynchronizedBeforeSuite runs cluster-level setup once across all parallel processes (process 1),
// then every process builds its own Kubernetes clients.
var _ = SynchronizedBeforeSuite(
	func() []byte {
		// CRDs are installed cluster-wide by the Makefile chaos target before the suite runs.
		// Nothing process-global is required here.
		return nil
	},
	func(_ []byte) {
		By("building Kubernetes clients")

		cfg, err := framework.RESTConfig()
		Expect(err).NotTo(HaveOccurred(), "failed to load kubeconfig")
		Expect(cfg).NotTo(BeNil())

		scheme := clientgoscheme.Scheme
		Expect(redkeyv1beta1.AddToScheme(scheme)).To(Succeed())

		k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred(), "failed to create controller-runtime client")
		Expect(k8sClient).NotTo(BeNil())

		k8sClientset, err = kubernetes.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred(), "failed to create Kubernetes clientset")
		Expect(k8sClientset).NotTo(BeNil())

		ctx, cancel = context.WithCancel(context.Background())

		chaosIterations = framework.GetChaosIterations()

		if seedStr := os.Getenv("CHAOS_SEED"); seedStr != "" {
			if seed, perr := strconv.ParseInt(seedStr, 10, 64); perr == nil {
				chaosSeed = seed
			} else {
				chaosSeed = GinkgoRandomSeed()
			}
		} else {
			chaosSeed = GinkgoRandomSeed()
		}

		skipDeleteNamespace = framework.KeepNamespaceOnFailure()

		GinkgoWriter.Printf("Chaos test configuration: iterations=%d, seed=%d, skipDeleteNamespace=%v\n",
			chaosIterations, chaosSeed, skipDeleteNamespace)
	},
)

// SynchronizedAfterSuite cancels the shared context after all processes finish.
var _ = SynchronizedAfterSuite(
	func() {
		if cancel != nil {
			cancel()
		}
	},
	func() {},
)
