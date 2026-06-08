// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	redkeyv1beta1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

const (
	namespacePollInterval = 2 * time.Second
	namespaceWaitTimeout  = 120 * time.Second
)

// CreateNamespace creates a namespace with a generated name and waits for it to be active.
func CreateNamespace(ctx context.Context, c client.Client, prefix string) (*corev1.Namespace, error) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix + "-",
			Labels: map[string]string{
				"pod-security.kubernetes.io/enforce": "privileged",
				"pod-security.kubernetes.io/audit":   "privileged",
				"pod-security.kubernetes.io/warn":    "privileged",
			},
		},
	}
	if err := c.Create(ctx, ns); err != nil {
		return nil, fmt.Errorf("creating namespace: %w", err)
	}

	err := wait.PollUntilContextTimeout(ctx, namespacePollInterval, namespaceWaitTimeout, true,
		func(ctx context.Context) (bool, error) {
			var tmp corev1.Namespace
			if err := c.Get(ctx, client.ObjectKey{Name: ns.Name}, &tmp); err != nil {
				return false, nil
			}
			return tmp.Status.Phase == corev1.NamespaceActive, nil
		})
	if err != nil {
		return nil, fmt.Errorf("namespace %s not ready: %w", ns.Name, err)
	}
	return ns, nil
}

// DeleteNamespace cleans up RedkeyCluster CRs (removing finalizers) and deletes the namespace.
func DeleteNamespace(ctx context.Context, c client.Client, ns *corev1.Namespace) error {
	if ns == nil {
		return nil
	}

	// Remove any RedkeyCluster CRs so their finalizers don't stall deletion
	var clusterList redkeyv1beta1.RedkeyClusterList
	if err := c.List(ctx, &clusterList, &client.ListOptions{Namespace: ns.Name}); err == nil {
		for i := range clusterList.Items {
			name := clusterList.Items[i].Name
			namespace := clusterList.Items[i].Namespace

			// Strip finalizers
			_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				rc := &redkeyv1beta1.RedkeyCluster{}
				if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, rc); err != nil {
					return err
				}
				rc.Finalizers = nil
				return c.Update(ctx, rc)
			})

			// Delete the CR
			_ = c.Delete(ctx, &redkeyv1beta1.RedkeyCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			})
		}
	}

	// Also remove finalizers from configs
	var configList redkeyv1beta1.RedkeyClusterConfigList
	if err := c.List(ctx, &configList, &client.ListOptions{Namespace: ns.Name}); err == nil {
		for i := range configList.Items {
			name := configList.Items[i].Name
			namespace := configList.Items[i].Namespace

			_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				cfg := &redkeyv1beta1.RedkeyClusterConfig{}
				if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, cfg); err != nil {
					return err
				}
				cfg.Finalizers = nil
				return c.Update(ctx, cfg)
			})
		}
	}

	// Delete namespace
	if err := c.Delete(ctx, ns); err != nil {
		return fmt.Errorf("deleting namespace %s: %w", ns.Name, err)
	}

	// Wait for namespace to disappear
	err := wait.PollUntilContextTimeout(ctx, namespacePollInterval, namespaceWaitTimeout, true,
		func(ctx context.Context) (bool, error) {
			err := c.Get(ctx, types.NamespacedName{Name: ns.Name}, &corev1.Namespace{})
			if err != nil {
				return true, nil // gone
			}
			return false, nil
		})
	if err != nil {
		return fmt.Errorf("namespace %s still exists after timeout: %w", ns.Name, err)
	}
	return nil
}

// DeleteRedkeyCluster removes a single RedkeyCluster CR and its associated configs from a
// namespace, stripping finalizers so deletion is not stalled, and waits for the cluster CR to
// disappear. It is intended for per-Context cleanup in Ordered specs that share a namespace, so
// that clusters do not accumulate (and starve node CPU) until the whole Describe finishes.
func DeleteRedkeyCluster(ctx context.Context, c client.Client, name, namespace string) error {
	// Strip finalizers from the cluster's configs first so they don't block CR deletion.
	var configList redkeyv1beta1.RedkeyClusterConfigList
	if err := c.List(ctx, &configList,
		client.InNamespace(namespace),
		client.MatchingLabels{"redkey.inditex.dev/cluster": name},
	); err == nil {
		for i := range configList.Items {
			cfgName := configList.Items[i].Name
			_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				cfg := &redkeyv1beta1.RedkeyClusterConfig{}
				if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: cfgName}, cfg); err != nil {
					return err
				}
				cfg.Finalizers = nil
				return c.Update(ctx, cfg)
			})
			_ = c.Delete(ctx, &redkeyv1beta1.RedkeyClusterConfig{
				ObjectMeta: metav1.ObjectMeta{Name: cfgName, Namespace: namespace},
			})
		}
	}

	// Strip the cluster CR's own finalizers, then delete it.
	_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		rc := &redkeyv1beta1.RedkeyCluster{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, rc); err != nil {
			return err
		}
		rc.Finalizers = nil
		return c.Update(ctx, rc)
	})
	_ = c.Delete(ctx, &redkeyv1beta1.RedkeyCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	})

	// Wait for the cluster CR to disappear so its pods are released before the next Context starts.
	err := wait.PollUntilContextTimeout(ctx, namespacePollInterval, namespaceWaitTimeout, true,
		func(ctx context.Context) (bool, error) {
			err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &redkeyv1beta1.RedkeyCluster{})
			if err != nil {
				return true, nil // gone
			}
			return false, nil
		})
	if err != nil {
		return fmt.Errorf("redkeycluster %s/%s still exists after timeout: %w", namespace, name, err)
	}
	return nil
}
