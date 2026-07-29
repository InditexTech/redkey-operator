// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

// Default timeouts and intervals for polling.
// These can be overridden via environment variables for CI tuning:
//   - E2E_POLL_INTERVAL: poll interval in seconds (default: 3)
//   - E2E_CREATION_TIMEOUT: creation timeout in seconds (default: 180)
//   - E2E_HEALTH_TIMEOUT: health timeout in seconds (default: 180)
//   - E2E_UPGRADE_TIMEOUT: upgrade timeout in seconds (default: 900)
var (
	DefaultTimeout      = 10 * time.Minute
	DefaultPollInterval = envDurationSeconds("E2E_POLL_INTERVAL", 3)
	CreationTimeout     = envDurationSeconds("E2E_CREATION_TIMEOUT", 180)
	HealthTimeout       = envDurationSeconds("E2E_HEALTH_TIMEOUT", 180)
	// UpgradeTimeout covers a full rolling N+1 upgrade. The heaviest topology
	// (3 primaries x 2 replicas = 9 nodes) recycles every pod one at a time and
	// then migrates all slots back from the extra node in the ending phase, which
	// can exceed 10 minutes on loaded runners. Default to 15 minutes.
	UpgradeTimeout = envDurationSeconds("E2E_UPGRADE_TIMEOUT", 900)
)

func envDurationSeconds(key string, defaultSeconds int) time.Duration {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			return time.Duration(parsed) * time.Second
		}
	}
	return time.Duration(defaultSeconds) * time.Second
}

// WaitForClusterPhase waits until the Redkey has the specified .status.phase.
func WaitForClusterPhase(
	ctx context.Context,
	c client.Client,
	key types.NamespacedName,
	desiredPhase string,
	timeout time.Duration,
) (*redkeyv1beta1.Redkey, error) {
	var lastPhase string

	err := wait.PollUntilContextTimeout(ctx, DefaultPollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			cluster := &redkeyv1beta1.Redkey{}
			if err := c.Get(ctx, key, cluster); err != nil {
				return false, nil // keep polling
			}
			lastPhase = cluster.Status.Phase
			if lastPhase != "" && lastPhase != desiredPhase {
				_, _ = fmt.Fprintf(GinkgoWriter, "  [wait] cluster %s phase: %s (want: %s)\n", key.Name, lastPhase, desiredPhase)
			}
			return lastPhase == desiredPhase, nil
		})
	if err != nil {
		return nil, fmt.Errorf("timed out waiting for cluster %s phase %q (last seen: %q): %w",
			key.Name, desiredPhase, lastPhase, err)
	}

	cluster := &redkeyv1beta1.Redkey{}
	if err := c.Get(ctx, key, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// WaitForClusterStatus waits until the Redkey has the specified .status.status.
func WaitForClusterStatus(
	ctx context.Context,
	c client.Client,
	key types.NamespacedName,
	desiredStatus string,
	timeout time.Duration,
) (*redkeyv1beta1.Redkey, error) {
	var lastStatus string

	err := wait.PollUntilContextTimeout(ctx, DefaultPollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			cluster := &redkeyv1beta1.Redkey{}
			if err := c.Get(ctx, key, cluster); err != nil {
				return false, nil
			}
			lastStatus = cluster.Status.Status
			return lastStatus == desiredStatus, nil
		})
	if err != nil {
		return nil, fmt.Errorf("timed out waiting for cluster %s status %q (last seen: %q): %w",
			key.Name, desiredStatus, lastStatus, err)
	}

	cluster := &redkeyv1beta1.Redkey{}
	if err := c.Get(ctx, key, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// WaitForConfigPhase waits until the RedkeyConfig with the given key has the desired configPhase.
func WaitForConfigPhase(
	ctx context.Context,
	c client.Client,
	key types.NamespacedName,
	desiredPhase string,
	timeout time.Duration,
) (*redkeyv1beta1.RedkeyConfig, error) {
	var lastPhase string

	err := wait.PollUntilContextTimeout(ctx, DefaultPollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			config := &redkeyv1beta1.RedkeyConfig{}
			if err := c.Get(ctx, key, config); err != nil {
				return false, nil
			}
			lastPhase = config.Status.ConfigPhase
			return lastPhase == desiredPhase, nil
		})
	if err != nil {
		return nil, fmt.Errorf("timed out waiting for config %s phase %q (last: %q): %w",
			key.Name, desiredPhase, lastPhase, err)
	}

	config := &redkeyv1beta1.RedkeyConfig{}
	if err := c.Get(ctx, key, config); err != nil {
		return nil, err
	}
	return config, nil
}

// WaitForPodsReady waits until all pods matching the label selector are running and ready.
func WaitForPodsReady(
	ctx context.Context,
	c client.Client,
	namespace string,
	labelSelector map[string]string,
	expectedCount int,
	timeout time.Duration,
) error {
	return wait.PollUntilContextTimeout(ctx, DefaultPollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			pods := &corev1.PodList{}
			if err := c.List(ctx, pods, client.InNamespace(namespace), client.MatchingLabels(labelSelector)); err != nil {
				return false, nil
			}
			if len(pods.Items) != expectedCount {
				return false, nil
			}
			for _, pod := range pods.Items {
				if pod.Status.Phase != corev1.PodRunning {
					return false, nil
				}
				ready := false
				for _, cond := range pod.Status.Conditions {
					if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
						ready = true
						break
					}
				}
				if !ready {
					return false, nil
				}
			}
			return true, nil
		})
}

// WaitForClusterReady is a convenience function that waits for Phase=Ready and Status=Ready.
func WaitForClusterReady(
	ctx context.Context,
	c client.Client,
	key types.NamespacedName,
	timeout time.Duration,
) (*redkeyv1beta1.Redkey, error) {
	return WaitForClusterPhase(ctx, c, key, redkeyv1beta1.PhaseReady, timeout)
}

// WaitForActiveConfigApplied waits until the active (highest-sequence) config reaches "Applied".
func WaitForActiveConfigApplied(
	ctx context.Context,
	c client.Client,
	clusterName, namespace string,
	timeout time.Duration,
) (*redkeyv1beta1.RedkeyConfig, error) {
	var lastPhase string
	var lastConfigName string

	err := wait.PollUntilContextTimeout(ctx, DefaultPollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			configs := &redkeyv1beta1.RedkeyConfigList{}
			if err := c.List(ctx, configs, client.InNamespace(namespace),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
				return false, nil
			}
			if len(configs.Items) == 0 {
				return false, nil
			}

			// Find highest-sequence config
			var active *redkeyv1beta1.RedkeyConfig
			maxSeq := -1
			for i := range configs.Items {
				if configs.Items[i].Spec.Sequence > maxSeq {
					maxSeq = configs.Items[i].Spec.Sequence
					active = &configs.Items[i]
				}
			}
			if active == nil {
				return false, nil
			}

			lastConfigName = active.Name
			lastPhase = active.Status.ConfigPhase
			return lastPhase == redkeyv1beta1.ConfigPhaseApplied, nil
		})
	if err != nil {
		return nil, fmt.Errorf("timed out waiting for active config %q phase Applied (last: %q): %w",
			lastConfigName, lastPhase, err)
	}

	// Return the final config
	configs := &redkeyv1beta1.RedkeyConfigList{}
	if err := c.List(ctx, configs, client.InNamespace(namespace),
		client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
		return nil, err
	}
	var active *redkeyv1beta1.RedkeyConfig
	maxSeq := -1
	for i := range configs.Items {
		if configs.Items[i].Spec.Sequence > maxSeq {
			maxSeq = configs.Items[i].Spec.Sequence
			active = &configs.Items[i]
		}
	}
	return active, nil
}

// WaitForActiveConfigAppliedTopology waits until the active (highest-sequence) config both
// matches the expected topology (primaries and replicasPerPrimary) and reaches "Applied".
//
// Matching the topology is essential after a spec update: there is a brief window between
// the cluster spec change and the operator creating the new config during which the
// previous, already-Applied config is still the highest-sequence one. A plain
// WaitForActiveConfigApplied would observe that stale config and return immediately,
// declaring the scaling operation complete before it has even started. Requiring the active
// config to reflect the desired topology closes that race.
func WaitForActiveConfigAppliedTopology(
	ctx context.Context,
	c client.Client,
	clusterName, namespace string,
	expectedPrimaries, expectedReplicasPerPrimary int,
	timeout time.Duration,
) (*redkeyv1beta1.RedkeyConfig, error) {
	var lastPhase, lastConfigName string
	var lastPrimaries, lastReplicas int32

	err := wait.PollUntilContextTimeout(ctx, DefaultPollInterval, timeout, true,
		func(ctx context.Context) (bool, error) {
			configs := &redkeyv1beta1.RedkeyConfigList{}
			if err := c.List(ctx, configs, client.InNamespace(namespace),
				client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
				return false, nil
			}
			if len(configs.Items) == 0 {
				return false, nil
			}

			var active *redkeyv1beta1.RedkeyConfig
			maxSeq := -1
			for i := range configs.Items {
				if configs.Items[i].Spec.Sequence > maxSeq {
					maxSeq = configs.Items[i].Spec.Sequence
					active = &configs.Items[i]
				}
			}
			if active == nil {
				return false, nil
			}

			lastConfigName = active.Name
			lastPhase = active.Status.ConfigPhase
			lastPrimaries = active.Spec.Primaries
			lastReplicas = active.Spec.ReplicasPerPrimary

			topologyMatches := lastPrimaries == int32(expectedPrimaries) &&
				lastReplicas == int32(expectedReplicasPerPrimary)
			return topologyMatches && lastPhase == redkeyv1beta1.ConfigPhaseApplied, nil
		})
	if err != nil {
		return nil, fmt.Errorf(
			"timed out waiting for active config %q (primaries=%d replicasPerPrimary=%d) phase Applied "+
				"matching target topology (primaries=%d replicasPerPrimary=%d) (last phase: %q): %w",
			lastConfigName, lastPrimaries, lastReplicas,
			expectedPrimaries, expectedReplicasPerPrimary, lastPhase, err)
	}

	return WaitForActiveConfigApplied(ctx, c, clusterName, namespace, timeout)
}

// ListConfigs returns all RedkeyConfigs for a cluster, sorted by sequence.
func ListConfigs(
	ctx context.Context,
	c client.Client,
	clusterName, namespace string,
) ([]redkeyv1beta1.RedkeyConfig, error) {
	configs := &redkeyv1beta1.RedkeyConfigList{}
	if err := c.List(ctx, configs, client.InNamespace(namespace),
		client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
		return nil, err
	}

	// Sort by sequence
	items := configs.Items
	for i := range items {
		for j := i + 1; j < len(items); j++ {
			if items[j].Spec.Sequence < items[i].Spec.Sequence {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	return items, nil
}

// SetClusterConfigStatus sets the status.status field of the active RedkeyConfig for a cluster.
// This can be used to put Robin into Maintenance mode or restore it to Ready.
func SetClusterConfigStatus(
	ctx context.Context,
	c client.Client,
	clusterName, namespace string,
	status string,
) error {
	configs := &redkeyv1beta1.RedkeyConfigList{}
	if err := c.List(ctx, configs, client.InNamespace(namespace),
		client.MatchingLabels{"redkey.inditex.dev/cluster": clusterName}); err != nil {
		return fmt.Errorf("listing configs for cluster %s: %w", clusterName, err)
	}
	if len(configs.Items) == 0 {
		return fmt.Errorf("no RedkeyConfig found for cluster %s in namespace %s", clusterName, namespace)
	}

	// Find highest-sequence config (active)
	var active *redkeyv1beta1.RedkeyConfig
	maxSeq := -1
	for i := range configs.Items {
		if configs.Items[i].Spec.Sequence > maxSeq {
			maxSeq = configs.Items[i].Spec.Sequence
			active = &configs.Items[i]
		}
	}

	active.Status.Status = status
	if err := c.Status().Update(ctx, active); err != nil {
		return fmt.Errorf("updating config %s status to %s: %w", active.Name, status, err)
	}
	return nil
}
