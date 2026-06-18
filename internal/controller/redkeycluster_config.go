// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

// listConfigs returns all RedkeyClusterConfigs for the cluster, sorted by sequence (ascending).
func (r *RedkeyClusterReconciler) listConfigs(ctx context.Context, cluster *redisv1.RedkeyCluster) ([]redisv1.RedkeyClusterConfig, error) {
	var configList redisv1.RedkeyClusterConfigList
	if err := r.List(ctx, &configList,
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels{ClusterLabel: cluster.Name},
	); err != nil {
		return nil, err
	}

	items := configList.Items
	sort.Slice(items, func(i, j int) bool {
		return items[i].Spec.Sequence < items[j].Spec.Sequence
	})
	return items, nil
}

// needsNewConfig determines if a new RedkeyClusterConfig should be created based on generation tracking.
func needsNewConfig(cluster *redisv1.RedkeyCluster, latestConfig *redisv1.RedkeyClusterConfig) bool {
	// If a config exists and it was created for the current generation of the cluster, we don't need a new one.
	if latestConfig != nil {
		if genStr := latestConfig.Annotations["redkey.inditex.dev/cluster-generation"]; genStr != "" {
			if parsedGen, err := strconv.ParseInt(genStr, 10, 64); err == nil {
				if parsedGen >= cluster.Generation {
					return false
				}
			}
		}
	}
	// Fallback to cluster's observed generation
	return cluster.Status.ObservedGeneration < cluster.Generation
}

// createNewConfig increments the sequence counter and creates a new RedkeyClusterConfig.
func (r *RedkeyClusterReconciler) createNewConfig(ctx context.Context, cluster *redisv1.RedkeyCluster, highestSeq *redisv1.RedkeyClusterConfig) error {
	// Increment sequence counter
	seq := 1
	if highestSeq != nil {
		seq = highestSeq.Spec.Sequence + 1
	}

	// Build the RedkeyClusterConfig
	config := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%d", cluster.Name, seq),
			Namespace: cluster.Namespace,
			Labels: mergeMeta(derefMap(cluster.Spec.Labels), nil, map[string]string{
				ClusterLabel: cluster.Name,
			}),
			Annotations: mergeMeta(derefMap(cluster.Spec.Annotations), nil, map[string]string{
				"redkey.inditex.dev/cluster-generation": strconv.FormatInt(cluster.Generation, 10),
			}),
		},
		Spec: redisv1.RedkeyClusterConfigSpec{
			Sequence:             seq,
			SkipIfSuperseded:     cluster.Spec.SkipIfSuperseded,
			Mode:                 cluster.Spec.Mode,
			Primaries:            cluster.Spec.Primaries,
			ReplicasPerPrimary:   cluster.Spec.ReplicasPerPrimary,
			Ephemeral:            cluster.Spec.Ephemeral,
			Storage:              cluster.Spec.Storage,
			StorageClassName:     cluster.Spec.StorageClassName,
			Image:                cluster.Spec.Image,
			Version:              cluster.Spec.Version,
			RedisConfig:          cluster.Spec.Config,
			Resources:            cluster.Spec.Resources,
			Auth:                 cluster.Spec.Auth,
			Labels:               cluster.Spec.Labels,
			Annotations:          cluster.Spec.Annotations,
			DeletePVC:            cluster.Spec.DeletePVC,
			PurgeKeysOnRebalance: cluster.Spec.PurgeKeysOnRebalance,
			Override:             cluster.Spec.Override,
			Pdb:                  cluster.Spec.Pdb,
		},
	}

	// Pass Robin config if present
	if cluster.Spec.Robin.Config != nil {
		config.Spec.RobinConfig = cluster.Spec.Robin.Config
	}

	// Set ownerReference — enables cascading deletion and event-driven reconciliation
	if err := controllerutil.SetControllerReference(cluster, config, r.Scheme); err != nil {
		return err
	}

	log := logf.FromContext(ctx)

	if err := r.Create(ctx, config); err != nil {
		// AlreadyExists happens when a rapid follow-up reconcile runs before the informer
		// cache has observed a config we just created: listConfigs returns a stale (empty or
		// outdated) list, leading us to recompute the same sequence. The config is already
		// present, so treat this as a no-op rather than failing and backing off the reconcile.
		if errors.IsAlreadyExists(err) {
			log.V(1).Info("RedkeyClusterConfig already exists, skipping creation (stale cache)", "config", config.Name)
			return nil
		}
		return err
	}

	log.Info("Created new RedkeyClusterConfig", "config", config.Name)
	return nil
}

// aggregateStatus reads the active config status and mirrors the aggregated result to RedkeyCluster status.
// If there is only one config, it is used directly. If there are multiple, the one currently being applied
// (InProgress) is used, since configs are processed sequentially and pending ones have not been started yet.
func (r *RedkeyClusterReconciler) aggregateStatus(ctx context.Context, cluster *redisv1.RedkeyCluster, configs []redisv1.RedkeyClusterConfig) error {
	if len(configs) == 0 {
		return nil
	}

	activeConfig := selectActiveConfig(configs)

	// Capture the generation from the reconcile-start snapshot. ObservedGeneration must reflect
	// the generation whose config we have actually created/aggregated in this reconcile, not the
	// (possibly newer) generation re-fetched below: during rapid spec changes the live object may
	// already be several generations ahead, and claiming to have observed it would make
	// needsNewConfig skip creating the configs for those newer generations, stalling convergence
	// (e.g. superseding scale-ups stuck at an intermediate topology).
	observedGeneration := cluster.Generation

	// Retry on conflict: the cluster object we were handed was fetched at the start of the
	// reconcile, but owned-resource events and rapid resyncs can mutate it concurrently. Without
	// a refresh-and-retry, every conflicting Status().Update fails the whole reconcile and backs
	// off, which under CI churn can stall convergence (cluster stuck in Configuring/Initializing).
	var lastStatus, lastPhase string
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest redisv1.RedkeyCluster
		if err := r.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}, &latest); err != nil {
			return err
		}

		now := metav1.Now()
		latest.Status.Replicas = latest.Spec.Primaries
		latest.Status.Status = activeConfig.Status.Status
		latest.Status.Substatus = activeConfig.Status.Substatus
		latest.Status.Nodes = emptyNodesIfNil(activeConfig.Status.Nodes)
		latest.Status.Conditions = aggregateConditions(latest.Status.Conditions, activeConfig)
		latest.Status.Phase = computePhaseFromConditions(latest.Status.Conditions)
		latest.Status.LastUpdatedAt = &now
		latest.Status.ObservedGeneration = observedGeneration

		lastStatus, lastPhase = latest.Status.Status, latest.Status.Phase
		if err := r.Status().Update(ctx, &latest); err != nil {
			return err
		}
		// Keep the caller's copy in sync with what we persisted.
		latest.Status.DeepCopyInto(&cluster.Status)
		return nil
	})
	if err != nil {
		return err
	}
	log := logf.FromContext(ctx)
	log.Info("Updated RedkeyCluster status from config", "config", activeConfig.Name, "status", lastStatus, "phase", lastPhase)
	return nil
}

// selectActiveConfig returns the config that should be used to aggregate the cluster status.
// If there is only one config, it is returned directly. If there are multiple, the InProgress
// config is returned (Robin processes configs sequentially). If no InProgress config exists,
// the first config (the current Applied baseline) is used as fallback.
func selectActiveConfig(configs []redisv1.RedkeyClusterConfig) *redisv1.RedkeyClusterConfig {
	if len(configs) == 1 {
		return &configs[0]
	}
	for i := range configs {
		if configs[i].Status.ConfigPhase == redisv1.ConfigPhaseInProgress {
			return &configs[i]
		}
	}
	return &configs[0]
}

func aggregateConditions(existingConditions []metav1.Condition, highestConfig *redisv1.RedkeyClusterConfig) []metav1.Condition {
	conditions := append([]metav1.Condition(nil), existingConditions...)

	meta.SetStatusCondition(&conditions, metav1.Condition{
		Type:   "Ready",
		Status: conditionStatus(isConfigReady(highestConfig)),
		Reason: "StatusAggregated",
	})
	meta.SetStatusCondition(&conditions, metav1.Condition{
		Type:   "ConfigPending",
		Status: conditionStatus(!isTerminalConfigPhase(highestConfig.Status.ConfigPhase)),
		Reason: "StatusAggregated",
	})
	meta.SetStatusCondition(&conditions, metav1.Condition{
		Type:   "Error",
		Status: conditionStatus(hasConfigError(highestConfig)),
		Reason: "StatusAggregated",
	})

	return conditions
}

func computePhaseFromConditions(conditions []metav1.Condition) string {
	errorCond := meta.FindStatusCondition(conditions, "Error")
	if errorCond != nil && errorCond.Status == metav1.ConditionTrue {
		return redisv1.PhaseError
	}

	readyCond := meta.FindStatusCondition(conditions, "Ready")
	if readyCond != nil && readyCond.Status == metav1.ConditionTrue {
		return redisv1.PhaseReady
	}

	return redisv1.PhaseConfiguring
}

func isTerminalConfigPhase(phase string) bool {
	return phase == redisv1.ConfigPhaseApplied || phase == redisv1.ConfigPhaseSuperseded
}

func emptyNodesIfNil(nodes map[string]*redisv1.RedisNode) map[string]*redisv1.RedisNode {
	if nodes == nil {
		return map[string]*redisv1.RedisNode{}
	}

	return nodes
}

func isConfigReady(config *redisv1.RedkeyClusterConfig) bool {
	return config != nil && isTerminalConfigPhase(config.Status.ConfigPhase) && config.Status.Status != redisv1.ClusterPhaseError
}

func hasConfigError(config *redisv1.RedkeyClusterConfig) bool {
	return config != nil && isTerminalConfigPhase(config.Status.ConfigPhase) && config.Status.Status == redisv1.ClusterPhaseError
}

// cleanupSupersededConfigs deletes every config older than the last Applied config.
// It returns the last Applied config and any newer configs unchanged.
func (r *RedkeyClusterReconciler) cleanupSupersededConfigs(ctx context.Context, configs []redisv1.RedkeyClusterConfig) ([]redisv1.RedkeyClusterConfig, error) {
	if len(configs) <= 1 {
		return configs, nil
	}

	log := logf.FromContext(ctx)

	lastApplied := -1
	for i := len(configs) - 1; i >= 0; i-- {
		if configs[i].Status.ConfigPhase == redisv1.ConfigPhaseApplied {
			lastApplied = i
			break
		}
	}

	if lastApplied <= 0 {
		return configs, nil
	}

	for i := 0; i < lastApplied; i++ {
		if err := r.Delete(ctx, &configs[i]); err != nil && !errors.IsNotFound(err) {
			return configs[i:], err
		}
		log.Info("Deleted superseded RedkeyClusterConfig", "config", configs[i].Name)
	}

	return configs[lastApplied:], nil
}

func conditionStatus(b bool) metav1.ConditionStatus {
	if b {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}
