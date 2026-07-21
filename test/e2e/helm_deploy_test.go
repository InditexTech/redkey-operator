// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/test/e2e/framework"
	"github.com/inditextech/redkeyoperator/test/utils"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Helm Deployment", Ordered, Label("helm"), func() {
	var (
		suiteCtx  context.Context
		ctx       context.Context
		ns        *corev1.Namespace
		clusterNs string
	)

	framework.SetupSpecContexts(&suiteCtx, &ctx, 15*time.Minute)

	const (
		helmReleaseName = "e2e-helm-cluster"
		clusterName     = helmReleaseName
	)

	BeforeAll(func() {
		By("creating a test namespace")
		var err error
		ns, err = framework.CreateNamespace(suiteCtx, k8sClient, "e2e-helm")
		Expect(err).NotTo(HaveOccurred())
		clusterNs = ns.Name
		_, _ = fmt.Fprintf(GinkgoWriter, "Using namespace: %s\n", clusterNs)
	})

	AfterAll(func() {
		By("uninstalling Helm release")
		_ = framework.HelmUninstall(helmReleaseName, clusterNs)

		By("cleaning up the test namespace")
		if ns != nil {
			_ = framework.DeleteNamespace(suiteCtx, k8sClient, ns)
		}
	})

	AfterEach(func() {
		framework.CollectDebugInfoOnFailure(k8sClient, clusterNs)
	})

	It("should deploy a cluster using the redkey-cluster Helm chart and reach Ready", func() {
		projectDir, err := utils.GetProjectDir()
		Expect(err).NotTo(HaveOccurred())

		chartPath := filepath.Join(projectDir, "charts", "redkey-cluster")

		By("installing the redkey-cluster Helm chart")
		values := map[string]string{
			"cluster.primaries":          "3",
			"cluster.replicasPerPrimary": "0",
			"cluster.ephemeral":          "true",
			"cluster.image":              framework.GetRedisImage(),
			"robin.image":                robinImage,
			"robin.imagePullPolicy":      "IfNotPresent",
		}
		err = framework.HelmInstall(helmReleaseName, chartPath, clusterNs, values)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the cluster to reach Ready phase")
		cluster, err := framework.WaitForClusterReady(ctx, k8sClient,
			types.NamespacedName{Name: clusterName, Namespace: clusterNs},
			framework.CreationTimeout)
		Expect(err).NotTo(HaveOccurred())
		Expect(cluster.Status.Phase).To(Equal(redkeyv1beta1.PhaseReady))

		By("verifying pods are running")
		expectedPods := 3
		err = framework.WaitForPodsReady(ctx, k8sClient, clusterNs,
			framework.RedisPodLabels(clusterName),
			expectedPods, framework.CreationTimeout)
		Expect(err).NotTo(HaveOccurred())

		By("verifying the Redis cluster is healthy")
		podNames, err := framework.GetRedisPodNames(ctx, k8sClient, clusterName, clusterNs, expectedPods)
		Expect(err).NotTo(HaveOccurred())

		err = framework.VerifyClusterHealthy(clusterNs, podNames[0], expectedPods)
		Expect(err).NotTo(HaveOccurred())

		By("verifying no PVCs exist (ephemeral)")
		pvcs := &corev1.PersistentVolumeClaimList{}
		err = k8sClient.List(ctx, pvcs, client.InNamespace(clusterNs),
			client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName})
		Expect(err).NotTo(HaveOccurred())
		Expect(pvcs.Items).To(BeEmpty())
	})
})
