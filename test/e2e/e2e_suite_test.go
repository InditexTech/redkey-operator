// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/test/utils"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	// Optional Environment Variables:
	// - CERT_MANAGER_INSTALL_SKIP=true: Skips CertManager installation during test setup.
	// - OPERATOR_DEPLOY_SKIP=true: Skips operator deployment (assumes it's already deployed).
	skipCertManagerInstall = os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true"
	skipOperatorDeploy     = os.Getenv("OPERATOR_DEPLOY_SKIP") == "true"

	// isCertManagerAlreadyInstalled will be set true when CertManager CRDs be found on the cluster
	isCertManagerAlreadyInstalled = false

	// controllerImage is the operator image loaded into Kind and deployed by the E2E suite.
	controllerImage = getenvOrFallback("IMAGE_OPERATOR", "OPERATOR_IMAGE")

	// robinImage is the image used by the controller when creating Robin deployments.
	robinImage = getenvOrFallback("IMAGE_ROBIN", "ROBIN_IMAGE")

	// k8sClient is a controller-runtime client shared across all tests.
	k8sClient client.Client
)

// TestE2E runs the end-to-end (e2e) test suite for the project. These tests execute in an isolated,
// temporary environment to validate project changes with the purposed to be used in CI jobs.
// The default setup requires Kind, prebuilt operator/Robin images available locally for kind load,
// and installs CertManager.
func TestE2E(t *testing.T) {
	if controllerImage == "" {
		t.Fatal("the IMAGE_OPERATOR environment variable must be set")
	}
	if robinImage == "" {
		t.Fatal("the IMAGE_ROBIN environment variable must be set")
	}
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting redkey-operator E2E test suite\n")
	RunSpecs(t, "e2e suite")
}

func getenvOrFallback(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

var _ = BeforeSuite(func() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_ = ctx

	// Setup controller-runtime client
	By("setting up the Kubernetes client")
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(redkeyv1beta1.AddToScheme(scheme)).To(Succeed())

	cfg, err := config.GetConfig()
	Expect(err).NotTo(HaveOccurred(), "Failed to get kubeconfig")

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred(), "Failed to create Kubernetes client")

	// Setup CertManager
	if !skipCertManagerInstall {
		By("checking if cert manager is installed already")
		isCertManagerAlreadyInstalled = utils.IsCertManagerCRDsInstalled()
		if !isCertManagerAlreadyInstalled {
			_, _ = fmt.Fprintf(GinkgoWriter, "Installing CertManager...\n")
			Expect(utils.InstallCertManager()).To(Succeed(), "Failed to install CertManager")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "WARNING: CertManager is already installed. Skipping installation...\n")
		}
	}

	// Deploy operator via make deploy
	if !skipOperatorDeploy {
		By("deploying the operator via make deploy")
		cmd := exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", controllerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the operator")

		By("waiting for operator to be ready")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "pods", "-l", "control-plane=controller-manager",
				"-n", "redkey-operator", "-o", "jsonpath={.items[0].status.phase}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("Running"))
		}, 2*time.Minute, 5*time.Second).Should(Succeed(), "Operator pod did not reach Running state")
	}
})

var _ = AfterSuite(func() {
	// Undeploy operator
	if !skipOperatorDeploy {
		By("undeploying the operator")
		cmd := exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)
	}

	// Teardown CertManager
	if !skipCertManagerInstall && !isCertManagerAlreadyInstalled {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling CertManager...\n")
		utils.UninstallCertManager()
	}
})
