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

	redisv1 "github.com/inditextech/redkey-operator/api/v1beta1"
)

// derefMap returns the map pointed to by m, or nil when m is nil. It is a
// convenience for the optional spec.labels / spec.annotations pointer fields.
func derefMap(m *map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	return *m
}

// mergeMeta computes the final labels (or annotations) for a managed object by
// combining three sources with a fixed precedence:
//
//  1. spec: the user-provided spec.labels / spec.annotations from the Redkey.
//  2. override: labels / annotations defined in an override block (e.g. the Robin
//     pod template metadata). When the override defines any entry it fully REPLACES
//     the spec source (block replacement); the spec entries are discarded.
//  3. base: the internal labels / annotations Redkey requires for correct operation
//     (cluster identity / selector labels, generation annotation, ...). These always
//     win and are applied last so user input can never shadow them.
//
// The result is a freshly allocated map (independent per call), or nil when the
// combined result would be empty.
func mergeMeta(spec, override, base map[string]string) map[string]string {
	out := make(map[string]string, len(spec)+len(override)+len(base))
	if len(override) > 0 {
		maps.Copy(out, override)
	} else {
		maps.Copy(out, spec)
	}
	maps.Copy(out, base)
	if len(out) == 0 {
		return nil
	}
	return out
}

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
			Verbs:     []string{"get", "list", "watch", "delete"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"persistentvolumeclaims"},
			Verbs:     []string{"list", "delete"},
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
			Resources: []string{"redkeys"},
			Verbs:     []string{"get"},
		},
		{
			APIGroups: []string{"redkey.inditex.dev"},
			Resources: []string{"redkeyconfigs"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{"redkey.inditex.dev"},
			Resources: []string{"redkeyconfigs/status"},
			Verbs:     []string{"update", "patch"},
		},
	}
}

// ensureRBAC ensures the ServiceAccount, Role, and RoleBinding for Robin exist
// in the cluster's namespace and match the desired state. If any resource is
// missing it is created; if it has drifted from the desired spec it is patched
// (or, for RoleBinding whose RoleRef is immutable, deleted and recreated).
func (r *RedkeyReconciler) ensureRBAC(ctx context.Context, cluster *redisv1.Redkey) error {
	log := logf.FromContext(ctx)
	saName := fmt.Sprintf("%s-robin", cluster.Name)
	key := types.NamespacedName{Name: saName, Namespace: cluster.Namespace}

	// Internal base labels for Robin-owned RBAC objects. spec.labels / spec.annotations
	// decorate the objects, but the internal labels always win on collision.
	rbacBaseLabels := map[string]string{
		ClusterLabel:                   cluster.Name,
		"redkey.inditex.dev/component": "robin",
	}
	rbacLabels := mergeMeta(derefMap(cluster.Spec.Labels), nil, rbacBaseLabels)
	rbacAnnotations := mergeMeta(derefMap(cluster.Spec.Annotations), nil, nil)

	// --- ServiceAccount ---
	desiredSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        saName,
			Namespace:   cluster.Namespace,
			Labels:      rbacLabels,
			Annotations: rbacAnnotations,
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
	} else if !equality.Semantic.DeepEqual(existingSA.Labels, desiredSA.Labels) ||
		!equality.Semantic.DeepEqual(existingSA.Annotations, desiredSA.Annotations) {
		log.Info("Robin ServiceAccount metadata drift detected, patching", "serviceaccount", saName)
		base := existingSA.DeepCopy()
		existingSA.Labels = desiredSA.Labels
		existingSA.Annotations = desiredSA.Annotations
		if err := r.Patch(ctx, &existingSA, client.MergeFrom(base)); err != nil {
			return err
		}
	}

	// --- Role ---
	desiredRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:        saName,
			Namespace:   cluster.Namespace,
			Labels:      rbacLabels,
			Annotations: rbacAnnotations,
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
	} else if !equality.Semantic.DeepEqual(existingRole.Rules, desiredRole.Rules) ||
		!equality.Semantic.DeepEqual(existingRole.Labels, desiredRole.Labels) ||
		!equality.Semantic.DeepEqual(existingRole.Annotations, desiredRole.Annotations) {
		log.Info("Robin Role drift detected, patching", "role", saName)
		base := existingRole.DeepCopy()
		existingRole.Rules = desiredRole.Rules
		existingRole.Labels = desiredRole.Labels
		existingRole.Annotations = desiredRole.Annotations
		if err := r.Patch(ctx, &existingRole, client.MergeFrom(base)); err != nil {
			return err
		}
	}

	// --- RoleBinding ---
	desiredRB := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        saName,
			Namespace:   cluster.Namespace,
			Labels:      rbacLabels,
			Annotations: rbacAnnotations,
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
		needsPatch := !equality.Semantic.DeepEqual(existingRB.Subjects, desiredRB.Subjects) ||
			!equality.Semantic.DeepEqual(existingRB.Labels, desiredRB.Labels) ||
			!equality.Semantic.DeepEqual(existingRB.Annotations, desiredRB.Annotations)

		if needsRecreate {
			// RoleRef is immutable — must delete and recreate
			log.Info("Robin RoleBinding RoleRef drift detected, recreating", "rolebinding", saName)
			if err := r.Delete(ctx, &existingRB); err != nil {
				return err
			}
			return r.Create(ctx, desiredRB)
		}
		if needsPatch {
			log.Info("Robin RoleBinding drift detected, patching", "rolebinding", saName)
			base := existingRB.DeepCopy()
			existingRB.Subjects = desiredRB.Subjects
			existingRB.Labels = desiredRB.Labels
			existingRB.Annotations = desiredRB.Annotations
			return r.Patch(ctx, &existingRB, client.MergeFrom(base))
		}
	}
	return nil
}

// ensureRobinDeployment ensures the Robin Deployment exists and matches the desired spec.
// If the Deployment already exists, it checks for spec drift and patches if needed.
func (r *RedkeyReconciler) ensureRobinDeployment(ctx context.Context, cluster *redisv1.Redkey) error {
	log := logf.FromContext(ctx)
	desired := r.buildDesiredRobinDeployment(cluster)

	if err := controllerutil.SetControllerReference(cluster, desired, r.Scheme); err != nil {
		return err
	}

	var existing appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if errors.IsNotFound(err) {
		log.Info("Creating Robin Deployment", "deployment", desired.Name)
		if err := r.Create(ctx, desired); err != nil {
			return err
		}
		redkeyRobinDeploymentChanges.WithLabelValues("create").Inc()
		return nil
	}
	if err != nil {
		return err
	}

	// Check for spec drift and patch if needed
	if r.robinDeploymentNeedsUpdate(&existing, desired) {
		log.Info("Robin Deployment drift detected, patching", "deployment", desired.Name)
		base := existing.DeepCopy()
		existing.Labels = desired.Labels
		// Preserve Kubernetes-managed annotations (e.g. deployment.kubernetes.io/revision)
		// so the patch does not strip them; otherwise the Deployment controller would
		// re-add them and the operator would patch again in an endless loop.
		existing.Annotations = preserveManagedAnnotations(existing.Annotations, desired.Annotations)
		existing.Spec.Replicas = desired.Spec.Replicas
		existing.Spec.Template = desired.Spec.Template
		if err := r.Patch(ctx, &existing, client.MergeFrom(base)); err != nil {
			return err
		}
		redkeyRobinDeploymentChanges.WithLabelValues("patch").Inc()
		return nil
	}

	return nil
}

// buildDesiredRobinDeployment constructs the desired Robin Deployment from the Redkey spec.
func (r *RedkeyReconciler) buildDesiredRobinDeployment(cluster *redisv1.Redkey) *appsv1.Deployment {
	deployName := fmt.Sprintf("%s-robin", cluster.Name)
	saName := fmt.Sprintf("%s-robin", cluster.Name)
	replicas := int32(1)

	// Base labels (always present, internal — they always win over user input and
	// double as the Deployment / pod selector labels).
	baseLabels := map[string]string{
		ClusterLabel:                   cluster.Name,
		"redkey.inditex.dev/component": "robin",
	}

	specLabels := derefMap(cluster.Spec.Labels)
	specAnnotations := derefMap(cluster.Spec.Annotations)

	// The Robin pod template metadata (spec.robin.template.metadata) acts as an
	// override for the Robin pod: when set it takes precedence over spec.labels /
	// spec.annotations (block replacement). Internal base labels still win.
	var tplLabels, tplAnnotations map[string]string
	if cluster.Spec.Robin.Template != nil {
		tplLabels = cluster.Spec.Robin.Template.Metadata.Labels
		tplAnnotations = cluster.Spec.Robin.Template.Metadata.Annotations
	}

	deployLabels := mergeMeta(specLabels, nil, baseLabels)
	deployAnnotations := mergeMeta(specAnnotations, nil, nil)
	podLabels := mergeMeta(specLabels, tplLabels, baseLabels)
	podAnnotations := mergeMeta(specAnnotations, tplAnnotations, nil)

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

	// Apply overrides from cluster.Spec.Robin.Template if present
	if cluster.Spec.Robin.Template != nil {
		tpl := cluster.Spec.Robin.Template

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
			Name:        deployName,
			Namespace:   cluster.Namespace,
			Labels:      deployLabels,
			Annotations: deployAnnotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					ClusterLabel:                   cluster.Name,
					"redkey.inditex.dev/component": "robin",
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

// deploymentRevisionAnnotation is the annotation the Kubernetes Deployment
// controller stamps on every Deployment to track its current ReplicaSet
// revision. It is managed by Kubernetes, not by the operator.
const deploymentRevisionAnnotation = "deployment.kubernetes.io/revision"

// stripManagedAnnotations returns a copy of annotations without the
// Kubernetes-managed keys that the operator must not fight over. It returns nil
// when the result would be empty so it compares cleanly against an unset map.
func stripManagedAnnotations(annotations map[string]string) map[string]string {
	if _, ok := annotations[deploymentRevisionAnnotation]; !ok {
		return annotations
	}
	out := make(map[string]string, len(annotations))
	for k, v := range annotations {
		if k == deploymentRevisionAnnotation {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// preserveManagedAnnotations returns the desired annotations with any
// Kubernetes-managed annotation carried over from existing, so a patch does not
// strip annotations owned by the Deployment controller.
func preserveManagedAnnotations(existing, desired map[string]string) map[string]string {
	rev, ok := existing[deploymentRevisionAnnotation]
	if !ok {
		return desired
	}
	out := make(map[string]string, len(desired)+1)
	maps.Copy(out, desired)
	out[deploymentRevisionAnnotation] = rev
	return out
}

// robinDeploymentNeedsUpdate returns true if the existing Deployment differs from the desired spec.
func (r *RedkeyReconciler) robinDeploymentNeedsUpdate(existing, desired *appsv1.Deployment) bool {
	// Check replicas
	if existing.Spec.Replicas == nil || *existing.Spec.Replicas != *desired.Spec.Replicas {
		return true
	}

	// Compare labels and annotations with DeepEqual so that REMOVED keys (not just
	// added/changed ones) trigger an update and get pruned from the live object.
	if !equality.Semantic.DeepEqual(existing.Labels, desired.Labels) {
		return true
	}
	// Ignore Kubernetes-managed annotations (e.g. deployment.kubernetes.io/revision)
	// when comparing, otherwise the operator would detect false drift on every
	// reconcile and patch the Deployment in an endless loop.
	if !equality.Semantic.DeepEqual(stripManagedAnnotations(existing.Annotations), desired.Annotations) {
		return true
	}
	if !equality.Semantic.DeepEqual(existing.Spec.Template.Labels, desired.Spec.Template.Labels) {
		return true
	}
	if !equality.Semantic.DeepEqual(existing.Spec.Template.Annotations, desired.Spec.Template.Annotations) {
		return true
	}

	// Use Semantic.DeepDerivative to compare only the fields explicitly set in our desired spec,
	// effectively ignoring defaults injected by Kubernetes (e.g. ServiceAccount volumes, pull policies).
	// This way we avoid triggering updates not desired by the user.
	if !equality.Semantic.DeepDerivative(desired.Spec.Template.Spec, existing.Spec.Template.Spec) {
		return true
	}

	return false
}

func robinDefaultEnvVars(cluster *redisv1.Redkey) []corev1.EnvVar {
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
func (r *RedkeyReconciler) createIfNotExists(ctx context.Context, obj client.Object) error {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, key, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, obj)
	}
	return err
}
