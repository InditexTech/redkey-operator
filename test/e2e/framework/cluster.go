// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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
	Labels               *map[string]string
	Pdb                  redkeyv1beta1.Pdb
}

// DefaultClusterOptions returns a basic ephemeral cluster configuration.
func DefaultClusterOptions(name, namespace string) ClusterOptions {
	return ClusterOptions{
		Name:                 name,
		Namespace:            namespace,
		Primaries:            3,
		ReplicasPerPrimary:   0,
		Ephemeral:            true,
		PurgeKeysOnRebalance: ptr.To(true),
		SkipIfSuperseded:     true,
		RedisImage:           GetRedisImage(),
		RobinImage:           GetRobinImage(),
		Config:               defaultConfig,
		Resources:            defaultResources(),
	}
}

// WithReplicas sets the replicas per primary.
func (o ClusterOptions) WithReplicas(replicas int32) ClusterOptions {
	o.ReplicasPerPrimary = replicas
	return o
}

// WithPVC configures persistent storage instead of ephemeral.
func (o ClusterOptions) WithPVC(storage string) ClusterOptions {
	o.Ephemeral = false
	o.Storage = storage
	o.PurgeKeysOnRebalance = ptr.To(false)
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

// WithSkipIfSuperseded configures the skipIfSuperseded flag.
func (o ClusterOptions) WithSkipIfSuperseded(skip bool) ClusterOptions {
	o.SkipIfSuperseded = skip
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
				Image: o.RobinImage,
			},
			Config:               o.Config,
			SkipIfSuperseded:     o.SkipIfSuperseded,
			PurgeKeysOnRebalance: o.PurgeKeysOnRebalance,
			DeletePVC:            ptr.To(true),
			Resources:            o.Resources,
		},
	}

	if o.Storage != "" {
		cluster.Spec.Storage = o.Storage
		cluster.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
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

	if o.RobinConfig != nil {
		cluster.Spec.Robin.Config = o.RobinConfig
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
			"password": password,
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
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
	}
}
