// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

// DesiredRobinRules returns the RBAC PolicyRules that the Robin ServiceAccount needs.
func DesiredRobinRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{"apps"},
			Resources: []string{"statefulsets"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"services", "configmaps"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"policy"},
			Resources: []string{"poddisruptionbudgets"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"secrets"},
			Verbs:     []string{"get"},
		},
		{
			APIGroups: []string{"redkey.inditex.dev"},
			Resources: []string{"redkeyclusters"},
			Verbs:     []string{"get"},
		},
		{
			APIGroups: []string{"redkey.inditex.dev"},
			Resources: []string{"redkeyclusterconfigs"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"redkey.inditex.dev"},
			Resources: []string{"redkeyclusterconfigs/status"},
			Verbs:     []string{"update", "patch"},
		},
	}
}

// ensureRBAC ensures the ServiceAccount, Role, and RoleBinding for Robin exist
// in the cluster's namespace and match the desired state. If any resource is
// missing it is created; if it has drifted from the desired spec it is patched
// (or, for RoleBinding whose RoleRef is immutable, deleted and recreated).
func (r *RedkeyClusterReconciler) ensureRBAC(ctx context.Context, cluster *redisv1.RedkeyCluster) error {
	log := logf.FromContext(ctx)
	saName := fmt.Sprintf("%s-robin", cluster.Name)
	key := types.NamespacedName{Name: saName, Namespace: cluster.Namespace}

	// --- ServiceAccount ---
	desiredSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: cluster.Namespace,
		},
	}
	if err := controllerutil.SetControllerReference(cluster, desiredSA, r.Scheme); err != nil {
		return err
	}

	var existingSA corev1.ServiceAccount
	if err := r.Get(ctx, key, &existingSA); errors.IsNotFound(err) {
		log.Info("Creating Robin ServiceAccount", "serviceaccount", saName)
		if err := r.Create(ctx, desiredSA); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// --- Role ---
	desiredRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: cluster.Namespace,
		},
		Rules: DesiredRobinRules(),
	}
	if err := controllerutil.SetControllerReference(cluster, desiredRole, r.Scheme); err != nil {
		return err
	}

	var existingRole rbacv1.Role
	if err := r.Get(ctx, key, &existingRole); errors.IsNotFound(err) {
		log.Info("Creating Robin Role", "role", saName)
		if err := r.Create(ctx, desiredRole); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if !equality.Semantic.DeepEqual(existingRole.Rules, desiredRole.Rules) {
		log.Info("Robin Role drift detected, patching", "role", saName)
		base := existingRole.DeepCopy()
		existingRole.Rules = desiredRole.Rules
		if err := r.Patch(ctx, &existingRole, client.MergeFrom(base)); err != nil {
			return err
		}
	}

	// --- RoleBinding ---
	desiredRB := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: cluster.Namespace,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: cluster.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     saName,
		},
	}
	if err := controllerutil.SetControllerReference(cluster, desiredRB, r.Scheme); err != nil {
		return err
	}

	var existingRB rbacv1.RoleBinding
	if err := r.Get(ctx, key, &existingRB); errors.IsNotFound(err) {
		log.Info("Creating Robin RoleBinding", "rolebinding", saName)
		return r.Create(ctx, desiredRB)
	} else if err != nil {
		return err
	} else {
		needsRecreate := existingRB.RoleRef != desiredRB.RoleRef
		needsPatch := !equality.Semantic.DeepEqual(existingRB.Subjects, desiredRB.Subjects)

		if needsRecreate {
			// RoleRef is immutable — must delete and recreate
			log.Info("Robin RoleBinding RoleRef drift detected, recreating", "rolebinding", saName)
			if err := r.Delete(ctx, &existingRB); err != nil {
				return err
			}
			return r.Create(ctx, desiredRB)
		}
		if needsPatch {
			log.Info("Robin RoleBinding Subjects drift detected, patching", "rolebinding", saName)
			base := existingRB.DeepCopy()
			existingRB.Subjects = desiredRB.Subjects
			return r.Patch(ctx, &existingRB, client.MergeFrom(base))
		}
	}
	return nil
}

// ensureRobinDeployment ensures the Robin Deployment exists and matches the desired spec.
// If the Deployment already exists, it checks for spec drift and patches if needed.
func (r *RedkeyClusterReconciler) ensureRobinDeployment(ctx context.Context, cluster *redisv1.RedkeyCluster) error {
	log := logf.FromContext(ctx)
	desired := r.buildDesiredRobinDeployment(cluster)

	if err := controllerutil.SetControllerReference(cluster, desired, r.Scheme); err != nil {
		return err
	}

	var existing appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if errors.IsNotFound(err) {
		log.Info("Creating Robin Deployment", "deployment", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Check for spec drift and patch if needed
	if r.robinDeploymentNeedsUpdate(&existing, desired) {
		log.Info("Robin Deployment drift detected, patching", "deployment", desired.Name)
		base := existing.DeepCopy()
		existing.Labels = desired.Labels
		existing.Spec.Replicas = desired.Spec.Replicas
		existing.Spec.Template = desired.Spec.Template
		return r.Patch(ctx, &existing, client.MergeFrom(base))
	}

	return nil
}

// buildDesiredRobinDeployment constructs the desired Robin Deployment from the RedkeyCluster spec.
func (r *RedkeyClusterReconciler) buildDesiredRobinDeployment(cluster *redisv1.RedkeyCluster) *appsv1.Deployment {
	deployName := fmt.Sprintf("%s-robin", cluster.Name)
	saName := fmt.Sprintf("%s-robin", cluster.Name)
	replicas := int32(1)

	// Base labels (always present)
	deployLabels := map[string]string{
		ClusterLabel:                   cluster.Name,
		"app":                          "redkey-robin",
		"redkey.inditex.dev/component": "robin",
	}
	podLabels := map[string]string{
		ClusterLabel:                   cluster.Name,
		"app":                          "redkey-robin",
		"redkey.inditex.dev/component": "robin",
	}

	// Base container
	container := corev1.Container{
		Name:  "robin",
		Image: cluster.Spec.Robin.Image,
		Args: []string{
			"--cluster-name=$(CLUSTER_NAME)",
			"--namespace=$(NAMESPACE)",
		},
		Env: robinDefaultEnvVars(cluster),
		Ports: []corev1.ContainerPort{
			{
				Name:          "metrics",
				ContainerPort: 8080,
				Protocol:      corev1.ProtocolTCP,
			},
		},
	}

	// Apply first-level robin resources if present
	if cluster.Spec.Robin.Resources != nil {
		container.Resources = *cluster.Spec.Robin.Resources
	}

	// Pod spec defaults
	podSpec := corev1.PodSpec{
		ServiceAccountName: saName,
	}

	var podAnnotations map[string]string

	// Apply overrides from cluster.Spec.Robin.Template if present
	if cluster.Spec.Robin.Template != nil {
		tpl := cluster.Spec.Robin.Template

		// Pod-level metadata
		if len(tpl.Metadata.Labels) > 0 {
			maps.Copy(podLabels, tpl.Metadata.Labels)
		}
		if len(tpl.Metadata.Annotations) > 0 {
			podAnnotations = tpl.Metadata.Annotations
		}

		// Container overrides from the first container in the template
		if len(tpl.Spec.Containers) > 0 {
			src := tpl.Spec.Containers[0]
			if src.Image != "" {
				container.Image = src.Image
			}
			if src.Resources.Limits != nil || src.Resources.Requests != nil {
				container.Resources = src.Resources
			}
			if len(src.Env) > 0 {
				container.Env = mergeEnvVars(container.Env, src.Env)
			}
			if len(src.EnvFrom) > 0 {
				container.EnvFrom = src.EnvFrom
			}
			if len(src.VolumeMounts) > 0 {
				container.VolumeMounts = src.VolumeMounts
			}
			if src.SecurityContext != nil {
				container.SecurityContext = src.SecurityContext
			}
		}

		container.Env = mergeEnvVars(container.Env, robinDefaultEnvVars(cluster))

		// Pod-level spec overrides
		if len(tpl.Spec.NodeSelector) > 0 {
			podSpec.NodeSelector = tpl.Spec.NodeSelector
		}
		if len(tpl.Spec.Tolerations) > 0 {
			podSpec.Tolerations = tpl.Spec.Tolerations
		}
		if tpl.Spec.Affinity != nil {
			podSpec.Affinity = tpl.Spec.Affinity
		}
		if tpl.Spec.SecurityContext != nil {
			podSpec.SecurityContext = tpl.Spec.SecurityContext
		}
		if len(tpl.Spec.ImagePullSecrets) > 0 {
			podSpec.ImagePullSecrets = tpl.Spec.ImagePullSecrets
		}
		if tpl.Spec.PriorityClassName != "" {
			podSpec.PriorityClassName = tpl.Spec.PriorityClassName
		}
		if len(tpl.Spec.TopologySpreadConstraints) > 0 {
			podSpec.TopologySpreadConstraints = tpl.Spec.TopologySpreadConstraints
		}
		if len(tpl.Spec.Volumes) > 0 {
			podSpec.Volumes = tpl.Spec.Volumes
		}
	}

	podSpec.Containers = []corev1.Container{container}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: cluster.Namespace,
			Labels:    deployLabels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					ClusterLabel: cluster.Name,
					"app":        "redkey-robin",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: podAnnotations,
				},
				Spec: podSpec,
			},
		},
	}
}

// robinDeploymentNeedsUpdate returns true if the existing Deployment differs from the desired spec.
func (r *RedkeyClusterReconciler) robinDeploymentNeedsUpdate(existing, desired *appsv1.Deployment) bool {
	// Check replicas
	if existing.Spec.Replicas == nil || *existing.Spec.Replicas != *desired.Spec.Replicas {
		return true
	}

	// Check Deployment labels
	for k, v := range desired.Labels {
		if existing.Labels[k] != v {
			return true
		}
	}

	// Check PodTemplateSpec labels
	for k, v := range desired.Spec.Template.Labels {
		if existing.Spec.Template.Labels[k] != v {
			return true
		}
	}

	// Check PodTemplateSpec annotations
	for k, v := range desired.Spec.Template.Annotations {
		if existing.Spec.Template.Annotations[k] != v {
			return true
		}
	}

	// Use Semantic.DeepDerivative to compare only the fields explicitly set in our desired spec,
	// effectively ignoring defaults injected by Kubernetes (e.g. ServiceAccount volumes, pull policies).
	// This way we avoid triggering updates not desired by the user.
	if !equality.Semantic.DeepDerivative(desired.Spec.Template.Spec, existing.Spec.Template.Spec) {
		return true
	}

	return false
}

func robinDefaultEnvVars(cluster *redisv1.RedkeyCluster) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  "CLUSTER_NAME",
			Value: cluster.Name,
		},
		{
			Name:  "NAMESPACE",
			Value: cluster.Namespace,
		},
	}
}

func mergeEnvVars(base, overrides []corev1.EnvVar) []corev1.EnvVar {
	merged := append([]corev1.EnvVar(nil), base...)
	indexByName := make(map[string]int, len(merged))
	for index, envVar := range merged {
		indexByName[envVar.Name] = index
	}

	for _, envVar := range overrides {
		if index, exists := indexByName[envVar.Name]; exists {
			merged[index] = envVar
			continue
		}
		indexByName[envVar.Name] = len(merged)
		merged = append(merged, envVar)
	}

	return merged
}

// createIfNotExists creates the object if it doesn't already exist.
func (r *RedkeyClusterReconciler) createIfNotExists(ctx context.Context, obj client.Object) error {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, key, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, obj)
	}
	return err
}
