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

// listConfigs returns all RedkeyConfigs for the cluster, sorted by sequence (ascending).
func (r *RedkeyReconciler) listConfigs(ctx context.Context, cluster *redisv1.Redkey) ([]redisv1.RedkeyConfig, error) {
	var configList redisv1.RedkeyConfigList
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

// needsNewConfig determines if a new RedkeyConfig should be created based on generation tracking.
func needsNewConfig(cluster *redisv1.Redkey, latestConfig *redisv1.RedkeyConfig) bool {
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

// createNewConfig increments the sequence counter and creates a new RedkeyConfig.
func (r *RedkeyReconciler) createNewConfig(ctx context.Context, cluster *redisv1.Redkey, highestSeq *redisv1.RedkeyConfig) error {
	// Increment sequence counter
	seq := 1
	if highestSeq != nil {
		seq = highestSeq.Spec.Sequence + 1
	}

	// Build the RedkeyConfig
	config := &redisv1.RedkeyConfig{
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
		Spec: redisv1.RedkeyConfigSpec{
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
			log.V(1).Info("RedkeyConfig already exists, skipping creation (stale cache)", "config", config.Name)
			return nil
		}
		return err
	}

	log.Info("Created new RedkeyConfig", "config", config.Name)
	return nil
}

// aggregateStatus reads the active config status and mirrors the aggregated result to Redkey status.
// If there is only one config, it is used directly. If there are multiple, the one currently being applied
// (InProgress) is used, since configs are processed sequentially and pending ones have not been started yet.
func (r *RedkeyReconciler) aggregateStatus(ctx context.Context, cluster *redisv1.Redkey, configs []redisv1.RedkeyConfig) error {
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
		var latest redisv1.Redkey
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

		// Continuously track the last applied, non-zero topology for storage (non-ephemeral)
		// clusters. This is deliberately updated on EVERY successful reconcile while the cluster is
		// Applied at primaries>0 — NOT only at scale-to-zero — and is NEVER cleared. Recording it here,
		// during steady state, guarantees the value is already persisted BEFORE any scale-to-zero,
		// which is exactly what makes the root-level "scale-up-from-zero must match the previous
		// topology" CEL rule RACE-FREE. Do NOT move this to a scale-down-only path and do NOT add
		// clearing logic: either would reintroduce the admission-vs-reconcile race and allow
		// inconsistent PVC remounts. Only Applied configs are used so in-progress scale targets are
		// not captured until they have actually converged.
		if !latest.Spec.Ephemeral &&
			activeConfig.Status.ConfigPhase == redisv1.ConfigPhaseApplied &&
			activeConfig.Spec.Primaries > 0 {
			latest.Status.LastAppliedPrimaries = activeConfig.Spec.Primaries
			latest.Status.LastAppliedReplicasPerPrimary = activeConfig.Spec.ReplicasPerPrimary
		}

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
	log.Info("Updated Redkey status from config", "config", activeConfig.Name, "status", lastStatus, "phase", lastPhase)
	return nil
}

// selectActiveConfig returns the config that should be used to aggregate the cluster status.
// If there is only one config, it is returned directly. If there are multiple, the InProgress
// config is returned (Robin processes configs sequentially). If no InProgress config exists,
// the first config (the current Applied baseline) is used as fallback.
func selectActiveConfig(configs []redisv1.RedkeyConfig) *redisv1.RedkeyConfig {
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

func aggregateConditions(existingConditions []metav1.Condition, highestConfig *redisv1.RedkeyConfig) []metav1.Condition {
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

	aggregateHealthConditions(&conditions, highestConfig)

	return conditions
}

// healthConditionTypes are the live data-plane health conditions Robin writes on the config from the
// cluster health report. They are mirrored onto the Redkey independently of Ready/Phase: a
// cluster can be Ready (config applied) while the health-reconciler is still healing or rebalancing.
var healthConditionTypes = []string{
	redisv1.ConditionHealthy,
	redisv1.ConditionMembershipHealthy,
	redisv1.ConditionSlotsCovered,
	redisv1.ConditionSlotsBalanced,
	redisv1.ConditionReplicasBalanced,
	redisv1.ConditionClusterCheckPassing,
}

// aggregateHealthConditions mirrors Robin's health conditions from the active config onto the
// cluster. Robin only refreshes them while the cluster is applied and Ready, so while an operation
// is in progress (or no report exists yet) the last values are stale — in that case they are
// reported as Unknown/Reconciling instead of a potentially misleading True/False.
func aggregateHealthConditions(conditions *[]metav1.Condition, activeConfig *redisv1.RedkeyConfig) {
	settled := isTerminalConfigPhase(activeConfig.Status.ConfigPhase) &&
		activeConfig.Status.Status == redisv1.ClusterStatusReady

	for _, condType := range healthConditionTypes {
		if settled {
			if src := meta.FindStatusCondition(activeConfig.Status.Conditions, condType); src != nil {
				meta.SetStatusCondition(conditions, metav1.Condition{
					Type:    condType,
					Status:  src.Status,
					Reason:  src.Reason,
					Message: src.Message,
				})
				continue
			}
		}
		meta.SetStatusCondition(conditions, metav1.Condition{
			Type:   condType,
			Status: metav1.ConditionUnknown,
			Reason: "Reconciling",
		})
	}
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

func isConfigReady(config *redisv1.RedkeyConfig) bool {
	return config != nil && isTerminalConfigPhase(config.Status.ConfigPhase) && config.Status.Status != redisv1.ClusterPhaseError
}

func hasConfigError(config *redisv1.RedkeyConfig) bool {
	return config != nil && isTerminalConfigPhase(config.Status.ConfigPhase) && config.Status.Status == redisv1.ClusterPhaseError
}

// cleanupSupersededConfigs deletes every config older than the last Applied config.
// It returns the last Applied config and any newer configs unchanged.
func (r *RedkeyReconciler) cleanupSupersededConfigs(ctx context.Context, configs []redisv1.RedkeyConfig) ([]redisv1.RedkeyConfig, error) {
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
		log.Info("Deleted superseded RedkeyConfig", "config", configs[i].Name)
	}

	return configs[lastApplied:], nil
}

func conditionStatus(b bool) metav1.ConditionStatus {
	if b {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}
