// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

// Package framework provides helper functions for the Redkey chaos test suite.
// It mirrors the structure of test/e2e/framework but adds chaos-specific
// helpers: per-namespace operator deployment, fault injection (pod deletion,
// topology corruption) and k6 load generation.
package framework

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/homedir"
)

// Label keys used by the operator/Robin on the resources they manage.
const (
	clusterLabel   = "redkey.inditex.dev/cluster"
	componentLabel = "redkey.inditex.dev/component"

	// operatorLabelSelector matches the per-namespace operator Deployment that the
	// chaos suite deploys. It must stay in sync with the labels set in
	// operator_setup.go.
	operatorLabelSelector = "control-plane=redkey-operator"
)

// RedisPodsSelector returns the label selector for Redis pods in a cluster.
func RedisPodsSelector(clusterName string) string {
	return fmt.Sprintf("%s=%s,%s=redis", clusterLabel, clusterName, componentLabel)
}

// RobinPodsSelector returns the label selector for Robin pods in a cluster.
func RobinPodsSelector(clusterName string) string {
	return fmt.Sprintf("%s=%s,%s=robin", clusterLabel, clusterName, componentLabel)
}

// OperatorPodsSelector returns the label selector for the per-namespace operator pods.
func OperatorPodsSelector() string {
	return operatorLabelSelector
}

// Cached REST config used by RemoteCommand and the suite clients.
var (
	cachedConfig *rest.Config
	configOnce   sync.Once
	configErr    error
)

// RESTConfig returns a cached REST config built from KUBECONFIG (or ~/.kube/config).
func RESTConfig() (*rest.Config, error) {
	configOnce.Do(func() {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = filepath.Join(homedir.HomeDir(), ".kube", "config")
		}
		cachedConfig, configErr = clientcmd.BuildConfigFromFlags("", kubeconfig)
	})
	return cachedConfig, configErr
}

// RemoteCommand executes a command in a pod and returns stdout, stderr and error.
func RemoteCommand(ctx context.Context, namespace, podName, command string) (string, string, error) {
	config, err := RESTConfig()
	if err != nil {
		return "", "", fmt.Errorf("get config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return "", "", fmt.Errorf("create clientset: %w", err)
	}

	buf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	request := clientset.CoreV1().RESTClient().
		Post().
		Namespace(namespace).
		Resource("pods").
		Name(podName).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
			Command: []string{"/bin/sh", "-c", command},
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(config, "POST", request.URL())
	if err != nil {
		return "", "", fmt.Errorf("create executor: %w", err)
	}

	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: buf,
		Stderr: errBuf,
		Tty:    false,
	})
	if err != nil {
		return buf.String(), errBuf.String(), err
	}
	return buf.String(), errBuf.String(), nil
}

// GetPodLogs returns the last N lines of logs from the first pod matching the selector.
func GetPodLogs(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, labelSelector string,
	tailLines int64,
) (string, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", err
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found matching %s", labelSelector)
	}

	pod := pods.Items[0]
	opts := &corev1.PodLogOptions{TailLines: &tailLines}

	req := clientset.CoreV1().Pods(namespace).GetLogs(pod.Name, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("stream logs from %s: %w", pod.Name, err)
	}
	defer func() { _ = stream.Close() }()

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, stream); err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}

	return buf.String(), nil
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
