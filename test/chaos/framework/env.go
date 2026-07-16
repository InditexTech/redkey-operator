// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package framework

import (
	"os"
	"strconv"
)

// Environment variable names consumed by the chaos suite.
const (
	EnvOperatorImage   = "IMAGE_OPERATOR"
	EnvRobinImage      = "IMAGE_ROBIN"
	EnvRedisImage      = "REDIS_IMAGE"
	EnvK6Image         = "K6_IMG"
	EnvChaosIterations = "CHAOS_ITERATIONS"
	EnvChaosSeed       = "CHAOS_SEED"
	EnvChaosKeepNs     = "CHAOS_KEEP_NAMESPACE_ON_FAILED"
	EnvK6VUs           = "CHAOS_K6_VUS"
)

// Default images and tunables.
const (
	defaultOperatorImage = "localhost:5005/redkey-operator:dev"
	defaultRobinImage    = "localhost:5005/redkey-robin:dev"
	defaultRedisImage    = "redis:8-bookworm"
	defaultK6Image       = "localhost:5005/redkey-k6:dev"
	defaultK6VUs         = 10
	defaultIterations    = 3
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// GetOperatorImage returns the operator image to deploy in the chaos namespace.
func GetOperatorImage() string { return getEnv(EnvOperatorImage, defaultOperatorImage) }

// GetRobinImage returns the Robin image used by the RedkeyCluster spec.
func GetRobinImage() string { return getEnv(EnvRobinImage, defaultRobinImage) }

// GetRedisImage returns the Redis image used by the RedkeyCluster spec.
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
