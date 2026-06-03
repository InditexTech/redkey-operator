// SPDX-FileCopyrightText: 2026 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

// TestReconcileScaleToZero_NewClusterZeroPrimaries verifies that creating a RKCL with
// primaries=0 does NOT create any RKCC, Robin Deployment, or RBAC resources.
func TestReconcileScaleToZero_NewClusterZeroPrimaries(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "zero-new",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 0,
			Ephemeral: true,
			Robin:     redisv1.RobinSpec{Image: "robin:latest"},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &RedkeyClusterReconciler{
		Client:         fakeClient,
		Scheme:         s,
		ResyncInterval: 30 * time.Second,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "zero-new", Namespace: "default"}}
	res, err := r.Reconcile(context.TODO(), req)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, res.RequeueAfter)

	// Verify no configs were created
	var configs redisv1.RedkeyClusterConfigList
	err = fakeClient.List(context.TODO(), &configs, client.InNamespace("default"))
	require.NoError(t, err)
	assert.Empty(t, configs.Items)

	// Verify no deployment was created
	var deploy appsv1.Deployment
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "zero-new-robin", Namespace: "default"}, &deploy)
	assert.True(t, client.IgnoreNotFound(err) == nil && err != nil, "Robin deployment should not exist")

	// Verify no ServiceAccount was created
	var sa corev1.ServiceAccount
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "zero-new-robin", Namespace: "default"}, &sa)
	assert.True(t, client.IgnoreNotFound(err) == nil && err != nil, "ServiceAccount should not exist")

	// Verify status is updated
	var updated redisv1.RedkeyCluster
	err = fakeClient.Get(context.TODO(), req.NamespacedName, &updated)
	require.NoError(t, err)
	assert.Equal(t, redisv1.PhaseReady, updated.Status.Phase)
	assert.Equal(t, int32(0), updated.Status.Replicas)
	assert.Empty(t, updated.Status.Nodes)
}

// TestReconcileScaleToZero_CreatesConfig verifies that when an existing cluster (with configs)
// is scaled to 0, the operator creates a new RKCC with primaries=0 while keeping Robin alive.
func TestReconcileScaleToZero_CreatesConfig(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "scale-down",
			Namespace:  "default",
			Generation: 2,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 0,
			Ephemeral: true,
			Robin:     redisv1.RobinSpec{Image: "robin:latest"},
		},
	}
	// Existing config with primaries=3
	existingConfig := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scale-down-1",
			Namespace: "default",
			Labels:    map[string]string{ClusterLabel: "scale-down"},
			Annotations: map[string]string{
				"redkey.inditex.dev/cluster-generation": "1",
			},
		},
		Spec: redisv1.RedkeyClusterConfigSpec{
			Sequence:  1,
			Primaries: 3,
			Ephemeral: true,
		},
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhaseApplied,
			Status:      redisv1.ClusterStatusReady,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).
		WithObjects(cluster, existingConfig).
		WithStatusSubresource(cluster, existingConfig).
		Build()

	r := &RedkeyClusterReconciler{
		Client:         fakeClient,
		Scheme:         s,
		ResyncInterval: 30 * time.Second,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "scale-down", Namespace: "default"}}
	res, err := r.Reconcile(context.TODO(), req)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, res.RequeueAfter)

	// Verify a new config with primaries=0 was created
	var configs redisv1.RedkeyClusterConfigList
	err = fakeClient.List(context.TODO(), &configs, client.InNamespace("default"), client.MatchingLabels{ClusterLabel: "scale-down"})
	require.NoError(t, err)
	assert.Len(t, configs.Items, 2)

	// Find the new config (sequence 2)
	var newConfig *redisv1.RedkeyClusterConfig
	for i := range configs.Items {
		if configs.Items[i].Spec.Sequence == 2 {
			newConfig = &configs.Items[i]
			break
		}
	}
	require.NotNil(t, newConfig, "Should have created a config with sequence 2")
	assert.Equal(t, int32(0), newConfig.Spec.Primaries)

	// Verify Robin deployment exists (needed to process the config)
	var deploy appsv1.Deployment
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "scale-down-robin", Namespace: "default"}, &deploy)
	require.NoError(t, err)

	// Verify RBAC exists
	var role rbacv1.Role
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "scale-down-robin", Namespace: "default"}, &role)
	require.NoError(t, err)
}

// TestReconcileScaleToZero_WaitsForApplied verifies that when a config with primaries=0
// exists but is InProgress, the operator keeps Robin alive and doesn't clean up yet.
func TestReconcileScaleToZero_WaitsForApplied(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "waiting",
			Namespace:  "default",
			Generation: 2,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 0,
			Ephemeral: true,
			Robin:     redisv1.RobinSpec{Image: "robin:latest"},
		},
	}
	// Config with primaries=0 but still InProgress
	config := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "waiting-2",
			Namespace: "default",
			Labels:    map[string]string{ClusterLabel: "waiting"},
			Annotations: map[string]string{
				"redkey.inditex.dev/cluster-generation": "2",
			},
		},
		Spec: redisv1.RedkeyClusterConfigSpec{
			Sequence:  2,
			Primaries: 0,
			Ephemeral: true,
		},
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhaseInProgress,
			Status:      redisv1.ClusterStatusScalingToZero,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).
		WithObjects(cluster, config).
		WithStatusSubresource(cluster, config).
		Build()

	r := &RedkeyClusterReconciler{
		Client:         fakeClient,
		Scheme:         s,
		ResyncInterval: 30 * time.Second,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "waiting", Namespace: "default"}}
	res, err := r.Reconcile(context.TODO(), req)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, res.RequeueAfter)

	// Robin deployment should exist (operator ensures it's running)
	var deploy appsv1.Deployment
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "waiting-robin", Namespace: "default"}, &deploy)
	require.NoError(t, err)

	// Config should still exist
	var existingConfig redisv1.RedkeyClusterConfig
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "waiting-2", Namespace: "default"}, &existingConfig)
	require.NoError(t, err)
}

// TestReconcileScaleToZero_CleansUpAfterApplied verifies that once Robin marks the
// primaries=0 config as Applied, the operator deletes Deployment, RBAC, and all configs.
func TestReconcileScaleToZero_CleansUpAfterApplied(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cleanup",
			Namespace:  "default",
			Generation: 2,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 0,
			Ephemeral: true,
			Robin:     redisv1.RobinSpec{Image: "robin:latest"},
		},
	}
	// Config with primaries=0, Applied by Robin
	config := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cleanup-2",
			Namespace: "default",
			Labels:    map[string]string{ClusterLabel: "cleanup"},
			Annotations: map[string]string{
				"redkey.inditex.dev/cluster-generation": "2",
			},
		},
		Spec: redisv1.RedkeyClusterConfigSpec{
			Sequence:  2,
			Primaries: 0,
			Ephemeral: true,
		},
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhaseApplied,
			Status:      redisv1.ClusterStatusReady,
		},
	}
	// Robin Deployment
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cleanup-robin",
			Namespace: "default",
		},
	}
	// RBAC resources
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cleanup-robin",
			Namespace: "default",
		},
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cleanup-robin",
			Namespace: "default",
		},
	}
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cleanup-robin",
			Namespace: "default",
		},
		RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "cleanup-robin"},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).
		WithObjects(cluster, config, deploy, sa, role, rb).
		WithStatusSubresource(cluster, config).
		Build()

	r := &RedkeyClusterReconciler{
		Client:         fakeClient,
		Scheme:         s,
		ResyncInterval: 30 * time.Second,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "cleanup", Namespace: "default"}}
	res, err := r.Reconcile(context.TODO(), req)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, res.RequeueAfter)

	// Verify deployment was deleted
	var d appsv1.Deployment
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "cleanup-robin", Namespace: "default"}, &d)
	assert.Error(t, err, "Deployment should be deleted")

	// Verify RBAC deleted
	var s2 corev1.ServiceAccount
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "cleanup-robin", Namespace: "default"}, &s2)
	assert.Error(t, err, "ServiceAccount should be deleted")

	var r2 rbacv1.Role
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "cleanup-robin", Namespace: "default"}, &r2)
	assert.Error(t, err, "Role should be deleted")

	var rb2 rbacv1.RoleBinding
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "cleanup-robin", Namespace: "default"}, &rb2)
	assert.Error(t, err, "RoleBinding should be deleted")

	// Verify configs deleted
	var configs redisv1.RedkeyClusterConfigList
	err = fakeClient.List(context.TODO(), &configs, client.InNamespace("default"), client.MatchingLabels{ClusterLabel: "cleanup"})
	require.NoError(t, err)
	assert.Empty(t, configs.Items)

	// Verify status updated
	var updated redisv1.RedkeyCluster
	err = fakeClient.Get(context.TODO(), req.NamespacedName, &updated)
	require.NoError(t, err)
	assert.Equal(t, redisv1.PhaseReady, updated.Status.Phase)
	assert.Equal(t, int32(0), updated.Status.Replicas)
}

// TestReconcileScaleToZero_SteadyState verifies that once cleanup is done, subsequent
// reconciliations are no-ops that just ensure status stays correct.
func TestReconcileScaleToZero_SteadyState(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "steady",
			Namespace:  "default",
			Generation: 2,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 0,
			Ephemeral: true,
			Robin:     redisv1.RobinSpec{Image: "robin:latest"},
		},
		Status: redisv1.RedkeyClusterStatus{
			Phase:    redisv1.PhaseReady,
			Replicas: 0,
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &RedkeyClusterReconciler{
		Client:         fakeClient,
		Scheme:         s,
		ResyncInterval: 30 * time.Second,
	}

	// Run reconcile twice — should be idempotent
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "steady", Namespace: "default"}}
	res, err := r.Reconcile(context.TODO(), req)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, res.RequeueAfter)

	res, err = r.Reconcile(context.TODO(), req)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, res.RequeueAfter)

	// Verify no configs were created
	var configs redisv1.RedkeyClusterConfigList
	err = fakeClient.List(context.TODO(), &configs, client.InNamespace("default"))
	require.NoError(t, err)
	assert.Empty(t, configs.Items)
}

// TestReconcileScaleToZero_ListConfigsError verifies error propagation when listing configs fails.
func TestReconcileScaleToZero_ListConfigsError(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "list-err",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 0,
			Ephemeral: true,
			Robin:     redisv1.RobinSpec{Image: "robin:latest"},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(cluster).Build()

	r := &RedkeyClusterReconciler{
		Client:         &listErrClient{Client: fakeClient},
		Scheme:         s,
		ResyncInterval: 30 * time.Second,
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "list-err", Namespace: "default"}}
	_, err := r.Reconcile(context.TODO(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "list error")
}
