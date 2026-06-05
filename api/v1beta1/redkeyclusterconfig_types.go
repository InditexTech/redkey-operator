// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConfigPhase constants for RedkeyClusterConfig lifecycle.
const (
	ConfigPhasePending    = "Pending"
	ConfigPhaseInProgress = "InProgress"
	ConfigPhaseSuperseded = "Superseded"
	ConfigPhaseApplied    = "Applied"
)

// Operational phase constants for RedkeyClusterConfig status.
const (
	ClusterStatusInitializing  = "Initializing"
	ClusterStatusConfiguring   = "Configuring"
	ClusterStatusReady         = "Ready"
	ClusterStatusScalingUp     = "ScalingUp"
	ClusterStatusScalingDown   = "ScalingDown"
	ClusterStatusScalingToZero = "ScalingToZero"
	ClusterStatusUpgrading     = "Upgrading"
	ClusterStatusMaintenance   = "Maintenance"
	ClusterPhaseRebalancing    = "Rebalancing"
	ClusterPhaseError          = "Error"
)

// Substatus constants for scaling operations (informational only — not used for control flow).
const (
	// Common substatus values shared across scaling operations.
	SubstatusWaitingForPods    = "WaitingForPods"
	SubstatusRebalancing       = "Rebalancing"
	SubstatusAttachingReplicas = "AttachingReplicas"
	SubstatusVerifying         = "Verifying"

	// ScaleUp specific.
	SubstatusInitializingNodes = "InitializingNodes"

	// ScaleDown specific.
	SubstatusDrainingPrimaries    = "DrainingPrimaries"
	SubstatusRemovingNodes        = "RemovingNodes"
	SubstatusShrinkingStatefulSet = "ShrinkingStatefulSet"

	// FastScaling specific.
	SubstatusDeletingStatefulSet = "DeletingStatefulSet"
	SubstatusRecreatingCluster   = "RecreatingCluster"
	SubstatusFormingCluster      = "FormingCluster"

	// ScaleToZero specific.
	SubstatusDeletingResources = "DeletingResources"
	SubstatusDeletingPVCs      = "DeletingPVCs"

	// Upgrade — Rolling N+1 substatus values.
	SubstatusUpgradeScalingUp     = "AddingExtraNode"   // Scaling StatefulSet +1 (or +replicas) and meeting extra node(s).
	SubstatusUpgradeResharding    = "DrainingNode"      // Migrating slots from current partition node to destination.
	SubstatusUpgradeRollingUpdate = "RollingUpdate"     // Waiting for pod at current partition to be recreated with new image.
	SubstatusUpgradeEnding        = "MovingLastSlots"   // Migrating slots from extra node back to node 0.
	SubstatusUpgradeScalingDown   = "RemovingExtraNode" // Scaling StatefulSet back to original size.

	// Upgrade — Fast Upgrade substatus values.
	SubstatusFastUpgrading      = "FastUpgrading"      // StatefulSet updated and pods deleted for fast upgrade.
	SubstatusEndingFastUpgrade  = "FormingCluster"     // Waiting for pods to restart and cluster to reform.
	SubstatusFastUpgradeForming = "FastUpgradeForming" // Stale state cleared once; forming the new cluster idempotently.
)

// RedkeyClusterConfigSpec defines the desired state of RedkeyClusterConfig.
// This spec is immutable after creation — set by the operator, read-only for Robin.
type RedkeyClusterConfigSpec struct {
	// Sequence is a monotonically increasing counter set by the Operator.
	// +kubebuilder:validation:Required
	Sequence int `json:"sequence"`

	// SkipIfSuperseded indicates whether Robin may skip this config if a newer one is pending.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	SkipIfSuperseded bool `json:"skipIfSuperseded"`

	// Primaries specifies the number of Redis primary nodes in the cluster.
	// +kubebuilder:validation:Required
	Primaries int32 `json:"primaries"`

	// ReplicasPerPrimary specifies how many replicas should be attached to each Redis Primary node.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=0
	ReplicasPerPrimary int32 `json:"replicasPerPrimary"`

	// Ephemeral indicates whether storage is not persisted across pod restarts.
	// +kubebuilder:validation:Optional
	Ephemeral bool `json:"ephemeral"`

	// Storage is the amount of persistent storage to request for each Redis node.
	// +kubebuilder:validation:Optional
	Storage string `json:"storage,omitempty"`

	// StorageClassName is the name of the StorageClass to use for the PVC.
	// +kubebuilder:validation:Optional
	StorageClassName string `json:"storageClassName,omitempty"`

	// Image is the Redis image to use.
	// +kubebuilder:validation:Optional
	Image string `json:"image,omitempty"`

	// Version is the Redis version.
	// +kubebuilder:validation:Optional
	Version string `json:"version,omitempty"`

	// RedisConfig is the merged redis.conf content.
	// +kubebuilder:validation:Optional
	RedisConfig string `json:"redisConfig,omitempty"`

	// Resources is the resource requirements for the Redis pods.
	// +kubebuilder:validation:Optional
	Resources *v1.ResourceRequirements `json:"resources,omitempty"`

	// Auth references the Secret containing Redis authentication credentials.
	// +kubebuilder:validation:Optional
	Auth RedisAuth `json:"auth,omitempty"`

	// Labels are additional pod labels.
	// +kubebuilder:validation:Optional
	Labels *map[string]string `json:"labels,omitempty"`

	// Annotations are additional pod annotations.
	// +kubebuilder:validation:Optional
	Annotations *map[string]string `json:"annotations,omitempty"`

	// DeletePVC specifies if the PVC should be deleted when the cluster is deleted.
	// +kubebuilder:validation:Optional
	DeletePVC *bool `json:"deletePVC,omitempty"`

	// PurgeKeysOnRebalance specifies if keys should be purged on rebalance (ephemeral only).
	// +kubebuilder:validation:Optional
	PurgeKeysOnRebalance *bool `json:"purgeKeysOnRebalance,omitempty"`

	// Override provides ability to override generated child resources.
	// +kubebuilder:validation:Optional
	Override *RedkeyClusterOverrideSpec `json:"override,omitempty"`

	// Pdb is the PodDisruptionBudget configuration.
	// +kubebuilder:validation:Optional
	Pdb Pdb `json:"pdb,omitempty"`

	// RobinConfig provides Robin's operational settings overrides.
	// +kubebuilder:validation:Optional
	RobinConfig *RobinConfig `json:"robinConfig,omitempty"`
}

// RedkeyClusterConfigStatus defines the observed state of RedkeyClusterConfig.
// Written exclusively by Robin via the status subresource.
type RedkeyClusterConfigStatus struct {
	// ConfigPhase is the configuration lifecycle phase: Pending, InProgress, Superseded, or Applied.
	ConfigPhase string `json:"configPhase,omitempty"`

	// Status is the cluster operational status reported by Robin.
	Status string `json:"status"`

	// Substatus provides detailed sub-status information.
	Substatus RedkeyClusterSubstatus `json:"substatus"`

	// Nodes is the per-node status map.
	Nodes map[string]*RedisNode `json:"nodes"`

	// Conditions represent the latest available observations.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastUpdatedAt is the last time the status was updated.
	LastUpdatedAt *metav1.Time `json:"lastUpdatedAt,omitempty"`

	// ObservedGeneration is the most recent generation observed.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// RedkeyClusterSubstatus defines detailed sub-status information.
type RedkeyClusterSubstatus struct {
	Status             string `json:"status"`
	UpgradingPartition int    `json:"upgradingPartition"`
}

// RedisNode defines a Redis node status.
type RedisNode struct {
	Role              string `json:"role,omitempty"`
	IP                string `json:"ip,omitempty"`
	ReplicationStatus string `json:"replicationStatus,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rkcc
// +kubebuilder:printcolumn:name="Cluster",type="string",priority=0,JSONPath=".metadata.labels.redkey\\.inditex\\.dev/cluster",description="Parent RedkeyCluster"
// +kubebuilder:printcolumn:name="Sequence",type="integer",priority=0,JSONPath=".spec.sequence",description="Configuration sequence number"
// +kubebuilder:printcolumn:name="ConfigPhase",type="string",priority=0,JSONPath=".status.configPhase",description="Configuration lifecycle phase"
// +kubebuilder:printcolumn:name="Status",type="string",priority=0,JSONPath=".status.status",description="Cluster operational status"
// +kubebuilder:printcolumn:name="Substatus",type="string",priority=0,JSONPath=".status.substatus.status",description="The cluster substatus"
// +kubebuilder:printcolumn:name="Partition",type="string",priority=0,JSONPath=".status.substatus.upgradingPartition",description="Upgrading partition"

// RedkeyClusterConfig is the Schema for the redkeyclusterconfigs API.
// It represents a sequenced desired-state snapshot created by the Operator and consumed by Robin.
type RedkeyClusterConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RedkeyClusterConfigSpec   `json:"spec,omitempty"`
	Status RedkeyClusterConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RedkeyClusterConfigList contains a list of RedkeyClusterConfig.
type RedkeyClusterConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RedkeyClusterConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RedkeyClusterConfig{}, &RedkeyClusterConfigList{})
}
