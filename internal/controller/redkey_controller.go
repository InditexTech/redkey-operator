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
	"sigs.k8s.io/controller-runtime/pkg/controller"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

const (
	// SequenceAnnotation is the annotation key for the config sequence counter on Redkey.
	SequenceAnnotation = "redkey.inditex.dev/config-sequence"
	// ClusterLabel is the label key used to associate RedkeyConfig with its parent.
	ClusterLabel = "redkey.inditex.dev/cluster"
)

// RedkeyReconciler reconciles a Redkey object.
// It reacts to Redkey spec changes, RedkeyConfig status changes
// (via ownership watch), and owned resource modifications (Deployment, RBAC).
// A periodic resync acts as a safety net against missed events.
type RedkeyReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	ResyncInterval time.Duration
	// MaxConcurrentReconciles is the number of reconciles that may run in parallel.
	// Values <= 0 fall back to controller-runtime's default of 1.
	MaxConcurrentReconciles int
}

// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeys/scale,verbs=get;update;patch
// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeyconfigs,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=redkey.inditex.dev,resources=redkeyconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps;services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=list;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles both Redkey spec changes and RedkeyConfig status changes.
func (r *RedkeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.V(1).Info("Reconciling Redkey Start", "namespace", req.Namespace, "name", req.Name)

	// Fetch the Redkey instance
	var cluster redisv1.Redkey
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if errors.IsNotFound(err) {
			// Redkey deleted — owned resources are garbage collected automatically.
			// Drop its fleet-state metrics so no stale series linger.
			deleteRedkeyMetrics(req.NamespacedName)
			log.Info("Redkey resource not found. Ignoring since object must be deleted.", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		redkeyReconcileStageErrors.WithLabelValues("get_cluster").Inc()
		return ctrl.Result{}, err
	}

	// Scale to zero: when primaries == 0, skip normal reconciliation.
	// This handles both creation with 0 primaries (no-op) and scaling from >0 to 0
	// (delegate cleanup to Robin via a RedkeyConfig with primaries=0).
	if cluster.Spec.Primaries == 0 {
		res, err := r.reconcileScaleToZero(ctx, &cluster)
		if err == nil {
			recordRedkeyMetrics(&cluster, nil)
		} else {
			redkeyReconcileStageErrors.WithLabelValues("scale_to_zero").Inc()
		}
		return res, err
	}

	// Ensure RBAC resources exist for Robin
	if err := r.ensureRBAC(ctx, &cluster); err != nil {
		log.Error(err, "Failed to ensure RBAC resources")
		redkeyReconcileStageErrors.WithLabelValues("ensure_rbac").Inc()
		return ctrl.Result{}, err
	}

	// Ensure Robin Deployment exists
	if err := r.ensureRobinDeployment(ctx, &cluster); err != nil {
		log.Error(err, "Failed to ensure Robin Deployment")
		redkeyReconcileStageErrors.WithLabelValues("ensure_robin_deployment").Inc()
		return ctrl.Result{}, err
	}

	// List all RedkeyConfigs for this cluster
	configs, err := r.listConfigs(ctx, &cluster)
	if err != nil {
		redkeyReconcileStageErrors.WithLabelValues("list_configs").Inc()
		return ctrl.Result{}, err
	}

	var highestSeq *redisv1.RedkeyConfig
	if len(configs) > 0 {
		highestSeq = &configs[len(configs)-1]
	}

	// If no configs exist, or generation changed, create a new config
	if len(configs) == 0 || needsNewConfig(&cluster, highestSeq) {
		if err := r.createNewConfig(ctx, &cluster, highestSeq); err != nil {
			log.Error(err, "Failed to create new RedkeyConfig")
			redkeyReconcileStageErrors.WithLabelValues("create_new_config").Inc()
			return ctrl.Result{}, err
		}
		log.Info("Created new RedkeyConfig", "cluster", cluster.Name, "generation", cluster.Generation)
		// Re-fetch configs after creation
		configs, err = r.listConfigs(ctx, &cluster)
		if err != nil {
			redkeyReconcileStageErrors.WithLabelValues("list_configs").Inc()
			return ctrl.Result{}, err
		}
	}

	if len(configs) > 0 {
		// Keep the latest Applied config as the status baseline and drop any older applied configs.
		configs, err = r.cleanupSupersededConfigs(ctx, configs)
		if err != nil {
			log.Error(err, "Failed to cleanup superseded configs")
			redkeyReconcileStageErrors.WithLabelValues("cleanup_superseded_configs").Inc()
			return ctrl.Result{}, err
		}

		// Aggregate status from highest-sequence config into Redkey status
		if err := r.aggregateStatus(ctx, &cluster, configs); err != nil {
			log.Error(err, "Failed to aggregate status")
			redkeyReconcileStageErrors.WithLabelValues("aggregate_status").Inc()
			return ctrl.Result{}, err
		}
	}

	log.V(1).Info("Reconciling Redkey End", "namespace", req.Namespace, "name", req.Name)

	// Refresh the fleet-state metrics from the freshly aggregated status.
	var activeConfig *redisv1.RedkeyConfig
	if len(configs) > 0 {
		activeConfig = selectActiveConfig(configs)
	}
	recordRedkeyMetrics(&cluster, activeConfig)

	// Periodic requeue as a safety net against bugs in the informer cache or missed events.
	// The controller should be fully event-driven under normal operation, so this is just a fallback.
	return ctrl.Result{RequeueAfter: r.ResyncInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RedkeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&redisv1.Redkey{}).
		Owns(&redisv1.RedkeyConfig{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Named("redkey").
		WithOptions(r.controllerOptions()).
		Complete(r)
}

// controllerOptions builds the controller-runtime options for this reconciler.
// A single worker processes the work queue for every Redkey across all watched
// namespaces, so allowing concurrent reconciles prevents one slow or backing-off cluster
// from starving the others. Values <= 0 keep controller-runtime's default of 1.
func (r *RedkeyReconciler) controllerOptions() controller.Options {
	opts := controller.Options{}
	if r.MaxConcurrentReconciles > 0 {
		opts.MaxConcurrentReconciles = r.MaxConcurrentReconciles
	}
	return opts
}
