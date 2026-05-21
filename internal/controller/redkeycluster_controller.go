// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

const (
	// SequenceAnnotation is the annotation key for the config sequence counter on RedkeyCluster.
	SequenceAnnotation = "redkey.inditex.dev/config-sequence"
	// ClusterLabel is the label key used to associate RedkeyClusterConfig with its parent.
	ClusterLabel = "redkey.inditex.dev/cluster"
)

// RedkeyClusterReconciler reconciles a RedkeyCluster object.
// It reacts to RedkeyCluster spec changes, RedkeyClusterConfig status changes
// (via ownership watch), and owned resource modifications (Deployment, RBAC).
// A periodic resync acts as a safety net against missed events.
type RedkeyClusterReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	ResyncInterval time.Duration
}

// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeyclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeyclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeyclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeyclusterconfigs,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeyclusterconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps;services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles both RedkeyCluster spec changes and RedkeyClusterConfig status changes.
func (r *RedkeyClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.V(1).Info("Reconciling RedkeyCluster Start", "namespace", req.Namespace, "name", req.Name)

	// Fetch the RedkeyCluster instance
	var cluster redisv1.RedkeyCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if errors.IsNotFound(err) {
			// RedkeyCluster deleted — owned resources are garbage collected automatically
			log.Info("RedkeyCluster resource not found. Ignoring since object must be deleted.", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Ensure RBAC resources exist for Robin
	if err := r.ensureRBAC(ctx, &cluster); err != nil {
		log.Error(err, "Failed to ensure RBAC resources")
		return ctrl.Result{}, err
	}

	// Ensure Robin Deployment exists
	if err := r.ensureRobinDeployment(ctx, &cluster); err != nil {
		log.Error(err, "Failed to ensure Robin Deployment")
		return ctrl.Result{}, err
	}

	// List all RedkeyClusterConfigs for this cluster
	configs, err := r.listConfigs(ctx, &cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	var highestSeq *redisv1.RedkeyClusterConfig
	if len(configs) > 0 {
		highestSeq = &configs[len(configs)-1]
	}

	// If no configs exist, or generation changed, create a new config
	if len(configs) == 0 || needsNewConfig(&cluster, highestSeq) {
		if err := r.createNewConfig(ctx, &cluster, highestSeq); err != nil {
			log.Error(err, "Failed to create new RedkeyClusterConfig")
			return ctrl.Result{}, err
		}
		log.Info("Created new RedkeyClusterConfig", "cluster", cluster.Name, "generation", cluster.Generation)
		// Re-fetch configs after creation
		configs, err = r.listConfigs(ctx, &cluster)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	if len(configs) > 0 {
		// Keep the latest Applied config as the status baseline and drop any older applied configs.
		configs, err = r.cleanupSupersededConfigs(ctx, configs)
		if err != nil {
			log.Error(err, "Failed to cleanup superseded configs")
			return ctrl.Result{}, err
		}

		// Aggregate status from highest-sequence config into RedkeyCluster status
		if err := r.aggregateStatus(ctx, &cluster, configs); err != nil {
			log.Error(err, "Failed to aggregate status")
			return ctrl.Result{}, err
		}
	}

	log.V(1).Info("Reconciling RedkeyCluster End", "namespace", req.Namespace, "name", req.Name)

	// Periodic requeue as a safety net against bugs in the informer cache or missed events.
	// The controller should be fully event-driven under normal operation, so this is just a fallback.
	return ctrl.Result{RequeueAfter: r.ResyncInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RedkeyClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&redisv1.RedkeyCluster{}).
		Owns(&redisv1.RedkeyClusterConfig{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Named("redkeycluster").
		Complete(r)
}
