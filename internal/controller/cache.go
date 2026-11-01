// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ComponentLabel identifies the Redkey component an object belongs to.
	ComponentLabel = "redkey.inditex.dev/component"
	// RobinComponent is the ComponentLabel value of every object the operator creates for Robin.
	RobinComponent = "robin"
)

// RobinObjectSelector matches the objects the operator creates for Robin
// (Deployment, ServiceAccount, Role and RoleBinding).
func RobinObjectSelector() labels.Selector {
	return labels.SelectorFromSet(labels.Set{ComponentLabel: RobinComponent})
}

// ConfigureCache scopes the manager cache to the objects the operator actually manages.
//
// Without a label selector, every owned core type (Deployment, ServiceAccount, Role,
// RoleBinding) is cached cluster-wide, so memory grows with the size of the cluster
// instead of with the number of Redkeys. Restricting those informers to the Robin
// component label and stripping managedFields keeps the cache proportional to the
// Redkeys being managed. Any new owned or cached core type must be added here too.
func ConfigureCache(opts *cache.Options) {
	selector := RobinObjectSelector()
	if opts.ByObject == nil {
		opts.ByObject = map[client.Object]cache.ByObject{}
	}
	for _, obj := range []client.Object{
		&appsv1.Deployment{},
		&corev1.ServiceAccount{},
		&rbacv1.Role{},
		&rbacv1.RoleBinding{},
	} {
		opts.ByObject[obj] = cache.ByObject{Label: selector}
	}
	opts.DefaultTransform = cache.TransformStripManagedFields()
}
