// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

const defaultRedisConfig = `maxmemory 90mb
maxmemory-policy allkeys-lru
protected-mode no
appendonly no
save ""`

// ClusterOptions configures a Redkey for chaos testing.
type ClusterOptions struct {
	Name                 string
	Namespace            string
	Primaries            int32
	ReplicasPerPrimary   int32
	PurgeKeysOnRebalance bool
}

// DefaultClusterOptions returns chaos-tuned, ephemeral cluster options.
func DefaultClusterOptions(name, namespace string, primaries int32, purgeKeysOnRebalance bool) ClusterOptions {
	return ClusterOptions{
		Name:                 name,
		Namespace:            namespace,
		Primaries:            primaries,
		ReplicasPerPrimary:   0,
		PurgeKeysOnRebalance: purgeKeysOnRebalance,
	}
}

// BuildRedkey builds an ephemeral Redkey object from the options.
func (o ClusterOptions) BuildRedkey() *redkeyv1beta1.Redkey {
	return &redkeyv1beta1.Redkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.Name,
			Namespace: o.Namespace,
		},
		Spec: redkeyv1beta1.RedkeySpec{
			Primaries:          o.Primaries,
			ReplicasPerPrimary: o.ReplicasPerPrimary,
			Ephemeral:          true,
			Image:              GetRedisImage(),
			Robin: redkeyv1beta1.RobinSpec{
				Image:     GetRobinImage(),
				Resources: defaultRobinResources(),
				Config:    defaultRobinConfig(),
			},
			Config:               defaultRedisConfig,
			SkipIfSuperseded:     true,
			PurgeKeysOnRebalance: ptr.To(o.PurgeKeysOnRebalance),
			DeletePVC:            ptr.To(true),
			Resources:            defaultRedisResources(),
		},
	}
}

// CreateRedkey creates a Redkey in the given namespace.
func CreateRedkey(
	ctx context.Context,
	c client.Client,
	opts ClusterOptions,
) (*redkeyv1beta1.Redkey, error) {
	cluster := opts.BuildRedkey()
	if err := c.Create(ctx, cluster); err != nil {
		return nil, fmt.Errorf("creating Redkey %s/%s: %w", opts.Namespace, opts.Name, err)
	}
	return cluster, nil
}

// GetRedkey fetches the current Redkey CR.
func GetRedkey(
	ctx context.Context,
	c client.Client,
	namespace, name string,
) (*redkeyv1beta1.Redkey, error) {
	cluster := &redkeyv1beta1.Redkey{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, cluster); err != nil {
		return nil, err
	}
	return cluster, nil
}

// ScaleCluster updates spec.primaries on the Redkey, retrying on conflicts and on
// validation errors raised while the cluster is not yet Ready (the operator/Robin may reject a
// topology change until the cluster stabilizes).
func ScaleCluster(ctx context.Context, c client.Client, namespace, name string, primaries int32) error {
	return wait.PollUntilContextTimeout(ctx, 3*time.Second, scaleAckTimeout, true,
		func(ctx context.Context) (bool, error) {
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				cluster := &redkeyv1beta1.Redkey{}
				if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, cluster); err != nil {
					return err
				}
				cluster.Spec.Primaries = primaries
				return c.Update(ctx, cluster)
			})
			if err == nil {
				return true, nil
			}
			// Retry while the cluster is not Ready (validation rejects topology changes).
			if apierrors.IsInvalid(err) || apierrors.IsConflict(err) {
				return false, nil
			}
			return false, err
		})
}

// WaitForScaleAck waits until the Redis StatefulSet reflects the expected replica count, which
// happens after Robin reconciles the new topology. With PurgeKeysOnRebalance the StatefulSet may be
// recreated, so polling tolerates a transiently missing StatefulSet.
func WaitForScaleAck(ctx context.Context, c client.Client, namespace, name string, expectedReplicas int32) error {
	return wait.PollUntilContextTimeout(ctx, 3*time.Second, scaleAckTimeout, true,
		func(ctx context.Context) (bool, error) {
			sts := &appsv1.StatefulSet{}
			if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sts); err != nil {
				if apierrors.IsNotFound(err) {
					return false, nil
				}
				return false, err
			}
			if sts.Spec.Replicas == nil {
				return false, nil
			}
			return *sts.Spec.Replicas == expectedReplicas, nil
		})
}

// GetStatefulSetReplicas returns the desired replica count of the Redis StatefulSet.
func GetStatefulSetReplicas(ctx context.Context, c client.Client, namespace, name string) (int32, error) {
	sts := &appsv1.StatefulSet{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, sts); err != nil {
		return 0, err
	}
	if sts.Spec.Replicas == nil {
		return 0, nil
	}
	return *sts.Spec.Replicas, nil
}

func defaultRedisResources() *corev1.ResourceRequirements {
	return &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("300m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
	}
}

func defaultRobinResources() *corev1.ResourceRequirements {
	return &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
	}
}

// defaultRobinConfig returns a RobinConfig with shortened intervals to speed up chaos recovery.
func defaultRobinConfig() *redkeyv1beta1.RobinConfig {
	reconcileInterval := 5
	reconcileIntervalOnError := 3
	reconcileIntervalOnWait := 3
	clusterMeetWait := 2

	return &redkeyv1beta1.RobinConfig{
		Reconciler: &redkeyv1beta1.RobinConfigReconciler{
			IntervalSeconds:        &reconcileInterval,
			IntervalOnErrorSeconds: &reconcileIntervalOnError,
			IntervalOnWaitSeconds:  &reconcileIntervalOnWait,
		},
		Cluster: &redkeyv1beta1.RobinConfigCluster{
			ClusterMeetWaitSeconds: &clusterMeetWait,
		},
	}
}
