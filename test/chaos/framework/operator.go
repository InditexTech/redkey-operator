// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const operatorScaleTimeout = 2 * time.Minute

// ScaleOperatorDown scales the per-namespace operator deployment to 0 replicas and waits
// for its pods to terminate.
func ScaleOperatorDown(ctx context.Context, clientset kubernetes.Interface, namespace string) error {
	return scaleDeploymentNative(ctx, clientset, namespace, operatorName, 0)
}

// ScaleOperatorUp scales the per-namespace operator deployment to 1 replica and waits for readiness.
func ScaleOperatorUp(ctx context.Context, clientset kubernetes.Interface, namespace string) error {
	return scaleDeploymentNative(ctx, clientset, namespace, operatorName, 1)
}

// DeleteOperatorPods requests deletion of the current operator pods in the namespace.
func DeleteOperatorPods(ctx context.Context, clientset kubernetes.Interface, namespace string) error {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: OperatorPodsSelector(),
	})
	if err != nil {
		return fmt.Errorf("failed to list operator pods: %w", err)
	}
	for _, pod := range pods.Items {
		err := clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete operator pod %s: %w", pod.Name, err)
		}
	}
	return nil
}

func scaleDeploymentNative(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, name string,
	replicas int32,
) error {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment %s: %w", name, err)
	}

	dep.Spec.Replicas = ptr.To(replicas)
	if _, err := clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to scale deployment %s: %w", name, err)
	}

	if replicas == 0 {
		return waitForDeploymentScaleDownNative(ctx, clientset, namespace, name)
	}
	return waitForDeploymentReadyNative(ctx, clientset, namespace, name, replicas)
}

func waitForDeploymentScaleDownNative(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, name string,
) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, operatorScaleTimeout, true,
		func(ctx context.Context) (bool, error) {
			dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					return true, nil
				}
				return false, nil
			}
			selector, err := deploymentPodSelector(dep)
			if err != nil {
				return false, err
			}
			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return false, nil
			}
			return dep.Status.Replicas == 0 && dep.Status.ReadyReplicas == 0 && len(pods.Items) == 0, nil
		})
}

func waitForDeploymentReadyNative(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, name string,
	replicas int32,
) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, operatorScaleTimeout, true,
		func(ctx context.Context) (bool, error) {
			dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			return dep.Status.ReadyReplicas >= replicas, nil
		})
}

func deploymentPodSelector(dep *appsv1.Deployment) (string, error) {
	selector, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return "", fmt.Errorf("deployment %s has invalid selector: %w", dep.Name, err)
	}
	return selector.String(), nil
}

// WaitForOperatorAvailable waits until the operator deployment reports at least one available replica.
func WaitForOperatorAvailable(ctx context.Context, clientset kubernetes.Interface, namespace string) error {
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, operatorScaleTimeout, true,
		func(ctx context.Context) (bool, error) {
			dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, operatorName, metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			return dep.Status.AvailableReplicas >= 1, nil
		})
}
