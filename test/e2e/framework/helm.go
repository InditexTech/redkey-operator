// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"fmt"
	"os/exec"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck

	"github.com/inditextech/redkeyoperator/test/utils"
)

// HelmInstall runs `helm install` with the given release name, chart path, and values.
func HelmInstall(releaseName, chartPath, namespace string, values map[string]string) error {
	args := []string{
		"install", releaseName, chartPath,
		"--namespace", namespace, "--create-namespace",
		"--wait", "--timeout", "5m",
	}
	for k, v := range values {
		args = append(args, "--set", fmt.Sprintf("%s=%s", k, v))
	}

	cmd := exec.Command("helm", args...)
	_, err := utils.Run(cmd)
	if err != nil {
		return fmt.Errorf("helm install %s: %w", releaseName, err)
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "Helm release %s installed in namespace %s\n", releaseName, namespace)
	return nil
}

// HelmUninstall runs `helm uninstall` for the given release.
func HelmUninstall(releaseName, namespace string) error {
	cmd := exec.Command("helm", "uninstall", releaseName, "--namespace", namespace)
	_, err := utils.Run(cmd)
	if err != nil {
		return fmt.Errorf("helm uninstall %s: %w", releaseName, err)
	}
	_, _ = fmt.Fprintf(GinkgoWriter, "Helm release %s uninstalled from namespace %s\n", releaseName, namespace)
	return nil
}

// HelmUpgrade runs `helm upgrade --install` with the given values.
func HelmUpgrade(releaseName, chartPath, namespace string, values map[string]string) error {
	args := []string{"upgrade", "--install", releaseName, chartPath, "--namespace", namespace, "--wait", "--timeout", "5m"}
	for k, v := range values {
		args = append(args, "--set", fmt.Sprintf("%s=%s", k, v))
	}

	cmd := exec.Command("helm", args...)
	_, err := utils.Run(cmd)
	if err != nil {
		return fmt.Errorf("helm upgrade %s: %w", releaseName, err)
	}
	return nil
}
