// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// RedkeySpec defines the desired state of Redkey.
// +kubebuilder:validation:XValidation:rule="self.ephemeral || has(self.storage)", message="Ephemeral or storage must be set"
// +kubebuilder:validation:XValidation:rule="!(self.ephemeral && has(self.storage))", message="Ephemeral and storage cannot be combined"
// +kubebuilder:validation:XValidation:rule="!(!self.ephemeral && self.purgeKeysOnRebalance == true)", message="Cannot set purgeKeysOnRebalance to true for non-ephemeral clusters"
// +kubebuilder:validation:XValidation:rule="self.mode == oldSelf.mode", message="Changing the mode field is not allowed"
// +kubebuilder:validation:XValidation:rule="self.mode != 'standalone' || self.primaries <= 1", message="Standalone mode allows at most 1 primary (0 to scale to zero)"
// +kubebuilder:validation:XValidation:rule="self.mode != 'standalone' || self.replicasPerPrimary == 0", message="Standalone mode does not allow replicas (replicasPerPrimary must be 0)"
type RedkeySpec struct {
	// +kubebuilder:validation:Optional
	// RedisAuth
	Auth RedisAuth `json:"auth,omitempty"`

	// +kubebuilder:validation:Optional
	// Redis version
	Version string `json:"version,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=cluster
	// +kubebuilder:validation:Enum=cluster;standalone
	// Mode selects the deployment topology: "cluster" (Redis Cluster) or "standalone"
	// (a single, non-clustered Redis instance). Standalone is immutable once set and
	// restricts the cluster to at most one primary with no replicas.
	Mode string `json:"mode,omitempty"`

	// Primaries specifies the number of Redis primary nodes in the cluster.
	// A value of 0 means the cluster is scaled to zero — no Kubernetes objects will be created.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	Primaries int32 `json:"primaries"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=0
	// ReplicasPerPrimary specifies how many replicas should be attached to each Redis Primary node.
	ReplicasPerPrimary int32 `json:"replicasPerPrimary"`

	// +kubebuilder:validation:Optional
	// Image is the Redis image to use.
	Image string `json:"image,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	// DeletePVC specifies if the PVC should be deleted when the Redkey is deleted.
	DeletePVC *bool `json:"deletePVC"`

	// +kubebuilder:validation:Optional
	// Backup specifies if the Redkey should be backed up.
	Backup bool `json:"backup,omitempty"`

	// +kubebuilder:validation:Required
	// Robin specifies the Robin sidecar configuration for the Redkey.
	Robin RobinSpec `json:"robin"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	// SkipIfSuperseded indicates whether Robin may skip intermediate configs if a newer one is pending.
	SkipIfSuperseded bool `json:"skipIfSuperseded"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	// PurgeKeysOnRebalance specifies if keys should be purged on rebalance.
	PurgeKeysOnRebalance *bool `json:"purgeKeysOnRebalance"`

	// +kubebuilder:validation:Optional
	// Config is the Redis configuration to use.
	Config string `json:"config,omitempty"`

	// +kubebuilder:validation:Optional
	// Resources is the resource requirements for the Redkey.
	Resources *v1.ResourceRequirements `json:"resources,omitempty"`

	// +kubebuilder:validation:Optional
	// Labels are applied to all Kubernetes objects managed for this Redkey
	// (the RedkeyConfig, the Robin Deployment and its RBAC, and the Redis
	// StatefulSet, Service, ConfigMap and PodDisruptionBudget — including the pods).
	// Internal labels required for correct operation always take precedence on a
	// key collision. An object-level override (see Override / Robin.Template) fully
	// replaces these labels for that object.
	Labels *map[string]string `json:"labels,omitempty"`

	// +kubebuilder:validation:Optional
	// Annotations are applied to all Kubernetes objects managed for this Redkey
	// (the RedkeyConfig, the Robin Deployment and its RBAC, and the Redis
	// StatefulSet, Service, ConfigMap and PodDisruptionBudget — including the pods).
	// Internal annotations required for correct operation always take precedence on a
	// key collision. An object-level override (see Override / Robin.Template) fully
	// replaces these annotations for that object.
	Annotations *map[string]string `json:"annotations,omitempty"`

	// +kubebuilder:validation:Optional
	// Pdb is the PodDisruptionBudget configuration for the Redkey.
	Pdb Pdb `json:"pdb,omitempty"`

	// +kubebuilder:validation:Optional
	Override *RedkeyOverrideSpec `json:"override,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Changing the ephemeral field is not allowed"
	// Ephemeral storage is not persisted across pod restarts.
	Ephemeral bool `json:"ephemeral"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Changing the storage size is not allowed"
	// Storage is the amount of persistent storage to request for each Redis node.
	Storage string `json:"storage,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=""
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Changing the storage class name is not allowed"
	// StorageClassName is the name of the StorageClass to use for the PVC.
	StorageClassName string `json:"storageClassName"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={ReadWriteOnce}
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Changing the storage access modes is not allowed"
	// +kubebuilder:validation:items:Enum={ReadWriteOnce,ReadOnlyMany,ReadWriteMany}
	// AccessModes is the list of access modes for the PVC.
	AccessModes []v1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`
}

// NodesNeeded returns the total number of nodes needed for the Redkey.
func (s RedkeySpec) NodesNeeded() int {
	return int(s.Primaries + (s.Primaries * s.ReplicasPerPrimary))
}

// Deployment mode constants.
const (
	// ModeCluster deploys a Redis Cluster (the default).
	ModeCluster = "cluster"
	// ModeStandalone deploys a single, non-clustered Redis instance.
	ModeStandalone = "standalone"
)

// IsStandalone reports whether the cluster is configured in standalone mode.
func (s RedkeySpec) IsStandalone() bool {
	return s.Mode == ModeStandalone
}

// Redkey phase constants.
const (
	PhaseReady       = "Ready"
	PhaseConfiguring = "Configuring"
	PhaseError       = "Error"
)

// RedkeyStatus defines the observed state of Redkey.
// This is a simplified, user-facing status aggregated from RedkeyConfig.
type RedkeyStatus struct {
	// Replicas is the current number of primary nodes in the cluster.
	// Used by the scale subresource.
	Replicas int32 `json:"replicas,omitempty"`

	// Phase is a user-facing summary derived from Conditions: Ready, Configuring, or Error.
	Phase string `json:"phase"`

	// Status mirrors the operational phase reported by the highest-sequence RedkeyConfig.
	Status string `json:"status,omitempty"`

	// Substatus mirrors the detailed sub-status reported by the highest-sequence RedkeyConfig.
	Substatus RedkeySubstatus `json:"substatus,omitempty"`

	// Nodes mirrors the per-node status map reported by the highest-sequence RedkeyConfig.
	Nodes map[string]*RedisNode `json:"nodes"`

	// Conditions represent the latest available observations of the cluster state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastUpdatedAt is the last time the status was updated.
	LastUpdatedAt *metav1.Time `json:"lastUpdatedAt,omitempty"`

	// ObservedGeneration is the most recent generation observed.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastAppliedPrimaries records the number of primaries of the last successfully applied,
	// non-zero topology of a storage (non-ephemeral) cluster.
	//
	// WARNING — race-free scale-from-zero guard. This field is CONTINUOUSLY updated by the operator
	// on every successful reconcile while the cluster is applied with primaries>0, and is NEVER
	// cleared. It is intentionally maintained during steady state (NOT only at scale-to-zero) so the
	// value is always persisted BEFORE any scale-to-zero. The root-level CEL validation rule on
	// Redkey uses it to force a scale-up from zero back to the exact previous topology, preventing
	// inconsistent PVC/node/slot remounts. Do NOT change this to record-on-scale-down-only and do NOT
	// add clearing logic: either would reintroduce the admission-vs-reconcile race, because CEL runs
	// synchronously at admission while the operator writes status asynchronously afterwards.
	// Ephemeral clusters never set this field (they lose data on scale-to-zero, so no lock is needed).
	//
	// The Maximum bound keeps the CEL messageExpression that references this field within the API
	// server's cost budget (it lets the cost estimator bound the size of string(...) conversions).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100000
	LastAppliedPrimaries int32 `json:"lastAppliedPrimaries,omitempty"`

	// LastAppliedReplicasPerPrimary records the replicasPerPrimary of the last successfully applied,
	// non-zero topology of a storage (non-ephemeral) cluster. See LastAppliedPrimaries for the
	// continuous-tracking / race-free rationale — the same WARNING applies here.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100000
	LastAppliedReplicasPerPrimary int32 `json:"lastAppliedReplicasPerPrimary,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.primaries,statuspath=.status.replicas
// +kubebuilder:resource:shortName=rk
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Mode",type="string",priority=0,JSONPath=".spec.mode",description="Deployment mode: cluster or standalone"
// +kubebuilder:printcolumn:name="Primaries",type="integer",priority=0,JSONPath=".spec.primaries",description="Amount of Redis primary nodes"
// +kubebuilder:printcolumn:name="Replicas",type="integer",priority=0,JSONPath=".spec.replicasPerPrimary",description="Amount of replicas per primary node"
// +kubebuilder:printcolumn:name="Ephemeral",type="boolean",priority=0,JSONPath=".spec.ephemeral",description="Cluster ephemeral"
// +kubebuilder:printcolumn:name="PurgeKeys",type="boolean",priority=0,JSONPath=".spec.purgeKeysOnRebalance",description="Purge keys on rebalance"
// +kubebuilder:printcolumn:name="Storage",type="string",priority=1,JSONPath=".spec.storage",description="Amount of storage for Redis"
// +kubebuilder:printcolumn:name="DeletePVC",type="boolean",priority=1,JSONPath=".spec.deletePVC",description="Deleve PVC"
// +kubebuilder:printcolumn:name="Phase",type="string",priority=0,JSONPath=".status.phase",description="The cluster phase: Ready, Configuring, or Error"
// +kubebuilder:printcolumn:name="Status",type="string",priority=1,JSONPath=".status.status",description="The cluster status"
// +kubebuilder:printcolumn:name="Substatus",type="string",priority=1,JSONPath=".status.substatus.status",description="The cluster substatus"
// +kubebuilder:printcolumn:name="Partition",type="string",priority=1,JSONPath=".status.substatus.upgradingPartition",description="Upgrading partition"
//
// Scale-from-zero topology lock (storage/non-ephemeral clusters only). These are ROOT-level rules
// (placed on the Redkey type, not RedkeySpec) because only root-level rules can read self.status.
// They rely on status.lastAppliedPrimaries / status.lastAppliedReplicasPerPrimary, which the
// operator maintains CONTINUOUSLY while the cluster runs at primaries>0 (see RedkeyStatus fields).
// That continuous tracking is what makes this check RACE-FREE: the previous topology is already
// persisted before any scale-to-zero. Free scaling while primaries>0 stays unrestricted; only
// scaling UP from zero is forced back to the exact previous topology. Ephemeral clusters and
// fresh clusters created at zero (which never recorded a topology) are unaffected.
//
// NOTE: no messageExpression is used here. This CRD schema is very large (it embeds full
// StatefulSet/PodSpec overrides), and a root-level messageExpression that reads self.status is
// estimated by the API server's CEL cost budget against that whole schema, which exceeds the
// (stricter) messageExpression budget by >100x even for a trivial expression. The static message
// therefore points users to the exact required numbers in .status.lastAppliedPrimaries /
// .status.lastAppliedReplicasPerPrimary instead of interpolating them.
// +kubebuilder:validation:XValidation:rule="self.spec.ephemeral || oldSelf.spec.primaries != 0 || self.spec.primaries == 0 || !has(self.status.lastAppliedPrimaries) || self.spec.primaries == self.status.lastAppliedPrimaries",message="For storage clusters, scaling up from zero must restore the same primaries the cluster had before scaling to zero (see .status.lastAppliedPrimaries)"
// +kubebuilder:validation:XValidation:rule="self.spec.ephemeral || oldSelf.spec.primaries != 0 || self.spec.primaries == 0 || !has(self.status.lastAppliedPrimaries) || self.spec.replicasPerPrimary == (has(self.status.lastAppliedReplicasPerPrimary) ? self.status.lastAppliedReplicasPerPrimary : 0)",message="For storage clusters, scaling up from zero must restore the same replicasPerPrimary the cluster had before scaling to zero (see .status.lastAppliedReplicasPerPrimary)"

// Redkey is the Schema for the redkeys API.
type Redkey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RedkeySpec   `json:"spec,omitempty"`
	Status RedkeyStatus `json:"status,omitempty"`
}

// NamespacedName returns the NamespacedName of the Redkey.
func (r Redkey) NamespacedName() types.NamespacedName {
	return types.NamespacedName{
		Namespace: r.GetNamespace(),
		Name:      r.GetName(),
	}
}

// GetLabels returns the labels from Spec.Labels or an empty map if nil.
func (r Redkey) GetLabels() map[string]string {
	if r.Spec.Labels != nil {
		return *r.Spec.Labels
	}
	return map[string]string{}
}

// +kubebuilder:object:root=true

// RedkeyList contains a list of Redkey.
type RedkeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Redkey `json:"items"`
}

// RobinSpec defines the desired state of Robin configuration for Redkey.
type RobinSpec struct {
	// +kubebuilder:validation:Required
	// Image is the Robin container image to deploy for this cluster.
	Image string `json:"image"`

	// +kubebuilder:validation:Optional
	// Resources defines the resource requirements for the Robin container.
	Resources *v1.ResourceRequirements `json:"resources,omitempty"`

	// +kubebuilder:validation:Optional
	// Template allows advanced overrides of the Robin Deployment's PodTemplateSpec.
	// Fields set here take precedence over first-level fields like Resources.
	Template *PartialPodTemplateSpec `json:"template,omitempty"`

	Config *RobinConfig `json:"config,omitempty"`
}

// RobinConfig defines Robin's operational settings.
type RobinConfig struct {
	Reconciler *RobinConfigReconciler `json:"reconciler,omitempty"`
	Cluster    *RobinConfigCluster    `json:"cluster,omitempty"`
	Metrics    *RobinConfigMetrics    `json:"metrics,omitempty"`
	Profiling  *RobinConfigProfiling  `json:"profiling,omitempty"`
}

// RobinConfigProfiling defines profiling configuration for Robin.
// When enabled, pprof endpoints are served on the metrics HTTP server
// allowing runtime diagnosis of memory, CPU, and goroutine issues.
// This setting can be toggled at runtime without restarting the Robin pod.
type RobinConfigProfiling struct {
	// Enabled activates pprof profiling endpoints on the metrics server.
	// Defaults to false. Toggle this field to enable/disable profiling at runtime.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// RobinConfigReconciler defines reconciler configuration for Robin.
type RobinConfigReconciler struct {
	IntervalSeconds        *int `json:"intervalSeconds,omitempty"`
	IntervalOnErrorSeconds *int `json:"intervalOnErrorSeconds,omitempty"`
	IntervalOnWaitSeconds  *int `json:"intervalOnWaitSeconds,omitempty"`
}

// RobinConfigCluster defines cluster configuration for Robin.
type RobinConfigCluster struct {
	ConnectionMaxRetries         *int `json:"connectionMaxRetries,omitempty"`
	ConnectionBackOffSeconds     *int `json:"connectionBackOffSeconds,omitempty"`
	ClusterCommandTimeoutSeconds *int `json:"clusterCommandTimeoutSeconds,omitempty"`
	ClusterMeetWaitSeconds       *int `json:"clusterMeetWaitSeconds,omitempty"`
	RebalanceTimeoutSeconds      *int `json:"rebalanceTimeoutSeconds,omitempty"`
}

// RobinConfigMetrics defines metrics configuration for Robin.
type RobinConfigMetrics struct {
	CollectionIntervalSeconds *int              `json:"collectionIntervalSeconds,omitempty"`
	RedisInfoKeys             []string          `json:"redisInfoKeys,omitempty"`
	MetricsLabels             map[string]string `json:"metricsLabels,omitempty"`
}

// RedisAuth defines the authentication configuration for Redis.
type RedisAuth struct {
	SecretName string `json:"secret,omitempty"`
}

// Pdb defines the PodDisruptionBudget configuration for Redkey.
type Pdb struct {
	Enabled            bool               `json:"enabled,omitempty"`
	PdbSizeUnavailable intstr.IntOrString `json:"pdbSizeUnavailable,omitempty"`
	PdbSizeAvailable   intstr.IntOrString `json:"pdbSizeAvailable,omitempty"`
}

// RedkeyOverrideSpec provides the ability to override the generated manifest of several child resources.
type RedkeyOverrideSpec struct {
	// +kubebuilder:validation:Optional
	// Override configuration for the Redkey StatefulSet.
	StatefulSet *PartialStatefulSet `json:"statefulSet,omitempty"`

	// +kubebuilder:validation:Optional
	// Override configuration for the Redkey Service.
	Service *PartialService `json:"service,omitempty"`
}

// PartialStatefulSet is a reduced representation of apps/v1 StatefulSet.
// +kubebuilder:pruning:PreserveUnknownFields
type PartialStatefulSet struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	// +kubebuilder:pruning:PreserveUnknownFields
	Metadata metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:pruning:PreserveUnknownFields
	Spec *PartialStatefulSetSpec `json:"spec,omitempty"`
}

// PartialStatefulSetSpec is a partial representation of StatefulSetSpec.
// +kubebuilder:pruning:PreserveUnknownFields
type PartialStatefulSetSpec struct {
	// +kubebuilder:validation:Optional
	MinReadySeconds *int32 `json:"minReadySeconds,omitempty"`
	// +kubebuilder:validation:Optional
	Replicas *int32 `json:"replicas,omitempty"`
	// +kubebuilder:validation:Optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
	// +kubebuilder:validation:Optional
	ServiceName string `json:"serviceName,omitempty"`
	// +kubebuilder:validation:Optional
	Template *PartialPodTemplateSpec `json:"template,omitempty"`
	// +kubebuilder:validation:Optional
	UpdateStrategy *appsv1.StatefulSetUpdateStrategy `json:"updateStrategy,omitempty"`
	// +kubebuilder:validation:Optional
	RevisionHistoryLimit *int32 `json:"revisionHistoryLimit,omitempty"`
	// +kubebuilder:validation:Optional
	PodManagementPolicy string `json:"podManagementPolicy,omitempty"`
	// +kubebuilder:validation:Optional
	VolumeClaimTemplates []v1.PersistentVolumeClaim `json:"volumeClaimTemplates,omitempty"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	PersistentVolumeClaimRetentionPolicy *appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy `json:"persistentVolumeClaimRetentionPolicy,omitempty"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Ordinals *appsv1.StatefulSetOrdinals `json:"ordinals,omitempty"`
}

// PartialPodTemplateSpec is a partial representation of PodTemplateSpec.
// +kubebuilder:pruning:PreserveUnknownFields
type PartialPodTemplateSpec struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Metadata metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Spec PartialPodSpec `json:"spec,omitempty"`
}

// PartialPodSpec is a partial representation of PodSpec where containers are optional.
// +kubebuilder:pruning:PreserveUnknownFields
type PartialPodSpec struct {
	// +kubebuilder:validation:Optional
	Containers []v1.Container `json:"containers,omitempty"`
	// +kubebuilder:validation:Optional
	InitContainers []v1.Container `json:"initContainers,omitempty"`
	// +kubebuilder:validation:Optional
	EphemeralContainers []v1.EphemeralContainer `json:"ephemeralContainers,omitempty"`
	// +kubebuilder:validation:Optional
	RestartPolicy v1.RestartPolicy `json:"restartPolicy,omitempty"`
	// +kubebuilder:validation:Optional
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds,omitempty"`
	// +kubebuilder:validation:Optional
	DNSPolicy v1.DNSPolicy `json:"dnsPolicy,omitempty"`
	// +kubebuilder:validation:Optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// +kubebuilder:validation:Optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
	// +kubebuilder:validation:Optional
	SecurityContext *v1.PodSecurityContext `json:"securityContext,omitempty"`
	// +kubebuilder:validation:Optional
	ImagePullSecrets []v1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
	// +kubebuilder:validation:Optional
	Affinity *v1.Affinity `json:"affinity,omitempty"`
	// +kubebuilder:validation:Optional
	Tolerations []v1.Toleration `json:"tolerations,omitempty"`
	// +kubebuilder:validation:Optional
	PriorityClassName string `json:"priorityClassName,omitempty"`
	// +kubebuilder:validation:Optional
	TopologySpreadConstraints []v1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	// +kubebuilder:validation:Optional
	Volumes []v1.Volume `json:"volumes,omitempty"`
}

// PartialService is a reduced representation of core/v1 Service.
// +kubebuilder:pruning:PreserveUnknownFields
type PartialService struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	// +kubebuilder:pruning:PreserveUnknownFields
	Metadata metav1.ObjectMeta `json:"metadata,omitempty"`
	// +kubebuilder:pruning:PreserveUnknownFields
	Spec *PartialServiceSpec `json:"spec,omitempty"`
}

// PartialServiceSpec is a partial representation of ServiceSpec.
// +kubebuilder:pruning:PreserveUnknownFields
type PartialServiceSpec struct {
	// +kubebuilder:validation:Optional
	Ports []v1.ServicePort `json:"ports,omitempty"`
	// +kubebuilder:validation:Optional
	Selector map[string]string `json:"selector,omitempty"`
	// +kubebuilder:validation:Optional
	ClusterIP string `json:"clusterIP,omitempty"`
	// +kubebuilder:validation:Optional
	Type v1.ServiceType `json:"type,omitempty"`
	// +kubebuilder:validation:Optional
	PublishNotReadyAddresses bool `json:"publishNotReadyAddresses,omitempty"`
}

func init() {
	SchemeBuilder.Register(&Redkey{}, &RedkeyList{})
}
