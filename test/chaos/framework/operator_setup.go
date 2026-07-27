// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

const (
	operatorName           = "redkey-operator"
	operatorServiceAccount = "redkey-operator-sa"
	operatorRoleName       = "redkey-operator-role"
)

// EnsureOperatorSetup deploys a per-namespace operator scoped to watch only the
// given namespace. It creates the ServiceAccount, a namespaced Role + RoleBinding
// (derived from the cluster-scoped manager-role) and the operator Deployment.
//
// The operator is configured with --watch-namespaces=<namespace> so its cache and
// reconcilers only act on resources inside that namespace, isolating chaos tests
// that run in parallel namespaces. Leader election is disabled to keep restarts
// fast during fault injection.
func EnsureOperatorSetup(ctx context.Context, clientset kubernetes.Interface, namespace string) error {
	if err := ensureServiceAccount(ctx, clientset, newServiceAccount(namespace)); err != nil {
		return fmt.Errorf("ensure ServiceAccount: %w", err)
	}
	if err := ensureRole(ctx, clientset, newRole(namespace, operatorRoleName, operatorPolicyRules())); err != nil {
		return fmt.Errorf("ensure %s: %w", operatorRoleName, err)
	}
	if err := ensureRoleBinding(
		ctx, clientset,
		newRoleBinding(namespace, "redkey-operator-rolebinding", operatorRoleName),
	); err != nil {
		return fmt.Errorf("ensure redkey-operator-rolebinding: %w", err)
	}
	if err := ensureDeployment(ctx, clientset, newOperatorDeployment(namespace)); err != nil {
		return fmt.Errorf("ensure Deployment: %w", err)
	}
	return nil
}

func ensureServiceAccount(ctx context.Context, clientset kubernetes.Interface, desired *corev1.ServiceAccount) error {
	existing, err := clientset.CoreV1().ServiceAccounts(desired.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = clientset.CoreV1().ServiceAccounts(desired.Namespace).Create(ctx, desired, metav1.CreateOptions{})
			return err
		}
		return err
	}
	desired.ResourceVersion = existing.ResourceVersion
	desired.Secrets = existing.Secrets
	desired.ImagePullSecrets = existing.ImagePullSecrets
	desired.AutomountServiceAccountToken = existing.AutomountServiceAccountToken
	_, err = clientset.CoreV1().ServiceAccounts(desired.Namespace).Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func ensureRole(ctx context.Context, clientset kubernetes.Interface, desired *rbacv1.Role) error {
	existing, err := clientset.RbacV1().Roles(desired.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = clientset.RbacV1().Roles(desired.Namespace).Create(ctx, desired, metav1.CreateOptions{})
			return err
		}
		return err
	}
	desired.ResourceVersion = existing.ResourceVersion
	_, err = clientset.RbacV1().Roles(desired.Namespace).Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func ensureRoleBinding(ctx context.Context, clientset kubernetes.Interface, desired *rbacv1.RoleBinding) error {
	existing, err := clientset.RbacV1().RoleBindings(desired.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = clientset.RbacV1().RoleBindings(desired.Namespace).Create(ctx, desired, metav1.CreateOptions{})
			return err
		}
		return err
	}
	desired.ResourceVersion = existing.ResourceVersion
	_, err = clientset.RbacV1().RoleBindings(desired.Namespace).Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func ensureDeployment(ctx context.Context, clientset kubernetes.Interface, desired *appsv1.Deployment) error {
	existing, err := clientset.AppsV1().Deployments(desired.Namespace).Get(ctx, desired.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = clientset.AppsV1().Deployments(desired.Namespace).Create(ctx, desired, metav1.CreateOptions{})
			return err
		}
		return err
	}
	desired.ResourceVersion = existing.ResourceVersion
	_, err = clientset.AppsV1().Deployments(desired.Namespace).Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func newServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: operatorServiceAccount, Namespace: ns},
	}
}

func newRole(ns, name string, rules []rbacv1.PolicyRule) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Rules:      rules,
	}
}

func newRoleBinding(ns, name, roleName string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: roleName},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: operatorServiceAccount, Namespace: ns}},
	}
}

func newOperatorDeployment(ns string) *appsv1.Deployment {
	labels := map[string]string{"control-plane": operatorName}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorName,
			Namespace: ns,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName:            operatorServiceAccount,
					TerminationGracePeriodSeconds: ptr.To(int64(10)),
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot:   ptr.To(true),
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
					},
					Containers: []corev1.Container{newOperatorContainer(ns)},
				},
			},
		},
	}
}

func newOperatorContainer(ns string) corev1.Container {
	return corev1.Container{
		Name:    "manager",
		Image:   GetOperatorImage(),
		Command: []string{"/manager"},
		Args: []string{
			fmt.Sprintf("--watch-namespaces=%s", ns),
			"--health-probe-bind-address=:8081",
			"--metrics-bind-address=0",
			"--max-concurrent-reconciles=10",
		},
		ImagePullPolicy: corev1.PullIfNotPresent,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromInt(8081)},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       20,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromInt(8081)},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
		},
	}
}

// operatorPolicyRules mirrors config/rbac/role.yaml (ClusterRole manager-role)
// reduced to a namespaced Role. Events permissions are added so the operator can
// emit events without a cluster-scoped binding.
func operatorPolicyRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"configmaps", "serviceaccounts", "services"},
			Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"persistentvolumeclaims"},
			Verbs:     []string{"delete", "list"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"delete", "get", "list", "watch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"secrets"},
			Verbs:     []string{"get"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs:     []string{"create", "patch"},
		},
		{
			APIGroups: []string{"apps"},
			Resources: []string{"deployments", "statefulsets"},
			Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		{
			APIGroups: []string{"policy"},
			Resources: []string{"poddisruptionbudgets"},
			Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{"rolebindings", "roles"},
			Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		{
			APIGroups: []string{"redkey.inditex.dev"},
			Resources: []string{"redkeyconfigs"},
			Verbs:     []string{"create", "delete", "get", "list", "watch"},
		},
		{
			APIGroups: []string{"redkey.inditex.dev"},
			Resources: []string{"redkeyconfigs/status", "redkeys/scale", "redkeys/status"},
			Verbs:     []string{"get", "patch", "update"},
		},
		{
			APIGroups: []string{"redkey.inditex.dev"},
			Resources: []string{"redkeys"},
			Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		{
			APIGroups: []string{"redkey.inditex.dev"},
			Resources: []string{"redkeys/finalizers"},
			Verbs:     []string{"update"},
		},
	}
}
