// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	redisv1 "github.com/inditextech/redkey-operator/api/v1beta1"
)

// reconcileScaleToZero handles reconciliation when spec.primaries == 0.
//
// Two scenarios:
//   - Creation with 0 primaries: no objects are created, status is set to Ready with 0 replicas.
//   - Scale from >0 to 0: a RedkeyConfig with primaries=0 is created for Robin to process.
//     Robin deletes its managed objects (StatefulSet, Service, ConfigMap, PDB, optionally PVCs),
//     then marks the config as Applied. Once Applied, the operator cleans up its own objects
//     (Robin Deployment, RBAC, all RedkeyConfigs).
func (r *RedkeyReconciler) reconcileScaleToZero(ctx context.Context, cluster *redisv1.Redkey) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	configs, err := r.listConfigs(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Case A: No configs exist (fresh cluster with 0 primaries, or already fully cleaned up).
	// Don't create anything — just ensure status reflects scaled-to-zero state.
	if len(configs) == 0 {
		log.V(1).Info("Cluster scaled to zero, no configs — steady state")
		if err := r.updateStatusScaledToZero(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.ResyncInterval}, nil
	}

	latestConfig := &configs[len(configs)-1]

	// Case B: Latest config has primaries > 0 — we need to create a new config with primaries=0
	// so Robin can process the scale-down and clean up its objects.
	if latestConfig.Spec.Primaries != 0 {
		log.Info("Scaling to zero: creating config with primaries=0", "cluster", cluster.Name)

		// Keep Robin running so it can process the config
		if err := r.ensureRBAC(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.ensureRobinDeployment(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.createNewConfig(ctx, cluster, latestConfig); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.ResyncInterval}, nil
	}

	// Case C: Latest config has primaries=0 and is Applied — Robin finished cleanup.
	// Now the operator cleans up its own objects.
	if latestConfig.Status.ConfigPhase == redisv1.ConfigPhaseApplied {
		log.Info("Scale-to-zero config Applied, cleaning up operator objects", "cluster", cluster.Name)

		if err := r.deleteRobinDeployment(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.deleteRBAC(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.deleteAllConfigs(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.updateStatusScaledToZero(ctx, cluster); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: r.ResyncInterval}, nil
	}

	// Case D: Config with primaries=0 exists but is not yet Applied — Robin is working.
	// Keep Robin alive and aggregate status.
	log.Info("Waiting for Robin to complete scale-to-zero", "cluster", cluster.Name, "configPhase", latestConfig.Status.ConfigPhase)

	if err := r.ensureRBAC(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureRobinDeployment(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.aggregateStatus(ctx, cluster, configs); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: r.ResyncInterval}, nil
}

// deleteRobinDeployment deletes the Robin Deployment for the cluster.
func (r *RedkeyReconciler) deleteRobinDeployment(ctx context.Context, cluster *redisv1.Redkey) error {
	log := logf.FromContext(ctx)
	name := fmt.Sprintf("%s-robin", cluster.Name)

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cluster.Namespace,
		},
	}
	if err := r.Delete(ctx, deploy); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("deleting Robin Deployment %s: %w", name, err)
	}
	log.Info("Deleted Robin Deployment", "deployment", name)
	return nil
}

// deleteRBAC deletes the Robin ServiceAccount, Role, and RoleBinding.
func (r *RedkeyReconciler) deleteRBAC(ctx context.Context, cluster *redisv1.Redkey) error {
	log := logf.FromContext(ctx)
	name := fmt.Sprintf("%s-robin", cluster.Name)
	ns := cluster.Namespace

	objects := []client.Object{
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}},
	}

	for _, obj := range objects {
		if err := r.Delete(ctx, obj); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("deleting RBAC resource %s/%s: %w", obj.GetObjectKind().GroupVersionKind().Kind, name, err)
		}
		log.Info("Deleted RBAC resource", "kind", fmt.Sprintf("%T", obj), "name", name)
	}
	return nil
}

// deleteAllConfigs deletes all RedkeyConfigs associated with the cluster.
func (r *RedkeyReconciler) deleteAllConfigs(ctx context.Context, cluster *redisv1.Redkey) error {
	log := logf.FromContext(ctx)

	var configList redisv1.RedkeyConfigList
	if err := r.List(ctx, &configList,
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels{ClusterLabel: cluster.Name},
	); err != nil {
		return fmt.Errorf("listing configs for deletion: %w", err)
	}

	for i := range configList.Items {
		if err := r.Delete(ctx, &configList.Items[i]); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("deleting config %s: %w", configList.Items[i].Name, err)
		}
		log.Info("Deleted RedkeyConfig", "config", configList.Items[i].Name)
	}
	return nil
}

// updateStatusScaledToZero updates the Redkey status to reflect a scaled-to-zero state.
func (r *RedkeyReconciler) updateStatusScaledToZero(ctx context.Context, cluster *redisv1.Redkey) error {
	now := metav1.Now()

	cluster.Status.Replicas = 0
	cluster.Status.Phase = redisv1.PhaseReady
	cluster.Status.Status = ""
	cluster.Status.Substatus = redisv1.RedkeySubstatus{}
	cluster.Status.Nodes = map[string]*redisv1.RedisNode{}
	cluster.Status.LastUpdatedAt = &now
	cluster.Status.ObservedGeneration = cluster.Generation

	conditions := cluster.Status.Conditions
	meta.SetStatusCondition(&conditions, metav1.Condition{
		Type:   "Ready",
		Status: metav1.ConditionTrue,
		Reason: "ScaledToZero",
	})
	meta.SetStatusCondition(&conditions, metav1.Condition{
		Type:   "ConfigPending",
		Status: metav1.ConditionFalse,
		Reason: "ScaledToZero",
	})
	meta.SetStatusCondition(&conditions, metav1.Condition{
		Type:   "Error",
		Status: metav1.ConditionFalse,
		Reason: "ScaledToZero",
	})
	cluster.Status.Conditions = conditions

	return r.Status().Update(ctx, cluster)
}
