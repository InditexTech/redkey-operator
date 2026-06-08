// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

func TestRedkeyClusterReconciler_CreateFirstConfig(t *testing.T) {
	s := getScheme()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 3,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cluster).
		WithStatusSubresource(cluster, &redisv1.RedkeyClusterConfig{}).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	require.NoError(t, err)
	assert.Empty(t, res)

	// Check that a RedkeyClusterConfig was created
	var configList redisv1.RedkeyClusterConfigList
	err = fakeClient.List(context.TODO(), &configList, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, configList.Items, 1)

	config := configList.Items[0]
	var storedConfig redisv1.RedkeyClusterConfig
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: config.Name, Namespace: config.Namespace}, &storedConfig)
	require.NoError(t, err)

	assert.Equal(t, "test-cluster-1", config.Name)
	assert.Equal(t, 1, config.Spec.Sequence)
	assert.Empty(t, storedConfig.Status.Nodes)

	// Check status merged back to RedkeyCluster
	var updatedCluster redisv1.RedkeyCluster
	err = fakeClient.Get(context.TODO(), req.NamespacedName, &updatedCluster)
	require.NoError(t, err)
	assert.Equal(t, redisv1.PhaseConfiguring, updatedCluster.Status.Phase)
	assert.NotNil(t, updatedCluster.Status.Nodes)
	assert.Empty(t, updatedCluster.Status.Nodes)
}

func TestRedkeyClusterReconciler_CreateNewConfig_WithRobinConfig(t *testing.T) {
	s := getScheme()
	reconcileInterval := 15
	reconcileIntervalOnError := 7
	reconcileIntervalOnWait := 10
	connectionMaxRetries := 10
	connectionBackOffSeconds := 4
	collectionIntervalSeconds := 60
	redisInfoKeys := []string{"connected_clients", "total_commands_processed"}
	metricsLabels := map[string]string{"env": "prod", "team": "platform"}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster-robin-config",
			Namespace:  "default",
			Generation: 2,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 3,
			Robin: redisv1.RobinSpec{
				Image: "redkey-robin:latest",
				Config: &redisv1.RobinConfig{
					Reconciler: &redisv1.RobinConfigReconciler{
						IntervalSeconds:        &reconcileInterval,
						IntervalOnErrorSeconds: &reconcileIntervalOnError,
						IntervalOnWaitSeconds:  &reconcileIntervalOnWait,
					},
					Cluster: &redisv1.RobinConfigCluster{
						ConnectionMaxRetries:     &connectionMaxRetries,
						ConnectionBackOffSeconds: &connectionBackOffSeconds,
					},
					Metrics: &redisv1.RobinConfigMetrics{
						CollectionIntervalSeconds: &collectionIntervalSeconds,
						RedisInfoKeys:             redisInfoKeys,
						MetricsLabels:             metricsLabels,
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	r := &RedkeyClusterReconciler{Client: fakeClient, Scheme: s}

	err := r.createNewConfig(context.TODO(), cluster, &redisv1.RedkeyClusterConfig{
		Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 1},
	})
	require.NoError(t, err)

	var stored redisv1.RedkeyClusterConfig
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "test-cluster-robin-config-2", Namespace: "default"}, &stored)
	require.NoError(t, err)
	require.NotNil(t, stored.Spec.RobinConfig)
	require.NotNil(t, stored.Spec.RobinConfig.Reconciler)
	require.NotNil(t, stored.Spec.RobinConfig.Cluster)
	require.NotNil(t, stored.Spec.RobinConfig.Metrics)
	assert.Equal(t, reconcileInterval, *stored.Spec.RobinConfig.Reconciler.IntervalSeconds)
	assert.Equal(t, reconcileIntervalOnError, *stored.Spec.RobinConfig.Reconciler.IntervalOnErrorSeconds)
	assert.Equal(t, reconcileIntervalOnWait, *stored.Spec.RobinConfig.Reconciler.IntervalOnWaitSeconds)
	assert.Equal(t, connectionMaxRetries, *stored.Spec.RobinConfig.Cluster.ConnectionMaxRetries)
	assert.Equal(t, connectionBackOffSeconds, *stored.Spec.RobinConfig.Cluster.ConnectionBackOffSeconds)
	assert.Equal(t, collectionIntervalSeconds, *stored.Spec.RobinConfig.Metrics.CollectionIntervalSeconds)
	assert.Equal(t, redisInfoKeys, stored.Spec.RobinConfig.Metrics.RedisInfoKeys)
	assert.Equal(t, metricsLabels, stored.Spec.RobinConfig.Metrics.MetricsLabels)
}

func TestRedkeyClusterReconciler_CreateNewConfig_WithLabelsAndAnnotations(t *testing.T) {
	s := getScheme()

	labels := map[string]string{"team": "platform", "env": "prod"}
	annotations := map[string]string{"prometheus.io/scrape": "true", "prometheus.io/port": "9121"}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-la",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral:   true,
			Primaries:   3,
			Labels:      &labels,
			Annotations: &annotations,
			Robin:       redisv1.RobinSpec{Image: "redkey-robin:latest"},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
	r := &RedkeyClusterReconciler{Client: fakeClient, Scheme: s}

	err := r.createNewConfig(context.TODO(), cluster, nil)
	require.NoError(t, err)

	var stored redisv1.RedkeyClusterConfig
	err = fakeClient.Get(context.TODO(), types.NamespacedName{Name: "test-cluster-la-1", Namespace: "default"}, &stored)
	require.NoError(t, err)

	require.NotNil(t, stored.Spec.Labels)
	assert.Equal(t, labels, *stored.Spec.Labels)
	require.NotNil(t, stored.Spec.Annotations)
	assert.Equal(t, annotations, *stored.Spec.Annotations)
}

func TestRedkeyClusterReconciler_CreateNewConfig_SetControllerReferenceError(t *testing.T) {
	r := &RedkeyClusterReconciler{
		Client: fake.NewClientBuilder().WithScheme(getScheme()).Build(),
		Scheme: runtime.NewScheme(),
	}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	err := r.createNewConfig(context.TODO(), cluster, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no kind is registered")
}

func TestRedkeyClusterReconciler_UpdateConfigGeneration(t *testing.T) {
	s := getScheme()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 2,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 5, // Updated
		},
	}

	existingConfig := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-1",
			Namespace: "default",
			Labels: map[string]string{
				ClusterLabel: "test-cluster",
			},
		},
		Spec: redisv1.RedkeyClusterConfigSpec{
			Sequence: 1,
		},
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhaseApplied,
			Status:      redisv1.ClusterStatusReady,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cluster, existingConfig).
		WithStatusSubresource(cluster, existingConfig).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	require.NoError(t, err)
	assert.Empty(t, res)

	// Check that a new RedkeyClusterConfig was created (sequence 2)
	var configList redisv1.RedkeyClusterConfigList
	err = fakeClient.List(context.TODO(), &configList, client.InNamespace("default"))
	require.NoError(t, err)
	require.Len(t, configList.Items, 2)

	// The newer config should be sequence 2, while the previous Applied config remains
	// as the last stable baseline until the new one reaches Applied.
	var newConfig redisv1.RedkeyClusterConfig
	var existingConfigFound bool
	for _, c := range configList.Items {
		if c.Spec.Sequence == 1 {
			existingConfigFound = true
		}
		if c.Spec.Sequence == 2 {
			newConfig = c
		}
	}
	assert.True(t, existingConfigFound)
	assert.Equal(t, "test-cluster-2", newConfig.Name)
	assert.Equal(t, 2, newConfig.Spec.Sequence)
}

func TestRedkeyClusterReconciler_CleanupSupersededConfigs(t *testing.T) {
	s := getScheme()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 3,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 3,
		},
	}

	config1 := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-1",
			Namespace: "default",
			Labels: map[string]string{
				ClusterLabel: "test-cluster",
			},
		},
		Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 1},
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhaseApplied,
		},
	}

	config2 := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-2",
			Namespace: "default",
			Labels: map[string]string{
				ClusterLabel: "test-cluster",
			},
		},
		Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 2},
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhasePending,
		},
	}

	config3 := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-3",
			Namespace: "default",
			Labels: map[string]string{
				ClusterLabel: "test-cluster",
			},
			Annotations: map[string]string{
				"redkey.inditex.dev/cluster-generation": "3", // matches cluster generation
			},
		},
		Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 3},
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhaseApplied,
		},
	}

	config4 := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-4",
			Namespace: "default",
			Labels: map[string]string{
				ClusterLabel: "test-cluster",
			},
			Annotations: map[string]string{
				"redkey.inditex.dev/cluster-generation": "3",
			},
		},
		Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 4},
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhasePending,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cluster, config1, config2, config3, config4).
		WithStatusSubresource(cluster, config1, config2, config3, config4).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.TODO(), req)
	require.NoError(t, err)

	// Check if the older applied config (config1) was deleted
	var configList redisv1.RedkeyClusterConfigList
	err = fakeClient.List(context.TODO(), &configList, client.InNamespace("default"))
	require.NoError(t, err)
	// We expect the last Applied config and any later configs to remain.
	require.Len(t, configList.Items, 2)
	assert.ElementsMatch(t, []string{"test-cluster-3", "test-cluster-4"}, []string{configList.Items[0].Name, configList.Items[1].Name})
}

func TestRedkeyClusterReconciler_AggregateStatus(t *testing.T) {
	s := getScheme()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 3,
		},
	}

	config := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-1",
			Namespace: "default",
			Labels: map[string]string{
				ClusterLabel: "test-cluster",
			},
			Annotations: map[string]string{
				"redkey.inditex.dev/cluster-generation": "1", // matches cluster generation
			},
		},
		Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 1},
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhaseApplied,
			Status:      redisv1.ClusterPhaseError, // Mock error reported by Robin
			Substatus: redisv1.RedkeyClusterSubstatus{
				Status:             "RollingBack",
				UpgradingPartition: 2,
			},
			Nodes: map[string]*redisv1.RedisNode{
				"redis-0": {
					Role:              "master",
					IP:                "10.0.0.10",
					ReplicationStatus: "synced",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cluster, config).
		WithStatusSubresource(cluster, config).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.TODO(), req)
	require.NoError(t, err)

	// Verify RedkeyCluster status updated to Error phase
	var updatedCluster redisv1.RedkeyCluster
	err = fakeClient.Get(context.TODO(), req.NamespacedName, &updatedCluster)
	require.NoError(t, err)
	assert.Equal(t, redisv1.PhaseError, updatedCluster.Status.Phase)
	assert.Equal(t, redisv1.ClusterPhaseError, updatedCluster.Status.Status)
	assert.Equal(t, config.Status.Substatus, updatedCluster.Status.Substatus)
	assert.Equal(t, config.Status.Nodes, updatedCluster.Status.Nodes)

	// Has Error condition
	var hasErrorCond bool
	for _, cond := range updatedCluster.Status.Conditions {
		if cond.Type == "Error" && cond.Status == metav1.ConditionTrue {
			hasErrorCond = true
		}
	}
	assert.True(t, hasErrorCond, "Expected Error condition to be true")
}

func TestRedkeyClusterReconciler_AggregateStatus_EmptyStatus(t *testing.T) {
	s := getScheme()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster-empty-status",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 3,
		},
	}

	config := &redisv1.RedkeyClusterConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-empty-status-1",
			Namespace: "default",
			Labels: map[string]string{
				ClusterLabel: "test-cluster-empty-status",
			},
		},
		Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 1},
		// Deliberately pass NO status (equivalent to empty struct)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cluster, config).
		WithStatusSubresource(cluster, config).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster-empty-status",
			Namespace: "default",
		},
	}

	_, err := r.Reconcile(context.TODO(), req)
	require.NoError(t, err, "Reconciler should handle missing config status gracefully without panics")

	// Verify RedkeyCluster aggregated an empty status safely
	var updatedCluster redisv1.RedkeyCluster
	err = fakeClient.Get(context.TODO(), req.NamespacedName, &updatedCluster)
	require.NoError(t, err)

	assert.Equal(t, redisv1.PhaseConfiguring, updatedCluster.Status.Phase, "Should default to Pending configuration")
	assert.Equal(t, "", updatedCluster.Status.Status)
	assert.Empty(t, updatedCluster.Status.Nodes, "Nodes map should be safely initialized to an empty map")

	var hasPendingCond bool
	for _, cond := range updatedCluster.Status.Conditions {
		if cond.Type == "ConfigPending" && cond.Status == metav1.ConditionTrue {
			hasPendingCond = true
		}
	}
	assert.True(t, hasPendingCond, "Expected ConfigPending condition to be true because phase is not Applied/Superseded")
}

func TestRedkeyClusterReconciler_AggregateStatus_EmptyConfigs(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).Build(),
		Scheme: s,
	}
	cluster := &redisv1.RedkeyCluster{}

	err := r.aggregateStatus(context.TODO(), cluster, nil)
	assert.NoError(t, err)
	assert.Empty(t, cluster.Status.Conditions)
}

func TestRedkeyClusterReconciler_AggregateStatus_PatchErrorIsReturned(t *testing.T) {
	s := getScheme()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 1,
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()
	r := &RedkeyClusterReconciler{
		Client: &statusUpdateErrClient{
			Client: fakeClient,
			updateErr: k8serrors.NewConflict(
				schema.GroupResource{Group: "redkey.inditex.dev", Resource: "redkeyclusters"},
				"test-cluster",
				errors.New("conflict"),
			),
		},
		Scheme: s,
	}

	configs := []redisv1.RedkeyClusterConfig{{
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhasePending,
		},
	}}

	// A persistent conflict should be retried and ultimately propagated to the caller.
	err := r.aggregateStatus(context.TODO(), cluster, configs)
	assert.Error(t, err)
	assert.True(t, k8serrors.IsConflict(err))
}

func TestAggregateConditions_PreserveUnchangedTransitionTimes(t *testing.T) {
	initialTransitionTime := metav1.NewTime(time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC))
	conditions := []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			LastTransitionTime: initialTransitionTime,
			Reason:             "StatusAggregated",
		},
		{
			Type:               "ConfigPending",
			Status:             metav1.ConditionTrue,
			LastTransitionTime: initialTransitionTime,
			Reason:             "StatusAggregated",
		},
		{
			Type:               "Error",
			Status:             metav1.ConditionFalse,
			LastTransitionTime: initialTransitionTime,
			Reason:             "StatusAggregated",
		},
	}

	config := &redisv1.RedkeyClusterConfig{
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhaseApplied,
			Status:      redisv1.ClusterStatusReady,
		},
	}

	aggregated := aggregateConditions(conditions, config)

	readyCondition := meta.FindStatusCondition(aggregated, "Ready")
	require.NotNil(t, readyCondition)
	assert.Equal(t, metav1.ConditionTrue, readyCondition.Status)
	assert.True(t, readyCondition.LastTransitionTime.After(initialTransitionTime.Time))

	configPendingCondition := meta.FindStatusCondition(aggregated, "ConfigPending")
	require.NotNil(t, configPendingCondition)
	assert.Equal(t, metav1.ConditionFalse, configPendingCondition.Status)
	assert.True(t, configPendingCondition.LastTransitionTime.After(initialTransitionTime.Time))

	errorCondition := meta.FindStatusCondition(aggregated, "Error")
	require.NotNil(t, errorCondition)
	assert.Equal(t, metav1.ConditionFalse, errorCondition.Status)
	assert.True(t, errorCondition.LastTransitionTime.Time.Equal(initialTransitionTime.Time))
}

func TestComputePhaseFromConditions(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		expected   string
	}{
		{
			name:       "No conditions returns Configuring",
			conditions: nil,
			expected:   redisv1.PhaseConfiguring,
		},
		{
			name: "Error=True returns Error",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionFalse},
				{Type: "Error", Status: metav1.ConditionTrue},
			},
			expected: redisv1.PhaseError,
		},
		{
			name: "Ready=True and Error=False returns Ready",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Error", Status: metav1.ConditionFalse},
			},
			expected: redisv1.PhaseReady,
		},
		{
			name: "Ready=False and Error=False returns Configuring",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionFalse},
				{Type: "Error", Status: metav1.ConditionFalse},
			},
			expected: redisv1.PhaseConfiguring,
		},
		{
			name: "Error=True takes precedence over Ready=True",
			conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Error", Status: metav1.ConditionTrue},
			},
			expected: redisv1.PhaseError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := computePhaseFromConditions(tt.conditions)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAggregateConditions_SupersededConfigBehavesLikeApplied(t *testing.T) {
	conditions := aggregateConditions(nil, &redisv1.RedkeyClusterConfig{
		Status: redisv1.RedkeyClusterConfigStatus{
			ConfigPhase: redisv1.ConfigPhaseSuperseded,
			Status:      redisv1.ClusterPhaseError,
		},
	})

	readyCondition := meta.FindStatusCondition(conditions, "Ready")
	require.NotNil(t, readyCondition)
	assert.Equal(t, metav1.ConditionFalse, readyCondition.Status)

	configPendingCondition := meta.FindStatusCondition(conditions, "ConfigPending")
	require.NotNil(t, configPendingCondition)
	assert.Equal(t, metav1.ConditionFalse, configPendingCondition.Status)

	errorCondition := meta.FindStatusCondition(conditions, "Error")
	require.NotNil(t, errorCondition)
	assert.Equal(t, metav1.ConditionTrue, errorCondition.Status)
}

func TestNeedsNewConfig(t *testing.T) {
	tests := []struct {
		name         string
		cluster      *redisv1.RedkeyCluster
		latestConfig *redisv1.RedkeyClusterConfig
		expected     bool
	}{
		{
			name: "No existing config and unobserved generation",
			cluster: &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status:     redisv1.RedkeyClusterStatus{ObservedGeneration: 0},
			},
			latestConfig: nil,
			expected:     true,
		},
		{
			name: "Config exists with matching generation",
			cluster: &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Status:     redisv1.RedkeyClusterStatus{ObservedGeneration: 2},
			},
			latestConfig: &redisv1.RedkeyClusterConfig{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"redkey.inditex.dev/cluster-generation": "2",
					},
				},
			},
			expected: false,
		},
		{
			name: "Config exists with older generation",
			cluster: &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
				Status:     redisv1.RedkeyClusterStatus{ObservedGeneration: 2},
			},
			latestConfig: &redisv1.RedkeyClusterConfig{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"redkey.inditex.dev/cluster-generation": "2",
					},
				},
			},
			expected: true,
		},
		{
			name: "Config exists with newer generation (edge case)",
			cluster: &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Status:     redisv1.RedkeyClusterStatus{ObservedGeneration: 2},
			},
			latestConfig: &redisv1.RedkeyClusterConfig{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"redkey.inditex.dev/cluster-generation": "3",
					},
				},
			},
			expected: false,
		},
		{
			name: "Config exists with invalid generation annotation",
			cluster: &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Status:     redisv1.RedkeyClusterStatus{ObservedGeneration: 1},
			},
			latestConfig: &redisv1.RedkeyClusterConfig{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						"redkey.inditex.dev/cluster-generation": "not-a-number",
					},
				},
			},
			expected: true,
		},
		{
			name: "Config exists with no generation annotation",
			cluster: &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Status:     redisv1.RedkeyClusterStatus{ObservedGeneration: 1},
			},
			latestConfig: &redisv1.RedkeyClusterConfig{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			expected: true,
		},
		{
			name: "ObservedGeneration matches, no new config needed",
			cluster: &redisv1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status:     redisv1.RedkeyClusterStatus{ObservedGeneration: 1},
			},
			latestConfig: &redisv1.RedkeyClusterConfig{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := needsNewConfig(tt.cluster, tt.latestConfig)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConditionStatus(t *testing.T) {
	assert.Equal(t, metav1.ConditionTrue, conditionStatus(true))
	assert.Equal(t, metav1.ConditionFalse, conditionStatus(false))
}

func TestCleanupSupersededConfigs_EmptyConfigs(t *testing.T) {
	s := getScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	result, err := r.cleanupSupersededConfigs(context.Background(), nil)
	assert.NoError(t, err)
	assert.Nil(t, result)
}

func TestAggregateStatus_UsesHighestSequenceConfig(t *testing.T) {
	s := getScheme()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 2,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 3,
		},
		Status: redisv1.RedkeyClusterStatus{
			Phase:  redisv1.PhaseReady,
			Status: redisv1.ClusterStatusReady,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	configs := []redisv1.RedkeyClusterConfig{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-1",
				Namespace: "default",
			},
			Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 1},
			Status: redisv1.RedkeyClusterConfigStatus{
				ConfigPhase: redisv1.ConfigPhaseApplied,
				Status:      redisv1.ClusterStatusReady,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: "default",
			},
			Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 2},
			Status: redisv1.RedkeyClusterConfigStatus{
				ConfigPhase: redisv1.ConfigPhaseInProgress,
				Status:      "Configuring",
			},
		},
	}

	err := r.aggregateStatus(context.TODO(), cluster, configs)
	require.NoError(t, err)

	// Should use InProgress config (the one being applied) → Phase = Configuring
	assert.Equal(t, redisv1.PhaseConfiguring, cluster.Status.Phase)
	assert.Equal(t, "Configuring", cluster.Status.Status)
}

func TestAggregateStatus_PendingConfigYieldsConfiguring(t *testing.T) {
	s := getScheme()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 3,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	// Single Pending config (initial creation) → should yield Configuring per architecture doc
	configs := []redisv1.RedkeyClusterConfig{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-1",
				Namespace: "default",
			},
			Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 1},
			Status: redisv1.RedkeyClusterConfigStatus{
				ConfigPhase: redisv1.ConfigPhasePending,
			},
		},
	}

	err := r.aggregateStatus(context.TODO(), cluster, configs)
	require.NoError(t, err)

	// Per architecture doc: single Pending config → Phase = Configuring, ConfigPending = True
	assert.Equal(t, redisv1.PhaseConfiguring, cluster.Status.Phase)

	var hasPendingCond bool
	for _, cond := range cluster.Status.Conditions {
		if cond.Type == "ConfigPending" && cond.Status == metav1.ConditionTrue {
			hasPendingCond = true
		}
	}
	assert.True(t, hasPendingCond, "Expected ConfigPending condition to be true")
}

func TestAggregateStatus_MultiplePendingUsesAppliedBaseline(t *testing.T) {
	s := getScheme()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 2,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 5,
		},
		Status: redisv1.RedkeyClusterStatus{
			Phase:  redisv1.PhaseReady,
			Status: redisv1.ClusterStatusReady,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	// Applied baseline + Pending (Robin hasn't started yet) → use Applied baseline → Phase = Ready
	configs := []redisv1.RedkeyClusterConfig{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-1",
				Namespace: "default",
			},
			Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 1},
			Status: redisv1.RedkeyClusterConfigStatus{
				ConfigPhase: redisv1.ConfigPhaseApplied,
				Status:      redisv1.ClusterStatusReady,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: "default",
			},
			Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 2},
			Status: redisv1.RedkeyClusterConfigStatus{
				ConfigPhase: redisv1.ConfigPhasePending,
			},
		},
	}

	err := r.aggregateStatus(context.TODO(), cluster, configs)
	require.NoError(t, err)

	// No InProgress config → falls back to Applied baseline → Phase = Ready
	assert.Equal(t, redisv1.PhaseReady, cluster.Status.Phase)
	assert.Equal(t, redisv1.ClusterStatusReady, cluster.Status.Status)
}

func TestAggregateStatus_InProgressWithQueuedPending(t *testing.T) {
	s := getScheme()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 3,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 5,
		},
		Status: redisv1.RedkeyClusterStatus{
			Phase:  redisv1.PhaseReady,
			Status: redisv1.ClusterStatusReady,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cluster).
		WithStatusSubresource(cluster).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	// Applied + InProgress + Pending → use InProgress (ignores queued Pending)
	configs := []redisv1.RedkeyClusterConfig{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-1",
				Namespace: "default",
			},
			Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 1},
			Status: redisv1.RedkeyClusterConfigStatus{
				ConfigPhase: redisv1.ConfigPhaseApplied,
				Status:      redisv1.ClusterStatusReady,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: "default",
			},
			Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 2},
			Status: redisv1.RedkeyClusterConfigStatus{
				ConfigPhase: redisv1.ConfigPhaseInProgress,
				Status:      "ScalingUp",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-3",
				Namespace: "default",
			},
			Spec: redisv1.RedkeyClusterConfigSpec{Sequence: 3},
			Status: redisv1.RedkeyClusterConfigStatus{
				ConfigPhase: redisv1.ConfigPhasePending,
			},
		},
	}

	err := r.aggregateStatus(context.TODO(), cluster, configs)
	require.NoError(t, err)

	// Uses InProgress config, not the queued Pending one
	assert.Equal(t, redisv1.PhaseConfiguring, cluster.Status.Phase)
	assert.Equal(t, "ScalingUp", cluster.Status.Status)
}
