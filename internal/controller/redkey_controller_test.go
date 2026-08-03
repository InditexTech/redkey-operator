// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	redisv1 "github.com/inditextech/redkey-operator/api/v1beta1"
)

func getScheme() *runtime.Scheme {
	s := scheme.Scheme
	_ = redisv1.AddToScheme(s)
	return s
}

type listErrClient struct {
	client.Client
}

func (c *listErrClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return errors.New("list error")
}

type deleteErrClient struct {
	client.Client
}

func (c *deleteErrClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return errors.New("delete error")
}

type deploymentGetErrClient struct {
	client.Client
}

func (c *deploymentGetErrClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*appsv1.Deployment); ok {
		return errors.New("deployment get error")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

type configCreateErrClient struct {
	client.Client
}

func (c *configCreateErrClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*redisv1.RedkeyConfig); ok {
		return errors.New("create config error")
	}
	return c.Client.Create(ctx, obj, opts...)
}

type staticStatusWriter struct {
	updateErr error
}

func (w *staticStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return w.updateErr
}

func (w *staticStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return w.updateErr
}

func (w *staticStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return w.updateErr
}

func (w *staticStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return w.updateErr
}

type statusUpdateErrClient struct {
	client.Client
	updateErr error
}

func (c *statusUpdateErrClient) Status() client.SubResourceWriter {
	return &staticStatusWriter{updateErr: c.updateErr}
}

func TestRedkeyReconciler_NotFound(t *testing.T) {
	s := getScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	r := &RedkeyReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "does-not-exist",
			Namespace: "default",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.NoError(t, err)
	assert.Empty(t, res)
}

func TestRedkeyReconciler_GetError(t *testing.T) {
	s := getScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	r := &RedkeyReconciler{
		Client: &getErrClient{Client: fakeClient},
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "get-error",
			Namespace: "default",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get error")
	assert.Empty(t, res)
}

func TestRedkeyReconciler_EnsureRBACError(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.Redkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rbac-error",
			Namespace: "default",
		},
		Spec: redisv1.RedkeySpec{
			Primaries: 3,
			Ephemeral: true,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(cluster).Build()

	r := &RedkeyReconciler{
		Client: &badClient{Client: fakeClient},
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "rbac-error",
			Namespace: "default",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	assert.Empty(t, res)
}

func TestRedkeyReconciler_ListConfigsError(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.Redkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "list-error",
			Namespace: "default",
		},
		Spec: redisv1.RedkeySpec{
			Ephemeral: true,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(cluster).Build()

	r := &RedkeyReconciler{
		Client: &listErrClient{Client: fakeClient},
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "list-error",
			Namespace: "default",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list error")
	assert.Empty(t, res)
}

func TestRedkeyReconciler_CleanupSupersededConfigsDeleteError(t *testing.T) {
	s := getScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	r := &RedkeyReconciler{
		Client: &deleteErrClient{Client: fakeClient},
		Scheme: s,
	}

	configs := []redisv1.RedkeyConfig{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "config-1", Namespace: "default"},
			Spec:       redisv1.RedkeyConfigSpec{Sequence: 1},
			Status:     redisv1.RedkeyConfigStatus{ConfigPhase: redisv1.ConfigPhaseApplied},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "config-2", Namespace: "default"},
			Spec:       redisv1.RedkeyConfigSpec{Sequence: 2},
			Status:     redisv1.RedkeyConfigStatus{ConfigPhase: redisv1.ConfigPhaseApplied},
		},
	}

	_, err := r.cleanupSupersededConfigs(context.TODO(), configs)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete error")
}

func TestRedkeyReconciler_EnsureRobinDeploymentError(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.Redkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deployment-error",
			Namespace: "default",
		},
		Spec: redisv1.RedkeySpec{
			Primaries: 3,
			Ephemeral: true,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(cluster).Build()

	r := &RedkeyReconciler{
		Client: &deploymentGetErrClient{Client: fakeClient},
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "deployment-error",
			Namespace: "default",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deployment get error")
	assert.Empty(t, res)
}

func TestRedkeyReconciler_CreateNewConfigError(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.Redkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "config-create-error",
			Namespace: "default",
		},
		Spec: redisv1.RedkeySpec{
			Primaries: 3,
			Ephemeral: true,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(cluster).Build()

	r := &RedkeyReconciler{
		Client: &configCreateErrClient{Client: fakeClient},
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "config-create-error",
			Namespace: "default",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create config error")
	assert.Empty(t, res)
}

func TestRedkeyReconciler_AggregateStatusError(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.Redkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "status-error",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: redisv1.RedkeySpec{
			Primaries: 3,
			Ephemeral: true,
		},
	}
	config := &redisv1.RedkeyConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "status-error-1",
			Namespace: "default",
			Labels: map[string]string{
				ClusterLabel: "status-error",
			},
			Annotations: map[string]string{
				"redkey.inditex.dev/cluster-generation": "1",
			},
		},
		Spec: redisv1.RedkeyConfigSpec{Sequence: 1},
		Status: redisv1.RedkeyConfigStatus{
			ConfigPhase: redisv1.ConfigPhaseApplied,
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(cluster, config).Build()

	r := &RedkeyReconciler{
		Client: &statusUpdateErrClient{Client: fakeClient, updateErr: errors.New("status update error")},
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "status-error",
			Namespace: "default",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status update error")
	assert.Empty(t, res)

	var roleBinding rbacv1.RoleBinding
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "status-error-robin", Namespace: "default"}, &roleBinding)
	require.NoError(t, err)
}

func TestRedkeyReconciler_ControllerOptions(t *testing.T) {
	t.Run("positive value is propagated", func(t *testing.T) {
		r := &RedkeyReconciler{MaxConcurrentReconciles: 10}
		assert.Equal(t, 10, r.controllerOptions().MaxConcurrentReconciles)
	})

	t.Run("zero falls back to controller-runtime default", func(t *testing.T) {
		r := &RedkeyReconciler{MaxConcurrentReconciles: 0}
		// Leaving it at 0 lets controller-runtime apply its own default (1).
		assert.Equal(t, 0, r.controllerOptions().MaxConcurrentReconciles)
	})

	t.Run("negative falls back to controller-runtime default", func(t *testing.T) {
		r := &RedkeyReconciler{MaxConcurrentReconciles: -5}
		assert.Equal(t, 0, r.controllerOptions().MaxConcurrentReconciles)
	})
}
