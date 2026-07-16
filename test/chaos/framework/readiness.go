// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

const (
	defaultChaosReadyTimeout = 10 * time.Minute
	readinessPollInterval    = 2 * time.Second
	// scaleAckTimeout bounds ScaleCluster/WaitForScaleAck polling.
	scaleAckTimeout = 5 * time.Minute
)

// WaitForChaosReady waits for the Redis cluster to be fully healthy after a fault.
// Checks: CR phase == Ready, the operator has observed the latest spec (ObservedGeneration ==
// Generation), the highest-sequence RedkeyClusterConfig has finished applying (ConfigPhase=Applied,
// Status=Ready), pod count matches spec, all pods Running, redis-cli --cluster check passes, and no
// fail/migrating states.
//
// Gating on the highest-sequence config (not just the aggregated CR phase) is essential: the
// operator's status aggregation falls back to the previous Applied config until Robin marks a newly
// created, higher-sequence config InProgress, so the CR phase can momentarily read Ready while a
// scaling/rebalance is actually still pending or in flight. Without this gate the chaos loop can
// advance to the next operation mid-rebalance.
func WaitForChaosReady(
	ctx context.Context,
	c client.Client,
	clientset kubernetes.Interface,
	namespace, clusterName string,
	timeout time.Duration,
) error {
	if timeout == 0 {
		timeout = defaultChaosReadyTimeout
	}

	var lastReason string
	err := wait.PollUntilContextTimeout(ctx, readinessPollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}

			cluster, err := GetRedkeyCluster(ctx, c, namespace, clusterName)
			if err != nil {
				if ctx.Err() == nil {
					lastReason = fmt.Sprintf("error getting cluster: %v", err)
				}
				return false, nil
			}

			if cluster.Status.Phase != redkeyv1beta1.PhaseReady {
				lastReason = fmt.Sprintf("CR phase is %q (want Ready)", cluster.Status.Phase)
				return false, nil
			}

			// The operator stamps ObservedGeneration with the spec generation it created/aggregated
			// a config for. Until it catches up to the live generation, a Ready phase still reflects
			// the *previous* spec, not the scaling change we just requested.
			if cluster.Status.ObservedGeneration != cluster.Generation {
				lastReason = fmt.Sprintf("observedGeneration %d != generation %d (operator has not picked up the latest spec yet)",
					cluster.Status.ObservedGeneration, cluster.Generation)
				return false, nil
			}

			// Authoritative gate: the highest-sequence config must itself report Applied/Ready. This
			// closes the window where the aggregated CR phase reads Ready from an older Applied config
			// while a newer one is still pending or rebalancing.
			if settled, reason := highestConfigSettled(ctx, c, namespace, clusterName); !settled {
				if ctx.Err() == nil {
					lastReason = reason
				}
				return false, nil
			}

			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: RedisPodsSelector(clusterName),
			})
			if err != nil {
				if ctx.Err() == nil {
					lastReason = fmt.Sprintf("error listing pods: %v", err)
				}
				return false, nil
			}

			if len(pods.Items) == 0 {
				lastReason = "pod count is 0"
				return false, nil
			}

			// Guard against a false-positive Ready while the operator/Robin hasn't yet
			// processed a spec.primaries change (race between CR update and reconcile).
			expected := cluster.Spec.NodesNeeded()
			if len(pods.Items) != expected {
				lastReason = fmt.Sprintf("pod count %d != expected %d (spec.primaries=%d)",
					len(pods.Items), expected, cluster.Spec.Primaries)
				return false, nil
			}

			for _, pod := range pods.Items {
				if pod.Status.Phase != corev1.PodRunning {
					lastReason = fmt.Sprintf("pod %s phase is %s (want Running)", pod.Name, pod.Status.Phase)
					return false, nil
				}
				if ctx.Err() != nil {
					return false, nil
				}
				if !clusterCheckPasses(ctx, namespace, pod.Name) {
					if ctx.Err() == nil {
						lastReason = fmt.Sprintf("redis-cli --cluster check failed on pod %s", pod.Name)
					}
					return false, nil
				}
				if clusterNodesHasFailure(ctx, namespace, pod.Name) {
					if ctx.Err() == nil {
						lastReason = fmt.Sprintf("cluster nodes failure detected on pod %s", pod.Name)
					}
					return false, nil
				}
			}
			return true, nil
		})
	if err != nil && lastReason != "" {
		return fmt.Errorf("WaitForChaosReady(%s/%s): last check: %s: %w", namespace, clusterName, lastReason, err)
	}
	return err
}

// highestConfigSettled reports whether the highest-sequence RedkeyClusterConfig has finished
// applying (ConfigPhase=Applied and operational Status=Ready). The aggregated RedkeyCluster status
// can momentarily read Ready while a freshly-created, higher-sequence config has not been picked up
// yet, because the operator's aggregation falls back to the previous Applied config until Robin
// marks the new one InProgress. Gating on the highest-sequence config directly closes that window so
// the chaos loop never advances mid-operation.
func highestConfigSettled(ctx context.Context, c client.Client, namespace, clusterName string) (bool, string) {
	var configs redkeyv1beta1.RedkeyClusterConfigList
	if err := c.List(ctx, &configs,
		client.InNamespace(namespace),
		client.MatchingLabels{clusterLabel: clusterName},
	); err != nil {
		if ctx.Err() != nil {
			return false, ""
		}
		return false, fmt.Sprintf("error listing configs: %v", err)
	}
	if len(configs.Items) == 0 {
		return false, "no RedkeyClusterConfig found yet"
	}

	highest := &configs.Items[0]
	for i := range configs.Items {
		if configs.Items[i].Spec.Sequence > highest.Spec.Sequence {
			highest = &configs.Items[i]
		}
	}

	if highest.Status.ConfigPhase != redkeyv1beta1.ConfigPhaseApplied {
		return false, fmt.Sprintf("highest config %s (seq %d) ConfigPhase=%q (want Applied)",
			highest.Name, highest.Spec.Sequence, highest.Status.ConfigPhase)
	}
	if highest.Status.Status != redkeyv1beta1.ClusterStatusReady {
		return false, fmt.Sprintf("highest config %s (seq %d) Status=%q (want Ready)",
			highest.Name, highest.Spec.Sequence, highest.Status.Status)
	}
	return true, ""
}

// clusterCheckPasses runs redis-cli --cluster check and returns true if it succeeds.
func clusterCheckPasses(ctx context.Context, namespace, podName string) bool {
	stdout, _, err := RemoteCommand(ctx, namespace, podName, "redis-cli --cluster check localhost:6379")
	if err != nil {
		return false
	}
	return !strings.Contains(stdout, "[ERR]")
}

// clusterNodesHasFailure checks if any node is in fail state or has migrating/importing slots.
func clusterNodesHasFailure(ctx context.Context, namespace, podName string) bool {
	stdout, _, err := RemoteCommand(ctx, namespace, podName, "redis-cli cluster nodes")
	if err != nil {
		return true
	}
	return strings.Contains(stdout, "fail") || strings.Contains(stdout, "->") || strings.Contains(stdout, "<-")
}

// AssertAllSlotsAssigned verifies that all 16384 slots are assigned.
func AssertAllSlotsAssigned(ctx context.Context, clientset kubernetes.Interface, namespace, clusterName string) error {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: RedisPodsSelector(clusterName),
	})
	if err != nil {
		return err
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no redis pods found")
	}

	stdout, _, err := RemoteCommand(ctx, namespace, pods.Items[0].Name, "redis-cli cluster info")
	if err != nil {
		return fmt.Errorf("failed to get cluster info: %w", err)
	}
	if !strings.Contains(stdout, "cluster_slots_ok:16384") {
		return fmt.Errorf("not all slots assigned: %s", stdout)
	}
	return nil
}

// AssertNoNodesInFailState verifies no nodes are in fail state.
func AssertNoNodesInFailState(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, clusterName string,
) error {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: RedisPodsSelector(clusterName),
	})
	if err != nil {
		return err
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no redis pods found")
	}

	for _, pod := range pods.Items {
		stdout, _, err := RemoteCommand(ctx, namespace, pod.Name, "redis-cli cluster nodes")
		if err != nil {
			return fmt.Errorf("failed to get cluster nodes from %s: %w", pod.Name, err)
		}
		if strings.Contains(stdout, "fail") {
			return fmt.Errorf("node in fail state detected in pod %s: %s", pod.Name, stdout)
		}
	}
	return nil
}
