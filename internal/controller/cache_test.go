// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	redisv1 "github.com/inditextech/redkey-operator/api/v1beta1"
)

func TestConfigureCache_ScopesOwnedTypesByRobinLabel(t *testing.T) {
	opts := cache.Options{
		DefaultNamespaces: map[string]cache.Config{"ns1": {}},
	}
	ConfigureCache(&opts)

	assert.Contains(t, opts.DefaultNamespaces, "ns1", "namespace scoping must be preserved")
	require.Len(t, opts.ByObject, 4)

	kinds := map[string]bool{}
	for obj, byObj := range opts.ByObject {
		kinds[typeName(obj)] = true
		require.NotNil(t, byObj.Label, "%s must have a label selector", typeName(obj))
		assert.True(t, byObj.Label.Matches(labels.Set{ComponentLabel: RobinComponent, ClusterLabel: "c"}))
		assert.False(t, byObj.Label.Matches(labels.Set{ComponentLabel: "redis"}))
		assert.False(t, byObj.Label.Matches(labels.Set{"app": "other"}))
	}
	assert.Equal(t, map[string]bool{
		"Deployment": true, "ServiceAccount": true, "Role": true, "RoleBinding": true,
	}, kinds)

	require.NotNil(t, opts.DefaultTransform)
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name:          "x",
		ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
	}}
	out, err := opts.DefaultTransform(sa)
	require.NoError(t, err)
	assert.Empty(t, out.(*corev1.ServiceAccount).ManagedFields)
}

func TestRobinObjectsMatchCacheSelector(t *testing.T) {
	userLabels := map[string]string{ComponentLabel: "hijacked"}
	cluster := &redisv1.Redkey{
		ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "default", UID: "uid"},
		Spec:       redisv1.RedkeySpec{Labels: &userLabels},
	}
	s := getScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	r := &RedkeyReconciler{Client: fakeClient, Scheme: s}
	ctx := context.Background()
	require.NoError(t, r.ensureRBAC(ctx, cluster))
	require.NoError(t, r.ensureRobinDeployment(ctx, cluster))

	selector := RobinObjectSelector()
	key := types.NamespacedName{Name: "c-robin", Namespace: "default"}
	for _, obj := range []client.Object{
		&appsv1.Deployment{}, &corev1.ServiceAccount{}, &rbacv1.Role{}, &rbacv1.RoleBinding{},
	} {
		require.NoError(t, fakeClient.Get(ctx, key, obj))
		assert.True(t, selector.Matches(labels.Set(obj.GetLabels())),
			"%s created by the operator must be visible to the label-scoped cache", typeName(obj))
	}
}

// labelScopedClient simulates the label-scoped cache: objects without the Robin
// component label are reported as NotFound, as the real informer would never see them.
type labelScopedClient struct {
	client.Client
}

func (c *labelScopedClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := c.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if !RobinObjectSelector().Matches(labels.Set(obj.GetLabels())) {
		return apierrors.NewNotFound(schema.GroupResource{}, key.Name)
	}
	return nil
}

func unlabelledRobinObjects() []client.Object {
	meta := metav1.ObjectMeta{Name: "c-robin", Namespace: "default"}
	return []client.Object{
		&corev1.ServiceAccount{ObjectMeta: meta},
		&rbacv1.Role{ObjectMeta: meta},
		&rbacv1.RoleBinding{
			ObjectMeta: meta,
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "c-robin"},
		},
		&appsv1.Deployment{ObjectMeta: meta},
	}
}

func TestEnsureRobinObjects_AdoptsUnlabelledObjectsViaAPIReader(t *testing.T) {
	s := getScheme()
	apiClient := fake.NewClientBuilder().WithScheme(s).WithObjects(unlabelledRobinObjects()...).Build()
	r := &RedkeyReconciler{
		Client:    &labelScopedClient{Client: apiClient},
		APIReader: apiClient,
		Scheme:    s,
	}
	cluster := &redisv1.Redkey{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "default", UID: "uid"}}
	ctx := context.Background()

	require.NoError(t, r.ensureRBAC(ctx, cluster))
	require.NoError(t, r.ensureRobinDeployment(ctx, cluster))

	key := types.NamespacedName{Name: "c-robin", Namespace: "default"}
	for _, obj := range []client.Object{
		&appsv1.Deployment{}, &corev1.ServiceAccount{}, &rbacv1.Role{}, &rbacv1.RoleBinding{},
	} {
		require.NoError(t, apiClient.Get(ctx, key, obj))
		assert.Equal(t, RobinComponent, obj.GetLabels()[ComponentLabel],
			"%s must be relabelled so it is cached again", typeName(obj))
	}
}

func TestEnsureRBAC_WithoutAPIReaderFailsOnUnlabelledObjects(t *testing.T) {
	s := getScheme()
	apiClient := fake.NewClientBuilder().WithScheme(s).WithObjects(unlabelledRobinObjects()...).Build()
	r := &RedkeyReconciler{Client: &labelScopedClient{Client: apiClient}, Scheme: s}
	cluster := &redisv1.Redkey{ObjectMeta: metav1.ObjectMeta{Name: "c", Namespace: "default", UID: "uid"}}

	err := r.ensureRBAC(context.Background(), cluster)
	assert.True(t, apierrors.IsAlreadyExists(err), "expected AlreadyExists, got %v", err)
}

func typeName(obj client.Object) string {
	switch obj.(type) {
	case *appsv1.Deployment:
		return "Deployment"
	case *corev1.ServiceAccount:
		return "ServiceAccount"
	case *rbacv1.Role:
		return "Role"
	case *rbacv1.RoleBinding:
		return "RoleBinding"
	}
	return "unknown"
}
