// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"os"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

// Default images and constants.
const (
	defaultRedisImage = "redis:8-bookworm"
	defaultConfig     = `maxmemory 90mb
maxmemory-policy allkeys-lru
protected-mode no
appendonly no
save ""`
)

// GetRedisImage returns the Redis image to use (from env or default).
func GetRedisImage() string {
	if img := os.Getenv("REDIS_IMAGE"); img != "" {
		return img
	}
	return defaultRedisImage
}

// GetRobinImage returns the Robin image to use (from env or default).
func GetRobinImage() string {
	if img := os.Getenv("IMAGE_ROBIN"); img != "" {
		return img
	}
	if img := os.Getenv("ROBIN_IMAGE"); img != "" {
		return img
	}
	return "redkey-robin:latest"
}

// ClusterOptions configures a RedkeyCluster for E2E testing.
type ClusterOptions struct {
	Name                 string
	Namespace            string
	Mode                 string
	Primaries            int32
	ReplicasPerPrimary   int32
	Ephemeral            bool
	Storage              string
	StorageClassName     string
	PurgeKeysOnRebalance *bool
	SkipIfSuperseded     bool
	AuthSecretName       string
	RedisImage           string
	RobinImage           string
	Config               string
	RobinConfig          *redkeyv1beta1.RobinConfig
	Resources            *corev1.ResourceRequirements
	RobinResources       *corev1.ResourceRequirements
	Labels               *map[string]string
	Annotations          *map[string]string
	Pdb                  redkeyv1beta1.Pdb
	StatefulSetOverride  *redkeyv1beta1.PartialStatefulSet
	ServiceOverride      *redkeyv1beta1.PartialService
}

// DefaultClusterOptions returns a basic ephemeral cluster configuration optimized for E2E tests.
// Resources are minimized and Robin reconcile intervals are shortened to speed up tests.
func DefaultClusterOptions(name, namespace string) ClusterOptions {
	return ClusterOptions{
		Name:                 name,
		Namespace:            namespace,
		Primaries:            3,
		ReplicasPerPrimary:   0,
		Ephemeral:            true,
		PurgeKeysOnRebalance: new(true),
		SkipIfSuperseded:     true,
		RedisImage:           GetRedisImage(),
		RobinImage:           GetRobinImage(),
		Config:               defaultConfig,
		Resources:            defaultResources(),
		RobinResources:       defaultRobinResources(),
		RobinConfig:          defaultRobinConfig(),
	}
}

// WithReplicas sets the replicas per primary.
func (o ClusterOptions) WithReplicas(replicas int32) ClusterOptions {
	o.ReplicasPerPrimary = replicas
	return o
}

// WithMode sets the deployment mode (cluster or standalone).
func (o ClusterOptions) WithMode(mode string) ClusterOptions {
	o.Mode = mode
	return o
}

// WithPVC configures persistent storage instead of ephemeral.
func (o ClusterOptions) WithPVC(storage string) ClusterOptions {
	o.Ephemeral = false
	o.Storage = storage
	o.PurgeKeysOnRebalance = new(false)
	return o
}

// WithAuth configures authentication with the given secret name.
func (o ClusterOptions) WithAuth(secretName string) ClusterOptions {
	o.AuthSecretName = secretName
	return o
}

// WithRobinConfig sets custom Robin configuration.
func (o ClusterOptions) WithRobinConfig(config *redkeyv1beta1.RobinConfig) ClusterOptions {
	o.RobinConfig = config
	return o
}

// WithRobinResources sets custom resource requirements for the Robin container.
func (o ClusterOptions) WithRobinResources(resources *corev1.ResourceRequirements) ClusterOptions {
	o.RobinResources = resources
	return o
}

// WithSkipIfSuperseded configures the skipIfSuperseded flag.
func (o ClusterOptions) WithSkipIfSuperseded(skip bool) ClusterOptions {
	o.SkipIfSuperseded = skip
	return o
}

// WithStatefulSetOverride sets a StatefulSet override applied to the Redis StatefulSet.
func (o ClusterOptions) WithStatefulSetOverride(override *redkeyv1beta1.PartialStatefulSet) ClusterOptions {
	o.StatefulSetOverride = override
	return o
}

// WithServiceOverride sets a Service override applied to the headless Redis Service.
func (o ClusterOptions) WithServiceOverride(override *redkeyv1beta1.PartialService) ClusterOptions {
	o.ServiceOverride = override
	return o
}

// BuildRedkeyCluster creates a RedkeyCluster object from the options.
func (o ClusterOptions) BuildRedkeyCluster() *redkeyv1beta1.RedkeyCluster {
	cluster := &redkeyv1beta1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.Name,
			Namespace: o.Namespace,
		},
		Spec: redkeyv1beta1.RedkeyClusterSpec{
			Primaries:          o.Primaries,
			ReplicasPerPrimary: o.ReplicasPerPrimary,
			Ephemeral:          o.Ephemeral,
			Image:              o.RedisImage,
			Robin: redkeyv1beta1.RobinSpec{
				Image:     o.RobinImage,
				Resources: o.RobinResources,
			},
			Config:               o.Config,
			SkipIfSuperseded:     o.SkipIfSuperseded,
			PurgeKeysOnRebalance: o.PurgeKeysOnRebalance,
			DeletePVC:            new(true),
			Resources:            o.Resources,
		},
	}

	if o.Storage != "" {
		cluster.Spec.Storage = o.Storage
		cluster.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

	if o.Mode != "" {
		cluster.Spec.Mode = o.Mode
	}

	if o.StorageClassName != "" {
		cluster.Spec.StorageClassName = o.StorageClassName
	}

	if o.AuthSecretName != "" {
		cluster.Spec.Auth = redkeyv1beta1.RedisAuth{SecretName: o.AuthSecretName}
	}

	if o.Labels != nil {
		cluster.Spec.Labels = o.Labels
	}

	if o.Annotations != nil {
		cluster.Spec.Annotations = o.Annotations
	}

	if o.RobinConfig != nil {
		cluster.Spec.Robin.Config = o.RobinConfig
	}

	if o.StatefulSetOverride != nil || o.ServiceOverride != nil {
		cluster.Spec.Override = &redkeyv1beta1.RedkeyClusterOverrideSpec{
			StatefulSet: o.StatefulSetOverride,
			Service:     o.ServiceOverride,
		}
	}

	cluster.Spec.Pdb = o.Pdb

	return cluster
}

// CreateRedkeyCluster creates a RedkeyCluster in the given namespace.
func CreateRedkeyCluster(
	ctx context.Context, c client.Client, opts ClusterOptions,
) (*redkeyv1beta1.RedkeyCluster, error) {
	cluster := opts.BuildRedkeyCluster()
	if err := c.Create(ctx, cluster); err != nil {
		return nil, fmt.Errorf("creating RedkeyCluster %s/%s: %w", opts.Namespace, opts.Name, err)
	}
	return cluster, nil
}

// CreateAuthSecret creates a Kubernetes Secret with a Redis password.
func CreateAuthSecret(ctx context.Context, c client.Client, namespace, name, password string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"password":    password,
			"requirepass": password,
		},
	}
	return c.Create(ctx, secret)
}

// UpdateAuthSecret updates the password in an existing auth secret.
func UpdateAuthSecret(ctx context.Context, c client.Client, namespace, name, newPassword string) error {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		return err
	}
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data["password"] = []byte(newPassword)
	secret.Data["requirepass"] = []byte(newPassword)
	return c.Update(ctx, secret)
}

// GetRedisPodNames returns the pod names for a RedkeyCluster's Redis StatefulSet.
func GetRedisPodNames(
	ctx context.Context, c client.Client, clusterName, namespace string, expectedCount int,
) ([]string, error) {
	pods := &corev1.PodList{}
	labels := RedisPodLabels(clusterName)
	if err := c.List(ctx, pods, client.InNamespace(namespace), client.MatchingLabels(labels)); err != nil {
		return nil, err
	}

	var names []string
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp == nil {
			names = append(names, pod.Name)
		}
	}

	if expectedCount > 0 && len(names) != expectedCount {
		return names, fmt.Errorf("expected %d pods, found %d", expectedCount, len(names))
	}
	return names, nil
}

// RedisPodLabels returns labels that match Redis pods but exclude Robin pods.
func RedisPodLabels(clusterName string) map[string]string {
	return map[string]string{
		"redkey.inditex.dev/cluster":   clusterName,
		"redkey.inditex.dev/component": "redis",
	}
}

func defaultResources() *corev1.ResourceRequirements {
	return &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
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

// defaultRobinConfig returns a RobinConfig with shortened intervals for faster E2E test execution.
// Override via E2E_RECONCILE_INTERVAL env var (in seconds) — applies to all three intervals.
func defaultRobinConfig() *redkeyv1beta1.RobinConfig {
	reconcileInterval := 5
	reconcileIntervalOnError := 3
	reconcileIntervalOnWait := 3
	clusterMeetWait := 2

	if v := os.Getenv("E2E_RECONCILE_INTERVAL"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			reconcileInterval = parsed
			reconcileIntervalOnError = parsed
			reconcileIntervalOnWait = parsed
		}
	}

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
