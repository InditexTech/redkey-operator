// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"os"
	"strconv"
	"time"
)

// Environment variable names consumed by the chaos suite.
const (
	// EnvOperatorImage overrides the operator image deployed into each chaos namespace.
	EnvOperatorImage = "IMAGE_OPERATOR"
	// EnvRobinImage overrides the Robin image used by the Redkey spec.
	EnvRobinImage = "IMAGE_ROBIN"
	// EnvRedisImage overrides the Redis image used for the cluster nodes.
	EnvRedisImage = "REDIS_IMAGE"
	// EnvK6Image overrides the k6 load-generator image.
	EnvK6Image = "K6_IMG"
	// EnvChaosIterations sets the number of fault-injection iterations per scenario.
	EnvChaosIterations = "CHAOS_ITERATIONS"
	// EnvChaosSeed sets a fixed RNG seed for reproducible chaos runs.
	EnvChaosSeed = "CHAOS_SEED"
	// EnvChaosKeepNs, when truthy, preserves the namespace of a failed spec for post-mortem inspection.
	EnvChaosKeepNs = "CHAOS_KEEP_NAMESPACE_ON_FAILED"
	// EnvK6VUs sets the number of k6 virtual users generating load.
	EnvK6VUs = "CHAOS_K6_VUS"
	// EnvDisruptionInterval controls the base cadence (seconds) at which the background disruptor
	// deletes pods while active. Because the disruptor stays active until the cluster converges, this
	// interval must be larger than the single-pod recovery time; otherwise the cluster never reaches
	// a clean window and readiness never converges. Ephemeral pods recover slowly (fresh node ID +
	// reshard), so the default is 30s.
	EnvDisruptionInterval = "CHAOS_DISRUPTION_INTERVAL"
	// EnvRebalanceDisruptionInterval controls the (slower) cadence used once the cluster is
	// structurally healthy and only a slot rebalance is pending. A rebalance is a multi-slot
	// migration that any pod deletion aborts, so the disruptor slows to this cadence to give Robin a
	// window to finish balancing (and let the spec converge) while still exercising disruption.
	EnvRebalanceDisruptionInterval = "CHAOS_DISRUPTION_REBALANCE_INTERVAL"
)

// Default images and tunables.
const (
	// defaultOperatorImage is the operator image used when IMAGE_OPERATOR is unset.
	defaultOperatorImage = "localhost:5005/redkey-operator:dev"
	// defaultRobinImage is the Robin image used when IMAGE_ROBIN is unset.
	defaultRobinImage = "localhost:5005/redkey-robin:dev"
	// defaultRedisImage is the Redis image used when REDIS_IMAGE is unset.
	defaultRedisImage = "redis:8-bookworm"
	// defaultK6Image is the k6 load-generator image used when K6_IMG is unset.
	defaultK6Image = "localhost:5005/redkey-k6:dev"
	// defaultK6VUs is the number of k6 virtual users used when CHAOS_K6_VUS is unset.
	defaultK6VUs = 10
	// defaultIterations is the number of chaos iterations used when CHAOS_ITERATIONS is unset.
	defaultIterations = 3
	// defaultDisruptionInterval is the base cadence (seconds) of the background disruptor. It must
	// exceed the single-pod recovery time so the cluster can make progress between deletions, and it
	// spaces the per-operation burst across the operation's slot-movement phase.
	defaultDisruptionInterval = 60
	// defaultRebalanceDisruptionInterval is the slower cadence (seconds) used while only a slot
	// rebalance is pending, giving the multi-slot migration a window to complete despite disruption.
	defaultRebalanceDisruptionInterval = 120
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// GetOperatorImage returns the operator image to deploy in the chaos namespace.
func GetOperatorImage() string { return getEnv(EnvOperatorImage, defaultOperatorImage) }

// GetRobinImage returns the Robin image used by the Redkey spec.
func GetRobinImage() string { return getEnv(EnvRobinImage, defaultRobinImage) }

// GetRedisImage returns the Redis image used by the Redkey spec.
func GetRedisImage() string { return getEnv(EnvRedisImage, defaultRedisImage) }

// GetK6Image returns the k6 load-generator image.
func GetK6Image() string { return getEnv(EnvK6Image, defaultK6Image) }

// GetK6VUs returns the number of virtual users for the k6 load deployment.
func GetK6VUs() int {
	if v := os.Getenv(EnvK6VUs); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultK6VUs
}

// GetChaosIterations returns the number of chaos iterations per scenario.
func GetChaosIterations() int {
	if v := os.Getenv(EnvChaosIterations); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultIterations
}

// KeepNamespaceOnFailure reports whether namespaces should be preserved after a failed spec.
func KeepNamespaceOnFailure() bool {
	v := os.Getenv(EnvChaosKeepNs)
	keep, _ := strconv.ParseBool(v)
	return keep
}

// GetDisruptionInterval returns the base cadence at which the background disruptor deletes pods
// while active.
func GetDisruptionInterval() time.Duration {
	if v := os.Getenv(EnvDisruptionInterval); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(defaultDisruptionInterval) * time.Second
}

// GetRebalanceDisruptionInterval returns the slower cadence used while the cluster is structurally
// healthy and only a slot rebalance is pending, giving the migration a window to complete.
func GetRebalanceDisruptionInterval() time.Duration {
	if v := os.Getenv(EnvRebalanceDisruptionInterval); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(defaultRebalanceDisruptionInterval) * time.Second
}
