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
	"k8s.io/apimachinery/pkg/api/meta"
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
	// requiredStableChecks is the number of consecutive clean polls the final topology verification
	// demands before declaring the cluster quiescent. Robin marks a scaling config Ready before its
	// health-reconciler finishes balancing slots, so a resharding can start moments after the
	// cluster first looks ready; requiring sustained cleanliness closes that window.
	requiredStableChecks = 3
)

// WaitForChaosReady waits for the Redis cluster to be fully healthy after a fault.
// Checks: CR phase == Ready, the operator has observed the latest spec (ObservedGeneration ==
// Generation), the highest-sequence RedkeyConfig has finished applying (ConfigPhase=Applied,
// Status=Ready), pod count matches spec, all pods Running, and the aggregated Healthy condition is
// True (Robin's own health assessment: membership, slot coverage, balance and cluster-check).
//
// Gating on the highest-sequence config (not just the aggregated CR phase) is essential: the
// operator's status aggregation falls back to the previous Applied config until Robin marks a newly
// created, higher-sequence config InProgress, so the CR phase can momentarily read Ready while a
// scaling/rebalance is actually still pending or in flight. Without this gate the chaos loop can
// advance to the next operation mid-rebalance.
//
// Gating on the Healthy condition (rather than execing redis-cli per pod) uses the product's own
// health signal: it is reported False (or Unknown while an operation is in progress) whenever Robin
// is still healing or rebalancing an applied cluster, so readiness is never declared mid-rebalance.
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
			ready, reason := chaosReadyState(ctx, c, clientset, namespace, clusterName)
			if !ready {
				if ctx.Err() == nil {
					lastReason = reason
				}
				return false, nil
			}
			return true, nil
		})
	if err != nil && lastReason != "" {
		return fmt.Errorf("WaitForChaosReady(%s/%s): last check: %s: %w", namespace, clusterName, lastReason, err)
	}
	return err
}

// chaosReadyState performs one evaluation of the chaos-readiness gates and reports whether the
// cluster is fully converged, along with a human-readable reason when it is not. It is the shared
// core of WaitForChaosReady (which polls it until it passes or times out) and IsChaosConverged
// (which samples it once). The gates are, in order: CR phase Ready, the operator has observed the
// latest spec (ObservedGeneration == Generation), the highest-sequence config is Applied/Ready, the
// pod count matches spec, every pod is Running, and the aggregated Healthy condition is True.
func chaosReadyState(
	ctx context.Context,
	c client.Client,
	clientset kubernetes.Interface,
	namespace, clusterName string,
) (bool, string) {
	cluster, err := GetRedkey(ctx, c, namespace, clusterName)
	if err != nil {
		return false, fmt.Sprintf("error getting cluster: %v", err)
	}

	if cluster.Status.Phase != redkeyv1beta1.PhaseReady {
		return false, fmt.Sprintf("CR phase is %q (want Ready)", cluster.Status.Phase)
	}

	// The operator stamps ObservedGeneration with the spec generation it created/aggregated a config
	// for. Until it catches up to the live generation, a Ready phase still reflects the *previous*
	// spec, not the scaling change we just requested.
	if cluster.Status.ObservedGeneration != cluster.Generation {
		return false, fmt.Sprintf("observedGeneration %d != generation %d (operator has not picked up the latest spec yet)",
			cluster.Status.ObservedGeneration, cluster.Generation)
	}

	// Authoritative gate: the highest-sequence config must itself report Applied/Ready. This closes
	// the window where the aggregated CR phase reads Ready from an older Applied config while a newer
	// one is still pending or rebalancing.
	if settled, reason := highestConfigSettled(ctx, c, namespace, clusterName); !settled {
		return false, reason
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: RedisPodsSelector(clusterName),
	})
	if err != nil {
		return false, fmt.Sprintf("error listing pods: %v", err)
	}

	if len(pods.Items) == 0 {
		return false, "pod count is 0"
	}

	// Guard against a false-positive Ready while the operator/Robin hasn't yet processed a
	// spec.primaries change (race between CR update and reconcile).
	expected := cluster.Spec.NodesNeeded()
	if len(pods.Items) != expected {
		return false, fmt.Sprintf("pod count %d != expected %d (spec.primaries=%d)",
			len(pods.Items), expected, cluster.Spec.Primaries)
	}

	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			return false, fmt.Sprintf("pod %s phase is %s (want Running)", pod.Name, pod.Status.Phase)
		}
	}

	// Gate on Robin's own health assessment, surfaced as the aggregated Healthy condition. It rolls
	// up membership, slot coverage, balance and cluster-check, and reads False (or Unknown while an
	// operation is in progress) whenever Robin is still healing or rebalancing an applied cluster.
	healthy := meta.FindStatusCondition(cluster.Status.Conditions, redkeyv1beta1.ConditionHealthy)
	if healthy == nil {
		return false, "Healthy condition not present yet"
	}
	if healthy.Status != metav1.ConditionTrue {
		return false, fmt.Sprintf("Healthy condition is %q (reason %q)", healthy.Status, healthy.Reason)
	}
	return true, ""
}

// IsChaosConverged samples the chaos-readiness gates once and reports whether the cluster is fully
// converged right now (same gates as WaitForChaosReady). Unlike WaitForChaosReady it never blocks,
// so callers can use it to short-circuit a wait as soon as an operation has already settled — for
// example to stop waiting for a disruption budget to be spent once the cluster has converged and the
// slot-movement phase the disruptor gates on can no longer recur.
func IsChaosConverged(
	ctx context.Context,
	c client.Client,
	clientset kubernetes.Interface,
	namespace, clusterName string,
) bool {
	ready, _ := chaosReadyState(ctx, c, clientset, namespace, clusterName)
	return ready
}

// IsInSlotMovementPhase reports whether the cluster is currently in a phase that physically moves
// hash slots or forms the cluster from scratch: the scale-up rebalance, the scale-down primary
// drain, or the (purge) cluster (re)formation. The chaos disruptor gates its bounded burst on this
// so its pod deletions land specifically while slots are in motion — the phase most sensitive to
// churn — rather than during the quiescent pod-startup steps. Any error returns false.
func IsInSlotMovementPhase(ctx context.Context, c client.Client, namespace, clusterName string) bool {
	cluster, err := GetRedkey(ctx, c, namespace, clusterName)
	if err != nil {
		return false
	}
	switch cluster.Status.Substatus.Status {
	case redkeyv1beta1.SubstatusRebalancing,
		redkeyv1beta1.SubstatusDrainingPrimaries,
		redkeyv1beta1.SubstatusFormingCluster:
		return true
	default:
		return false
	}
}

// highestConfigSettled reports whether the highest-sequence RedkeyConfig has finished
// applying (ConfigPhase=Applied and operational Status=Ready). The aggregated Redkey status
// can momentarily read Ready while a freshly-created, higher-sequence config has not been picked up
// yet, because the operator's aggregation falls back to the previous Applied config until Robin
// marks the new one InProgress. Gating on the highest-sequence config directly closes that window so
// the chaos loop never advances mid-operation.
func highestConfigSettled(ctx context.Context, c client.Client, namespace, clusterName string) (bool, string) {
	var configs redkeyv1beta1.RedkeyConfigList
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
		return false, "no RedkeyConfig found yet"
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

// clusterCheckPasses runs redis-cli --cluster check and returns true only when the cluster is fully
// settled. An in-flight resharding (open, migrating or importing slots) and coverage problems are
// reported by redis-cli as [WARNING]/[ERR] lines, while a clean cluster has neither. We must key off
// those markers and NOT off substrings like "open slots": the ">>> Check for open slots..." header
// is always printed, even on a healthy cluster, and matching it would never let readiness converge.
func clusterCheckPasses(ctx context.Context, namespace, podName string) bool {
	stdout, _, err := RemoteCommand(ctx, namespace, podName, "redis-cli --cluster check localhost:6379")
	if err != nil {
		return false
	}
	return !strings.Contains(stdout, "[ERR]") && !strings.Contains(stdout, "[WARNING]")
}

// AssertTopologyStable waits until the cluster topology is fully quiescent — redis-cli --cluster
// check reports no errors, warnings or open/migrating/importing slots on every pod — and stays that
// way for requiredStableChecks consecutive polls. This closes the window where Robin marks a scaling
// config Ready and only afterwards starts balancing slots (an in-flight resharding), which the CR
// phase and config-settled gates do not reflect. It is intended for the final verification with the
// background disruptor already stopped.
func AssertTopologyStable(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, clusterName string,
	timeout time.Duration,
) error {
	if timeout == 0 {
		timeout = defaultChaosReadyTimeout
	}

	var lastReason string
	stable := 0
	err := wait.PollUntilContextTimeout(ctx, readinessPollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: RedisPodsSelector(clusterName),
			})
			if err != nil {
				if ctx.Err() == nil {
					lastReason = fmt.Sprintf("error listing pods: %v", err)
				}
				stable = 0
				return false, nil
			}
			if len(pods.Items) == 0 {
				lastReason = "no redis pods found"
				stable = 0
				return false, nil
			}

			for _, pod := range pods.Items {
				if ctx.Err() != nil {
					return false, nil
				}
				if !clusterCheckPasses(ctx, namespace, pod.Name) {
					if ctx.Err() == nil {
						lastReason = fmt.Sprintf("redis-cli --cluster check not clean on pod %s (open/migrating slots)", pod.Name)
					}
					stable = 0
					return false, nil
				}
				if clusterNodesHasFailure(ctx, namespace, pod.Name) {
					if ctx.Err() == nil {
						lastReason = fmt.Sprintf("in-flight resharding or fail state on pod %s", pod.Name)
					}
					stable = 0
					return false, nil
				}
			}

			stable++
			if stable < requiredStableChecks {
				lastReason = fmt.Sprintf("topology clean, waiting for stability (%d/%d)", stable, requiredStableChecks)
				return false, nil
			}
			return true, nil
		})
	if err != nil && lastReason != "" {
		return fmt.Errorf("AssertTopologyStable(%s/%s): last check: %s: %w", namespace, clusterName, lastReason, err)
	}
	return err
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
