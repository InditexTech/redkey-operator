// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const robinScaleTimeout = 2 * time.Minute

// DeleteRandomRedisPods deletes N random redis pods from the cluster (never all of them).
func DeleteRandomRedisPods(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, clusterName string,
	count int,
	rng *rand.Rand,
) ([]string, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: RedisPodsSelector(clusterName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list redis pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no redis pods found to delete")
	}

	maxDelete := len(pods.Items) - 1
	if maxDelete < 1 {
		maxDelete = 1
	}
	if count > maxDelete {
		count = maxDelete
	}

	indices := rng.Perm(len(pods.Items))

	var deleted []string
	for i := 0; i < count && i < len(indices); i++ {
		pod := pods.Items[indices[i]]
		err := clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return deleted, fmt.Errorf("failed to delete pod %s: %w", pod.Name, err)
		}
		deleted = append(deleted, pod.Name)
	}
	return deleted, nil
}

// DeleteRobinPods requests deletion of all robin pods for the cluster.
func DeleteRobinPods(ctx context.Context, clientset kubernetes.Interface, namespace, clusterName string) error {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: RobinPodsSelector(clusterName),
	})
	if err != nil {
		return fmt.Errorf("failed to list robin pods: %w", err)
	}
	for _, pod := range pods.Items {
		err := clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete robin pod %s: %w", pod.Name, err)
		}
	}
	return nil
}

// ScaleRobinDown scales the robin deployment to 0 replicas and waits for its pods to disappear.
func ScaleRobinDown(ctx context.Context, clientset kubernetes.Interface, namespace, clusterName string) error {
	return scaleRobinDeploymentNative(ctx, clientset, namespace, clusterName, 0)
}

// ScaleRobinUp scales the robin deployment to 1 replica and waits for it to be ready.
func ScaleRobinUp(ctx context.Context, clientset kubernetes.Interface, namespace, clusterName string) error {
	return scaleRobinDeploymentNative(ctx, clientset, namespace, clusterName, 1)
}

func scaleRobinDeploymentNative(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, clusterName string,
	replicas int32,
) error {
	robinDepName := clusterName + "-robin"

	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, robinDepName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) && replicas == 0 {
			return nil
		}
		return fmt.Errorf("failed to get robin deployment %s: %w", robinDepName, err)
	}

	dep.Spec.Replicas = ptr.To(replicas)
	if _, err := clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to scale robin deployment %s: %w", robinDepName, err)
	}

	if replicas == 0 {
		return wait.PollUntilContextTimeout(ctx, 2*time.Second, robinScaleTimeout, true,
			func(ctx context.Context) (bool, error) {
				dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, robinDepName, metav1.GetOptions{})
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

	return wait.PollUntilContextTimeout(ctx, 2*time.Second, robinScaleTimeout, true,
		func(ctx context.Context) (bool, error) {
			dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, robinDepName, metav1.GetOptions{})
			if err != nil {
				return false, nil
			}
			return dep.Status.ReadyReplicas >= replicas, nil
		})
}

// CorruptSlotOwnership removes a slot from all nodes and reassigns it inconsistently to two
// different nodes. Requires the operator and robin to be scaled down first so they do not heal
// the corruption before it is observed.
func CorruptSlotOwnership(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, clusterName string,
	slot int,
) error {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: RedisPodsSelector(clusterName),
	})
	if err != nil {
		return fmt.Errorf("failed to list redis pods: %w", err)
	}
	if len(pods.Items) < 2 {
		return fmt.Errorf("need at least 2 pods for slot corruption")
	}

	nodeIDs := make([]string, 0, len(pods.Items))
	for _, pod := range pods.Items {
		stdout, _, err := RemoteCommand(ctx, namespace, pod.Name,
			"redis-cli cluster nodes | grep myself | awk '{ print $1 }'")
		if err != nil {
			return fmt.Errorf("failed to get node ID from %s: %w", pod.Name, err)
		}
		nodeIDs = append(nodeIDs, trimNewline(stdout))
	}

	for _, pod := range pods.Items {
		stdout, stderr, err := RemoteCommand(ctx, namespace, pod.Name, fmt.Sprintf("redis-cli cluster delslots %d", slot))
		if err != nil {
			return fmt.Errorf("failed to delslots %d on %s: %w (stdout=%q stderr=%q)",
				slot, pod.Name, err, trimNewline(stdout), trimNewline(stderr))
		}
	}

	if _, _, err = RemoteCommand(ctx, namespace, pods.Items[0].Name,
		fmt.Sprintf("redis-cli cluster setslot %d node %s", slot, nodeIDs[0])); err != nil {
		return fmt.Errorf("failed to setslot on first node: %w", err)
	}
	if _, _, err = RemoteCommand(ctx, namespace, pods.Items[1].Name,
		fmt.Sprintf("redis-cli cluster setslot %d node %s", slot, nodeIDs[1])); err != nil {
		return fmt.Errorf("failed to setslot on second node: %w", err)
	}
	return nil
}

// SetSlotMigrating puts a slot in migrating/importing state across two nodes, simulating an
// interrupted resharding. Requires operator and robin scaled down first.
func SetSlotMigrating(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, clusterName string,
	slot int,
) error {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: RedisPodsSelector(clusterName),
	})
	if err != nil {
		return fmt.Errorf("failed to list redis pods: %w", err)
	}
	if len(pods.Items) < 2 {
		return fmt.Errorf("need at least 2 pods for slot migration corruption")
	}

	nodeIDs := make([]string, 0, len(pods.Items))
	for _, pod := range pods.Items {
		stdout, _, err := RemoteCommand(ctx, namespace, pod.Name,
			"redis-cli cluster nodes | grep myself | awk '{ print $1 }'")
		if err != nil {
			return fmt.Errorf("failed to get node ID from %s: %w", pod.Name, err)
		}
		nodeIDs = append(nodeIDs, trimNewline(stdout))
	}

	if _, _, err = RemoteCommand(ctx, namespace, pods.Items[0].Name,
		fmt.Sprintf("redis-cli cluster setslot %d migrating %s", slot, nodeIDs[1])); err != nil {
		return fmt.Errorf("failed to set slot migrating: %w", err)
	}
	if _, _, err = RemoteCommand(ctx, namespace, pods.Items[1].Name,
		fmt.Sprintf("redis-cli cluster setslot %d importing %s", slot, nodeIDs[0])); err != nil {
		return fmt.Errorf("failed to set slot importing: %w", err)
	}
	return nil
}

// ForcePrimaryToReplica flushes a primary's slots and makes it replicate another primary,
// corrupting the topology. Requires operator and robin scaled down first.
func ForcePrimaryToReplica(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, clusterName, podName string,
) error {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: RedisPodsSelector(clusterName),
	})
	if err != nil {
		return fmt.Errorf("failed to list redis pods: %w", err)
	}

	var targetNodeID string
	for _, pod := range pods.Items {
		if pod.Name == podName {
			continue
		}
		stdout, _, err := RemoteCommand(ctx, namespace, pod.Name,
			"redis-cli cluster nodes | grep myself | awk '{ print $1 }'")
		if err != nil {
			continue
		}
		targetNodeID = trimNewline(stdout)
		break
	}
	if targetNodeID == "" {
		return fmt.Errorf("no target node found for replication")
	}

	stdout, stderr, err := RemoteCommand(ctx, namespace, podName, "redis-cli cluster flushslots")
	if err != nil {
		return fmt.Errorf("failed to flushslots on %s: %w (stdout=%q stderr=%q)",
			podName, err, trimNewline(stdout), trimNewline(stderr))
	}
	if _, _, err = RemoteCommand(ctx, namespace, podName,
		fmt.Sprintf("redis-cli cluster replicate %s", targetNodeID)); err != nil {
		return fmt.Errorf("failed to replicate: %w", err)
	}
	return nil
}
