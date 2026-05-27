// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/homedir"
)

// GetClientSet returns a Kubernetes clientset and rest config from KUBECONFIG or default.
func GetClientSet() (*kubernetes.Clientset, *rest.Config, error) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = filepath.Join(homedir.HomeDir(), ".kube", "config")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("error loading kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating kubernetes clientset: %w", err)
	}

	return clientset, config, nil
}

// ExecInPod executes a command in a pod and returns stdout and stderr.
func ExecInPod(namespace, podName, command string) (string, string, error) {
	return ExecInPodContainer(namespace, podName, "", command)
}

// ExecInPodContainer executes a command in a specific container of a pod.
func ExecInPodContainer(namespace, podName, container, command string) (string, string, error) {
	ctx := context.Background()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	executor, err := createExecutor(namespace, podName, container, command)
	if err != nil {
		return "", "", fmt.Errorf("creating executor: %w", err)
	}

	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    false,
	})
	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf(
			"exec in pod %s/%s: %w (stderr: %s)", namespace, podName, err, stderr.String())
	}

	return stdout.String(), stderr.String(), nil
}

func createExecutor(namespace, podName, container, command string) (remotecommand.Executor, error) {
	_, config, err := GetClientSet()
	if err != nil {
		return nil, err
	}

	coreClient, err := typedcorev1.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating core/v1 client: %w", err)
	}

	execOpts := &corev1.PodExecOptions{
		TypeMeta: metav1.TypeMeta{},
		Stdout:   true,
		Stderr:   true,
		TTY:      false,
		Command:  []string{"/bin/sh", "-c", command},
	}
	if container != "" {
		execOpts.Container = container
	}

	request := coreClient.RESTClient().
		Post().
		Namespace(namespace).
		Resource("pods").
		Name(podName).
		SubResource("exec").
		VersionedParams(execOpts, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(config, "POST", request.URL())
	if err != nil {
		return nil, fmt.Errorf("creating SPDY executor: %w", err)
	}

	return executor, nil
}
