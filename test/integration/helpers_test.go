// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package integration_test

import (
	"context"
	"sort"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
	"github.com/inditextech/redkeyoperator/internal/controller"
)

const clusterLabel = "redkey.inditex.dev/cluster"

func newReconciler() *controller.RedkeyClusterReconciler {
	return &controller.RedkeyClusterReconciler{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
	}
}

func reconcileCluster(ctx context.Context, name types.NamespacedName) {
	r := newReconciler()
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: name})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
}

func newTestCluster(name, namespace string) *redisv1.RedkeyCluster { //nolint:unparam
	return &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: redisv1.RedkeyClusterSpec{
			Ephemeral: true,
			Primaries: 3,
			Robin:     redisv1.RobinSpec{Image: "redkey-robin:latest"},
		},
	}
}

// updateConfigStatus updates the status subresource of a RedkeyClusterConfig,
// ensuring that required fields (Nodes) are initialised to satisfy CRD validation.
func updateConfigStatus(ctx context.Context, config *redisv1.RedkeyClusterConfig) {
	if config.Status.Nodes == nil {
		config.Status.Nodes = map[string]*redisv1.RedisNode{}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, config)).To(Succeed())
}

// listConfigs returns all configs for the given cluster sorted by sequence ascending.
func listConfigs(ctx context.Context, clusterName, namespace string) []redisv1.RedkeyClusterConfig {
	var configList redisv1.RedkeyClusterConfigList
	ExpectWithOffset(1, k8sClient.List(ctx, &configList,
		client.InNamespace(namespace),
		client.MatchingLabels{clusterLabel: clusterName},
	)).To(Succeed())

	items := configList.Items
	sort.Slice(items, func(i, j int) bool {
		return items[i].Spec.Sequence < items[j].Spec.Sequence
	})
	return items
}

// setupThreeConfigs creates a cluster and three configs via successive spec changes (primaries 3→5→7).
func setupThreeConfigs(ctx context.Context, clusterName, namespace string) []redisv1.RedkeyClusterConfig {
	namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

	cluster := newTestCluster(clusterName, namespace)
	ExpectWithOffset(1, k8sClient.Create(ctx, cluster)).To(Succeed())

	reconcileCluster(ctx, namespacedName)
	var cl redisv1.RedkeyCluster
	ExpectWithOffset(1, k8sClient.Get(ctx, namespacedName, &cl)).To(Succeed())
	cl.Spec.Primaries = 5
	ExpectWithOffset(1, k8sClient.Update(ctx, &cl)).To(Succeed())
	reconcileCluster(ctx, namespacedName)
	ExpectWithOffset(1, k8sClient.Get(ctx, namespacedName, &cl)).To(Succeed())
	cl.Spec.Primaries = 7
	ExpectWithOffset(1, k8sClient.Update(ctx, &cl)).To(Succeed())
	reconcileCluster(ctx, namespacedName)

	configs := listConfigs(ctx, clusterName, namespace)
	ExpectWithOffset(1, configs).To(HaveLen(3))
	return configs
}

// setupAppliedReadyCluster creates a cluster, reconciles, sets its config to Applied+Ready, and reconciles again.
func setupAppliedReadyCluster(ctx context.Context, clusterName, namespace string) redisv1.RedkeyCluster {
	namespacedName := types.NamespacedName{Name: clusterName, Namespace: namespace}

	cluster := newTestCluster(clusterName, namespace)
	ExpectWithOffset(1, k8sClient.Create(ctx, cluster)).To(Succeed())

	reconcileCluster(ctx, namespacedName)

	configs := listConfigs(ctx, clusterName, namespace)
	configs[0].Status.ConfigPhase = redisv1.ConfigPhaseApplied
	configs[0].Status.Status = redisv1.ClusterStatusReady
	updateConfigStatus(ctx, &configs[0])

	reconcileCluster(ctx, namespacedName)

	var updated redisv1.RedkeyCluster
	ExpectWithOffset(1, k8sClient.Get(ctx, namespacedName, &updated)).To(Succeed())
	return updated
}

// deleteCluster deletes a RedkeyCluster and all its configs.
func deleteCluster(ctx context.Context, name, namespace string) { //nolint:unparam
	cluster := &redisv1.RedkeyCluster{}
	nn := types.NamespacedName{Name: name, Namespace: namespace}
	if err := k8sClient.Get(ctx, nn, cluster); err == nil {
		ExpectWithOffset(1, k8sClient.Delete(ctx, cluster)).To(Succeed())
	}

	// Clean up any configs not garbage-collected (envtest doesn't run GC)
	configs := listConfigs(ctx, name, namespace)
	for i := range configs {
		_ = k8sClient.Delete(ctx, &configs[i])
	}
}
