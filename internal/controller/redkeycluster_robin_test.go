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
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

type badClient struct {
	client.Client
}

func (c *badClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return errors.New("boom")
}

func TestRedkeyClusterReconciler_EnsureRBAC(t *testing.T) {
	s := getScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}
	ctx := context.Background()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "some-uid",
		},
	}

	err := r.ensureRBAC(ctx, cluster)
	assert.NoError(t, err)

	sa := &corev1.ServiceAccount{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "test-cluster-robin", Namespace: "default"}, sa)
	require.NoError(t, err)
	assert.Equal(t, "test-cluster-robin", sa.Name)

	role := &rbacv1.Role{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "test-cluster-robin", Namespace: "default"}, role)
	require.NoError(t, err)

	roleBinding := &rbacv1.RoleBinding{}
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "test-cluster-robin", Namespace: "default"}, roleBinding)
	require.NoError(t, err)
}

func TestRedkeyClusterReconciler_EnsureRobinDeployment(t *testing.T) {
	s := getScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}
	ctx := context.Background()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "some-uid",
		},
	}

	err := r.ensureRobinDeployment(ctx, cluster)
	assert.NoError(t, err)
}

func TestRedkeyClusterReconciler_EnsureRBACErrors(t *testing.T) {
	s := getScheme()
	fakeC := fake.NewClientBuilder().WithScheme(s).Build()
	badC := &badClient{Client: fakeC}

	r := &RedkeyClusterReconciler{
		Client: badC,
		Scheme: s,
	}
	ctx := context.Background()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "some-uid",
		},
	}

	err := r.ensureRBAC(ctx, cluster)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestRedkeyClusterReconciler_EnsureRBAC_SetControllerReferenceError(t *testing.T) {
	s := runtime.NewScheme()

	r := &RedkeyClusterReconciler{
		Client: fake.NewClientBuilder().WithScheme(s).Build(),
		Scheme: s,
	}

	err := r.ensureRBAC(context.Background(), newEnsureRBACCluster())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no kind is registered")
}

func TestRedkeyClusterReconciler_EnsureRBAC_ServiceAccountGetError(t *testing.T) {
	s := getScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

	r := &RedkeyClusterReconciler{
		Client: &serviceAccountGetErrClient{Client: fakeClient},
		Scheme: s,
	}

	err := r.ensureRBAC(context.Background(), newEnsureRBACCluster())
	assert.EqualError(t, err, "get error")
}

func TestRedkeyClusterReconciler_EnsureRBAC_RoleErrors(t *testing.T) {
	t.Run("create error", func(t *testing.T) {
		s := getScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

		r := &RedkeyClusterReconciler{
			Client: &hookClient{
				Client: fakeClient,
				createHook: func(obj client.Object) error {
					if _, ok := obj.(*rbacv1.Role); ok {
						return errors.New("role create error")
					}
					return nil
				},
			},
			Scheme: s,
		}

		err := r.ensureRBAC(context.Background(), newEnsureRBACCluster())
		assert.EqualError(t, err, "role create error")
	})

	t.Run("get error", func(t *testing.T) {
		s := getScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

		r := &RedkeyClusterReconciler{
			Client: &hookClient{
				Client: fakeClient,
				getHook: func(obj client.Object) error {
					if _, ok := obj.(*rbacv1.Role); ok {
						return errors.New("role get error")
					}
					return nil
				},
			},
			Scheme: s,
		}

		err := r.ensureRBAC(context.Background(), newEnsureRBACCluster())
		assert.EqualError(t, err, "role get error")
	})

	t.Run("patch error", func(t *testing.T) {
		s := getScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-robin",
				Namespace: "default",
			},
			Rules: []rbacv1.PolicyRule{{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get"},
			}},
		}).Build()

		r := &RedkeyClusterReconciler{
			Client: &hookClient{
				Client: fakeClient,
				patchHook: func(obj client.Object) error {
					if _, ok := obj.(*rbacv1.Role); ok {
						return errors.New("role patch error")
					}
					return nil
				},
			},
			Scheme: s,
		}

		err := r.ensureRBAC(context.Background(), newEnsureRBACCluster())
		assert.EqualError(t, err, "role patch error")
	})
}

func TestRedkeyClusterReconciler_EnsureRBAC_RoleBindingErrors(t *testing.T) {
	t.Run("get error", func(t *testing.T) {
		s := getScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(s).Build()

		r := &RedkeyClusterReconciler{
			Client: &hookClient{
				Client: fakeClient,
				getHook: func(obj client.Object) error {
					if _, ok := obj.(*rbacv1.RoleBinding); ok {
						return errors.New("rolebinding get error")
					}
					return nil
				},
			},
			Scheme: s,
		}

		err := r.ensureRBAC(context.Background(), newEnsureRBACCluster())
		assert.EqualError(t, err, "rolebinding get error")
	})

	t.Run("delete error", func(t *testing.T) {
		s := getScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(
			&rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-robin",
					Namespace: "default",
				},
				Rules: DesiredRobinRules(),
			},
			&rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-robin",
					Namespace: "default",
				},
				Subjects: []rbacv1.Subject{{
					Kind:      "ServiceAccount",
					Name:      "test-cluster-robin",
					Namespace: "default",
				}},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "Role",
					Name:     "old-role",
				},
			},
		).Build()

		r := &RedkeyClusterReconciler{
			Client: &hookClient{
				Client: fakeClient,
				deleteHook: func(obj client.Object) error {
					if _, ok := obj.(*rbacv1.RoleBinding); ok {
						return errors.New("rolebinding delete error")
					}
					return nil
				},
			},
			Scheme: s,
		}

		err := r.ensureRBAC(context.Background(), newEnsureRBACCluster())
		assert.EqualError(t, err, "rolebinding delete error")
	})
}

func TestRedkeyClusterReconciler_EnsureRobinDeploymentExists(t *testing.T) {
	s := getScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-robin",
			Namespace: "default",
		},
	}).Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}
	ctx := context.Background()
	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "some-uid",
		},
	}

	err := r.ensureRobinDeployment(ctx, cluster)
	assert.NoError(t, err)
}

type getErrClient struct {
	client.Client
}

func (c *getErrClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return errors.New("get error")
}

type serviceAccountGetErrClient struct {
	client.Client
}

func (c *serviceAccountGetErrClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.ServiceAccount); ok {
		return errors.New("get error")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

type hookClient struct {
	client.Client
	getHook    func(obj client.Object) error
	createHook func(obj client.Object) error
	patchHook  func(obj client.Object) error
	deleteHook func(obj client.Object) error
}

func (c *hookClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.getHook != nil {
		if err := c.getHook(obj); err != nil {
			return err
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *hookClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if c.createHook != nil {
		if err := c.createHook(obj); err != nil {
			return err
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *hookClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.patchHook != nil {
		if err := c.patchHook(obj); err != nil {
			return err
		}
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *hookClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if c.deleteHook != nil {
		if err := c.deleteHook(obj); err != nil {
			return err
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func newEnsureRBACCluster() *redisv1.RedkeyCluster {
	return &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "some-uid",
		},
	}
}

func TestRedkeyClusterReconciler_EnsureRobinDeploymentGetErr(t *testing.T) {
	s := getScheme()
	fakeC := fake.NewClientBuilder().WithScheme(s).Build()
	badC := &getErrClient{Client: fakeC}

	r := &RedkeyClusterReconciler{
		Client: badC,
		Scheme: s,
	}
	ctx := context.Background()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "some-uid",
		},
	}

	err := r.ensureRobinDeployment(ctx, cluster)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get error")
}

func TestBuildDesiredRobinDeployment_Defaults(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "production",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin:     redisv1.RobinSpec{Image: "redkey-robin:latest"},
		},
	}

	deploy := r.buildDesiredRobinDeployment(cluster)

	assert.Equal(t, "my-cluster-robin", deploy.Name)
	assert.Equal(t, "production", deploy.Namespace)
	assert.Equal(t, int32(1), *deploy.Spec.Replicas)
	assert.Equal(t, "my-cluster-robin", deploy.Spec.Template.Spec.ServiceAccountName)

	assert.Equal(t, "my-cluster", deploy.Labels[ClusterLabel])
	assert.Equal(t, "redkey-robin", deploy.Labels["app"])
	assert.Equal(t, "robin", deploy.Labels["redkey.inditex.dev/component"])
	assert.Equal(t, "my-cluster", deploy.Spec.Template.Labels[ClusterLabel])
	assert.Equal(t, "redkey-robin", deploy.Spec.Template.Labels["app"])
	assert.Equal(t, "robin", deploy.Spec.Template.Labels["redkey.inditex.dev/component"])

	require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
	container := deploy.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "robin", container.Name)
	assert.Equal(t, "redkey-robin:latest", container.Image)
	assert.Equal(t, []string{
		"--cluster-name=$(CLUSTER_NAME)",
		"--namespace=$(NAMESPACE)",
	}, container.Args)
	require.Len(t, container.Env, 2)
	assert.Equal(t, "CLUSTER_NAME", container.Env[0].Name)
	assert.Equal(t, "my-cluster", container.Env[0].Value)
	assert.Equal(t, "NAMESPACE", container.Env[1].Name)
	assert.Equal(t, "production", container.Env[1].Value)
	require.Len(t, container.Ports, 1)
	assert.Equal(t, int32(8080), container.Ports[0].ContainerPort)
	assert.Equal(t, "metrics", container.Ports[0].Name)
	assert.Nil(t, deploy.Spec.Template.Annotations)
}

func TestBuildDesiredRobinDeployment_UsesSpecRobinImage(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin:     redisv1.RobinSpec{Image: "localhost:5005/redkey-robin:e2e"},
		},
	}

	deploy := r.buildDesiredRobinDeployment(cluster)
	require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "localhost:5005/redkey-robin:e2e", deploy.Spec.Template.Spec.Containers[0].Image)
}

func TestBuildDesiredRobinDeployment_WithCustomImage(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin: redisv1.RobinSpec{
				Image: "redkey-robin:latest",
				Template: &redisv1.PartialPodTemplateSpec{
					Spec: redisv1.PartialPodSpec{
						Containers: []corev1.Container{
							{
								Image: "custom-robin:v2.0.0",
							},
						},
					},
				},
			},
		},
	}

	deploy := r.buildDesiredRobinDeployment(cluster)

	require.Len(t, deploy.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "custom-robin:v2.0.0", deploy.Spec.Template.Spec.Containers[0].Image)
}

func TestBuildDesiredRobinDeployment_WithResources(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin: redisv1.RobinSpec{
				Image: "redkey-robin:latest",
				Template: &redisv1.PartialPodTemplateSpec{
					Spec: redisv1.PartialPodSpec{
						Containers: []corev1.Container{
							{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("128Mi"),
									},
									Limits: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("500m"),
										corev1.ResourceMemory: resource.MustParse("256Mi"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	deploy := r.buildDesiredRobinDeployment(cluster)

	container := deploy.Spec.Template.Spec.Containers[0]
	assert.Equal(t, resource.MustParse("100m"), container.Resources.Requests[corev1.ResourceCPU])
	assert.Equal(t, resource.MustParse("256Mi"), container.Resources.Limits[corev1.ResourceMemory])
}

func TestBuildDesiredRobinDeployment_WithPodLabelsAndAnnotations(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin: redisv1.RobinSpec{
				Image: "redkey-robin:latest",
				Template: &redisv1.PartialPodTemplateSpec{
					Metadata: metav1.ObjectMeta{
						Labels: map[string]string{
							"team": "platform",
						},
						Annotations: map[string]string{
							"prometheus.io/scrape": "true",
						},
					},
				},
			},
		},
	}

	deploy := r.buildDesiredRobinDeployment(cluster)

	// Custom labels merged with defaults
	assert.Equal(t, "platform", deploy.Spec.Template.Labels["team"])
	assert.Equal(t, "my-cluster", deploy.Spec.Template.Labels[ClusterLabel])
	assert.Equal(t, "redkey-robin", deploy.Spec.Template.Labels["app"])

	// Annotations
	assert.Equal(t, "true", deploy.Spec.Template.Annotations["prometheus.io/scrape"])
}

func TestBuildDesiredRobinDeployment_WithNodeSelectorAndTolerations(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin: redisv1.RobinSpec{
				Image: "redkey-robin:latest",
				Template: &redisv1.PartialPodTemplateSpec{
					Spec: redisv1.PartialPodSpec{
						NodeSelector: map[string]string{
							"node-type": "redis",
						},
						Tolerations: []corev1.Toleration{
							{
								Key:      "dedicated",
								Operator: corev1.TolerationOpEqual,
								Value:    "redis",
								Effect:   corev1.TaintEffectNoSchedule,
							},
						},
						PriorityClassName: "high-priority",
					},
				},
			},
		},
	}

	deploy := r.buildDesiredRobinDeployment(cluster)

	assert.Equal(t, map[string]string{"node-type": "redis"}, deploy.Spec.Template.Spec.NodeSelector)
	require.Len(t, deploy.Spec.Template.Spec.Tolerations, 1)
	assert.Equal(t, "dedicated", deploy.Spec.Template.Spec.Tolerations[0].Key)
	assert.Equal(t, "high-priority", deploy.Spec.Template.Spec.PriorityClassName)
}

func TestBuildDesiredRobinDeployment_WithAffinity(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin: redisv1.RobinSpec{
				Image: "redkey-robin:latest",
				Template: &redisv1.PartialPodTemplateSpec{
					Spec: redisv1.PartialPodSpec{
						Affinity: &corev1.Affinity{
							NodeAffinity: &corev1.NodeAffinity{
								RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
									NodeSelectorTerms: []corev1.NodeSelectorTerm{
										{
											MatchExpressions: []corev1.NodeSelectorRequirement{
												{
													Key:      "zone",
													Operator: corev1.NodeSelectorOpIn,
													Values:   []string{"eu-west-1a"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	deploy := r.buildDesiredRobinDeployment(cluster)
	assert.NotNil(t, deploy.Spec.Template.Spec.Affinity)
	assert.NotNil(t, deploy.Spec.Template.Spec.Affinity.NodeAffinity)
}

func TestBuildDesiredRobinDeployment_WithSecurityContext(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	runAsUser := int64(1000)
	runAsNonRoot := true
	readOnly := true

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin: redisv1.RobinSpec{
				Image: "redkey-robin:latest",
				Template: &redisv1.PartialPodTemplateSpec{
					Spec: redisv1.PartialPodSpec{
						SecurityContext: &corev1.PodSecurityContext{
							RunAsUser:    &runAsUser,
							RunAsNonRoot: &runAsNonRoot,
						},
						Containers: []corev1.Container{
							{
								SecurityContext: &corev1.SecurityContext{
									ReadOnlyRootFilesystem: &readOnly,
								},
							},
						},
					},
				},
			},
		},
	}

	deploy := r.buildDesiredRobinDeployment(cluster)

	assert.NotNil(t, deploy.Spec.Template.Spec.SecurityContext)
	assert.Equal(t, &runAsUser, deploy.Spec.Template.Spec.SecurityContext.RunAsUser)
	assert.NotNil(t, deploy.Spec.Template.Spec.Containers[0].SecurityContext)
	assert.Equal(t, &readOnly, deploy.Spec.Template.Spec.Containers[0].SecurityContext.ReadOnlyRootFilesystem)
}

func TestBuildDesiredRobinDeployment_WithEnvAndVolumes(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin: redisv1.RobinSpec{
				Image: "redkey-robin:latest",
				Template: &redisv1.PartialPodTemplateSpec{
					Spec: redisv1.PartialPodSpec{
						Containers: []corev1.Container{
							{
								Env: []corev1.EnvVar{
									{Name: "LOG_LEVEL", Value: "debug"},
									{Name: "CLUSTER_NAME", Value: "wrong-cluster"},
									{Name: "NAMESPACE", Value: "wrong-namespace"},
								},
								EnvFrom: []corev1.EnvFromSource{
									{ConfigMapRef: &corev1.ConfigMapEnvSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: "robin-config"},
									}},
								},
								VolumeMounts: []corev1.VolumeMount{
									{Name: "config", MountPath: "/etc/robin"},
								},
							},
						},
						Volumes: []corev1.Volume{
							{
								Name: "config",
								VolumeSource: corev1.VolumeSource{
									ConfigMap: &corev1.ConfigMapVolumeSource{
										LocalObjectReference: corev1.LocalObjectReference{Name: "robin-config"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	deploy := r.buildDesiredRobinDeployment(cluster)

	container := deploy.Spec.Template.Spec.Containers[0]
	require.Len(t, container.Env, 3)
	assert.Equal(t, "CLUSTER_NAME", container.Env[0].Name)
	assert.Equal(t, "my-cluster", container.Env[0].Value)
	assert.Equal(t, "NAMESPACE", container.Env[1].Name)
	assert.Equal(t, "default", container.Env[1].Value)
	assert.Equal(t, "LOG_LEVEL", container.Env[2].Name)
	require.Len(t, container.EnvFrom, 1)
	require.Len(t, container.VolumeMounts, 1)
	assert.Equal(t, "/etc/robin", container.VolumeMounts[0].MountPath)
	require.Len(t, deploy.Spec.Template.Spec.Volumes, 1)
	assert.Equal(t, "config", deploy.Spec.Template.Spec.Volumes[0].Name)
}

func TestBuildDesiredRobinDeployment_WithImagePullSecrets(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin: redisv1.RobinSpec{
				Image: "redkey-robin:latest",
				Template: &redisv1.PartialPodTemplateSpec{
					Spec: redisv1.PartialPodSpec{
						ImagePullSecrets: []corev1.LocalObjectReference{
							{Name: "registry-creds"},
						},
						TopologySpreadConstraints: []corev1.TopologySpreadConstraint{
							{
								MaxSkew:           1,
								TopologyKey:       "topology.kubernetes.io/zone",
								WhenUnsatisfiable: corev1.DoNotSchedule,
							},
						},
					},
				},
			},
		},
	}

	deploy := r.buildDesiredRobinDeployment(cluster)

	require.Len(t, deploy.Spec.Template.Spec.ImagePullSecrets, 1)
	assert.Equal(t, "registry-creds", deploy.Spec.Template.Spec.ImagePullSecrets[0].Name)
	require.Len(t, deploy.Spec.Template.Spec.TopologySpreadConstraints, 1)
	assert.Equal(t, "topology.kubernetes.io/zone", deploy.Spec.Template.Spec.TopologySpreadConstraints[0].TopologyKey)
}

func TestRobinDeploymentNeedsUpdate(t *testing.T) {
	s := getScheme()
	r := &RedkeyClusterReconciler{Scheme: s}

	replicas := int32(1)
	baseDeployment := func() *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster-robin",
				Namespace: "default",
				Labels: map[string]string{
					ClusterLabel: "my-cluster",
					"app":        "redkey-robin",
				},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							ClusterLabel: "my-cluster",
							"app":        "redkey-robin",
						},
					},
					Spec: corev1.PodSpec{
						ServiceAccountName: "my-cluster-robin",
						Containers: []corev1.Container{
							{
								Name:  "robin",
								Image: "redkey-robin:latest",
							},
						},
					},
				},
			},
		}
	}

	t.Run("identical deployments need no update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		assert.False(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("nil replicas triggers update", func(t *testing.T) {
		existing := baseDeployment()
		existing.Spec.Replicas = nil
		desired := baseDeployment()
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different replicas triggers update", func(t *testing.T) {
		existing := baseDeployment()
		two := int32(2)
		existing.Spec.Replicas = &two
		desired := baseDeployment()
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different deployment labels triggers update", func(t *testing.T) {
		existing := baseDeployment()
		existing.Labels["app"] = "wrong"
		desired := baseDeployment()
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different pod template labels triggers update", func(t *testing.T) {
		existing := baseDeployment()
		existing.Spec.Template.Labels["app"] = "wrong"
		desired := baseDeployment()
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different annotations triggers update", func(t *testing.T) {
		existing := baseDeployment()
		existing.Spec.Template.Annotations = map[string]string{"old": "annotation"}
		desired := baseDeployment()
		desired.Spec.Template.Annotations = map[string]string{"new": "annotation"}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different service account triggers update", func(t *testing.T) {
		existing := baseDeployment()
		existing.Spec.Template.Spec.ServiceAccountName = "old-sa"
		desired := baseDeployment()
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different image triggers update", func(t *testing.T) {
		existing := baseDeployment()
		existing.Spec.Template.Spec.Containers[0].Image = "old-image:v1"
		desired := baseDeployment()
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different resources triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.Containers[0].Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("100m"),
			},
		}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different env triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
			{Name: "NEW_VAR", Value: "value"},
		}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different envFrom triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{
			{ConfigMapRef: &corev1.ConfigMapEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "cm"},
			}},
		}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different volumeMounts triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
			{Name: "data", MountPath: "/data"},
		}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different container security context triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		readOnly := true
		desired.Spec.Template.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
			ReadOnlyRootFilesystem: &readOnly,
		}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different nodeSelector triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.NodeSelector = map[string]string{"zone": "eu"}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different tolerations triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.Tolerations = []corev1.Toleration{
			{Key: "dedicated", Value: "redis"},
		}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different affinity triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.Affinity = &corev1.Affinity{}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different pod security context triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		runAsUser := int64(1000)
		desired.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsUser: &runAsUser,
		}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different imagePullSecrets triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: "secret"},
		}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different priorityClassName triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.PriorityClassName = "high"
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different topologySpreadConstraints triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{
			{MaxSkew: 1, TopologyKey: "zone"},
		}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("different volumes triggers update", func(t *testing.T) {
		existing := baseDeployment()
		desired := baseDeployment()
		desired.Spec.Template.Spec.Volumes = []corev1.Volume{
			{Name: "data"},
		}
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("existing has no containers desired has containers", func(t *testing.T) {
		existing := baseDeployment()
		existing.Spec.Template.Spec.Containers = nil
		desired := baseDeployment()
		assert.True(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})

	t.Run("both have no containers", func(t *testing.T) {
		existing := baseDeployment()
		existing.Spec.Template.Spec.Containers = nil
		desired := baseDeployment()
		desired.Spec.Template.Spec.Containers = nil
		assert.False(t, r.robinDeploymentNeedsUpdate(existing, desired))
	})
}

func TestEnsureRobinDeployment_PatchesOnDrift(t *testing.T) {
	s := getScheme()
	testCtx := context.Background()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
			UID:       "uid-123",
		},
		Spec: redisv1.RedkeyClusterSpec{
			Primaries: 3,
			Ephemeral: true,
			Robin: redisv1.RobinSpec{
				Image: "redkey-robin:latest",
				Template: &redisv1.PartialPodTemplateSpec{
					Spec: redisv1.PartialPodSpec{
						Containers: []corev1.Container{
							{Image: "redkey-robin:v2.0.0"},
						},
					},
				},
			},
		},
	}

	// Existing deployment with old image
	oldReplicas := int32(1)
	existingDeploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster-robin",
			Namespace: "default",
			Labels: map[string]string{
				ClusterLabel: "my-cluster",
				"app":        "redkey-robin",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &oldReplicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					ClusterLabel: "my-cluster",
					"app":        "redkey-robin",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						ClusterLabel: "my-cluster",
						"app":        "redkey-robin",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "my-cluster-robin",
					Containers: []corev1.Container{
						{
							Name:  "robin",
							Image: "redkey-robin:v1.0.0", // Old image
						},
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(existingDeploy).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	err := r.ensureRobinDeployment(testCtx, cluster)
	require.NoError(t, err)

	// Verify the deployment was patched
	var updated appsv1.Deployment
	err = fakeClient.Get(testCtx, types.NamespacedName{Name: "my-cluster-robin", Namespace: "default"}, &updated)
	require.NoError(t, err)
	assert.Equal(t, "redkey-robin:v2.0.0", updated.Spec.Template.Spec.Containers[0].Image)
}

func TestRedkeyClusterReconciler_EnsureRBAC_PatchesRoleOnDrift(t *testing.T) {
	s := getScheme()
	testCtx := context.Background()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
			UID:       "uid-123",
		},
	}

	existingRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster-robin",
			Namespace: "default",
		},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"get"},
		}},
	}

	existingRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster-robin",
			Namespace: "default",
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      "my-cluster-robin",
			Namespace: "default",
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "my-cluster-robin",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(existingRole, existingRoleBinding).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	err := r.ensureRBAC(testCtx, cluster)
	require.NoError(t, err)

	var updatedRole rbacv1.Role
	err = fakeClient.Get(testCtx, types.NamespacedName{Name: "my-cluster-robin", Namespace: "default"}, &updatedRole)
	require.NoError(t, err)
	assert.Equal(t, DesiredRobinRules(), updatedRole.Rules)

	var createdServiceAccount corev1.ServiceAccount
	err = fakeClient.Get(testCtx, types.NamespacedName{Name: "my-cluster-robin", Namespace: "default"}, &createdServiceAccount)
	require.NoError(t, err)
}

func TestRedkeyClusterReconciler_EnsureRBAC_PatchesRoleBindingSubjectsOnDrift(t *testing.T) {
	s := getScheme()
	testCtx := context.Background()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
			UID:       "uid-123",
		},
	}

	existingRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster-robin",
			Namespace: "default",
		},
		Rules: DesiredRobinRules(),
	}

	existingRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster-robin",
			Namespace: "default",
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      "wrong-name",
			Namespace: "default",
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "my-cluster-robin",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(existingRole, existingRoleBinding).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	err := r.ensureRBAC(testCtx, cluster)
	require.NoError(t, err)

	var updatedRoleBinding rbacv1.RoleBinding
	err = fakeClient.Get(testCtx, types.NamespacedName{Name: "my-cluster-robin", Namespace: "default"}, &updatedRoleBinding)
	require.NoError(t, err)
	require.Len(t, updatedRoleBinding.Subjects, 1)
	assert.Equal(t, "my-cluster-robin", updatedRoleBinding.Subjects[0].Name)
}

func TestRedkeyClusterReconciler_EnsureRBAC_RecreatesRoleBindingOnRoleRefDrift(t *testing.T) {
	s := getScheme()
	testCtx := context.Background()

	cluster := &redisv1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
			UID:       "uid-123",
		},
	}

	existingRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster-robin",
			Namespace: "default",
		},
		Rules: DesiredRobinRules(),
	}

	existingRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster-robin",
			Namespace: "default",
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      "my-cluster-robin",
			Namespace: "default",
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "old-role",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(existingRole, existingRoleBinding).
		Build()

	r := &RedkeyClusterReconciler{
		Client: fakeClient,
		Scheme: s,
	}

	err := r.ensureRBAC(testCtx, cluster)
	require.NoError(t, err)

	var updatedRoleBinding rbacv1.RoleBinding
	err = fakeClient.Get(testCtx, types.NamespacedName{Name: "my-cluster-robin", Namespace: "default"}, &updatedRoleBinding)
	require.NoError(t, err)
	assert.Equal(t, "my-cluster-robin", updatedRoleBinding.RoleRef.Name)
	assert.Equal(t, "Role", updatedRoleBinding.RoleRef.Kind)
	require.Len(t, updatedRoleBinding.Subjects, 1)
	assert.Equal(t, "my-cluster-robin", updatedRoleBinding.Subjects[0].Name)
}

func TestRedkeyClusterReconciler_CreateIfNotExists(t *testing.T) {
	s := getScheme()
	testCtx := context.Background()

	t.Run("creates missing object", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
		r := &RedkeyClusterReconciler{Client: fakeClient, Scheme: s}

		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "create-if-missing",
				Namespace: "default",
			},
		}

		err := r.createIfNotExists(testCtx, serviceAccount)
		require.NoError(t, err)

		var stored corev1.ServiceAccount
		err = fakeClient.Get(testCtx, types.NamespacedName{Name: "create-if-missing", Namespace: "default"}, &stored)
		require.NoError(t, err)
	})

	t.Run("returns nil when object already exists", func(t *testing.T) {
		existing := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "already-exists",
				Namespace: "default",
			},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
		r := &RedkeyClusterReconciler{Client: fakeClient, Scheme: s}

		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "already-exists",
				Namespace: "default",
			},
		}

		err := r.createIfNotExists(testCtx, serviceAccount)
		require.NoError(t, err)

		var serviceAccounts corev1.ServiceAccountList
		err = fakeClient.List(testCtx, &serviceAccounts)
		require.NoError(t, err)
		assert.Len(t, serviceAccounts.Items, 1)
	})

	t.Run("propagates get errors", func(t *testing.T) {
		fakeClient := fake.NewClientBuilder().WithScheme(s).Build()
		r := &RedkeyClusterReconciler{
			Client: &serviceAccountGetErrClient{Client: fakeClient},
			Scheme: s,
		}

		serviceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "get-error",
				Namespace: "default",
			},
		}

		err := r.createIfNotExists(testCtx, serviceAccount)
		assert.EqualError(t, err, "get error")
	})
}
