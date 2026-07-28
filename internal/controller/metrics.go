// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/types"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	redisv1 "github.com/inditextech/redkeyoperator/api/v1beta1"
)

// Custom operator metrics describing the control-plane state of the managed Redkey fleet.
// They are exposed by the operator itself on its /metrics endpoint (the same one scraped for
// the controller-runtime metrics), so they work in any environment where the operator's
// ServiceMonitor is discovered — no changes to a central kube-state-metrics are required.
//
// Scope is deliberately narrow: only the lifecycle state the operator owns (Redkey phase and
// the active RedkeyConfig phase). Data-plane health (cluster healthy, slots, balance, ...) is
// owned and published by Robin (redkey_cluster_*) and is intentionally NOT mirrored here.
//
// Both metrics follow the "state set" convention: for every managed object a series is emitted
// for each possible value, carrying 1 on the active value and 0 on the rest. This makes fleet
// queries simple, e.g. count(redkey_phase{phase="Ready"}) for the fleet size, or
// sum by (phase) (redkey_phase) for the phase distribution.
var (
	redkeyPhase = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "redkey_phase",
		Help: "Phase of each managed Redkey (one series per phase; 1 on the active phase).",
	}, []string{"namespace", "name", "phase"})

	redkeyConfigPhase = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "redkey_config_phase",
		Help: "Config lifecycle phase of the active RedkeyConfig for each managed Redkey (one series per phase; 1 on the active phase).",
	}, []string{"namespace", "name", "config_phase"})

	// redkeyOperationDuration measures how long each control-plane operation (scaling, upgrade,
	// rebalance, initialization, ...) takes, from the moment the cluster enters a non-terminal
	// status until it settles back to Ready (or Error).
	redkeyOperationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "redkey_operation_duration_seconds",
		Help:    "Duration of Redkey operations from start until the cluster settles back to Ready or Error.",
		Buckets: []float64{5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600},
	}, []string{"operation"})

	// redkeyOperationTotal counts completed operations by type and result.
	redkeyOperationTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "redkey_operation_total",
		Help: "Total number of completed Redkey operations by type and result (success, error or superseded).",
	}, []string{"operation", "result"})

	// redkeyOperationInProgress marks the operation each Redkey is currently running: one series
	// per Redkey with value 1 while a non-terminal operation is in flight, removed as soon as the
	// cluster settles back to Ready/Error. It complements the completion-only counters/histogram.
	redkeyOperationInProgress = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "redkey_operation_in_progress",
		Help: "The control-plane operation a managed Redkey is currently running (1 while in progress).",
	}, []string{"namespace", "name", "operation"})
)

// redkeyPhaseValues and redkeyConfigPhaseValues enumerate every possible value so the state-set
// series are always fully materialized (active = 1, others = 0).
var (
	redkeyPhaseValues = []string{
		redisv1.PhaseReady,
		redisv1.PhaseConfiguring,
		redisv1.PhaseError,
	}
	redkeyConfigPhaseValues = []string{
		redisv1.ConfigPhasePending,
		redisv1.ConfigPhaseInProgress,
		redisv1.ConfigPhaseSuperseded,
		redisv1.ConfigPhaseApplied,
	}
)

func init() {
	crmetrics.Registry.MustRegister(redkeyPhase, redkeyConfigPhase, redkeyOperationDuration, redkeyOperationTotal, redkeyOperationInProgress)
}

// recordRedkeyMetrics refreshes the fleet-state metrics for a single Redkey from its freshly
// aggregated status. activeConfig may be nil (e.g. a cluster scaled to zero has no config), in
// which case the config-phase series for the Redkey are removed.
func recordRedkeyMetrics(cluster *redisv1.Redkey, activeConfig *redisv1.RedkeyConfig) {
	ns, name := cluster.Namespace, cluster.Name

	operationTracker.observe(types.NamespacedName{Namespace: ns, Name: name}, cluster.Status.Status, cluster.Status.Phase)

	// Live "operation in progress" gauge: one series per Redkey while a non-terminal operation is
	// running; removed as soon as it settles.
	inProgressLabels := prometheus.Labels{"namespace": ns, "name": name}
	redkeyOperationInProgress.DeletePartialMatch(inProgressLabels)
	if status := cluster.Status.Status; status != "" && status != redisv1.ClusterStatusReady && status != redisv1.ClusterPhaseError {
		redkeyOperationInProgress.WithLabelValues(ns, name, status).Set(1)
	}

	for _, phase := range redkeyPhaseValues {
		value := 0.0
		if phase == cluster.Status.Phase {
			value = 1.0
		}
		redkeyPhase.WithLabelValues(ns, name, phase).Set(value)
	}

	if activeConfig == nil {
		redkeyConfigPhase.DeletePartialMatch(prometheus.Labels{"namespace": ns, "name": name})
		return
	}
	for _, configPhase := range redkeyConfigPhaseValues {
		value := 0.0
		if configPhase == activeConfig.Status.ConfigPhase {
			value = 1.0
		}
		redkeyConfigPhase.WithLabelValues(ns, name, configPhase).Set(value)
	}
}

// deleteRedkeyMetrics removes every series belonging to a Redkey that no longer exists.
func deleteRedkeyMetrics(key types.NamespacedName) {
	crLabels := prometheus.Labels{"namespace": key.Namespace, "name": key.Name}
	redkeyPhase.DeletePartialMatch(crLabels)
	redkeyConfigPhase.DeletePartialMatch(crLabels)
	redkeyOperationInProgress.DeletePartialMatch(crLabels)
	operationTracker.forget(key)
}

// operationTracker measures operation durations by watching the transitions of each Redkey's
// operational status. Any non-terminal status (ScalingUp/Down, Upgrading, Rebalancing,
// Initializing, ...) counts as an in-flight operation; its duration and result are recorded when
// the cluster settles back to Ready (success), goes to Error (error), or switches to a different
// operation without settling (superseded).
//
// State is in-memory: an operator restart forgets in-flight operations, so an operation already
// running is timed from the first post-restart observation.
var operationTracker = &opTracker{active: make(map[types.NamespacedName]activeOperation)}

type activeOperation struct {
	operation string
	startedAt time.Time
}

type opTracker struct {
	mu     sync.Mutex
	active map[types.NamespacedName]activeOperation
}

func (t *opTracker) observe(key types.NamespacedName, status, phase string) {
	inOperation := status != "" &&
		status != redisv1.ClusterStatusReady &&
		status != redisv1.ClusterPhaseError

	t.mu.Lock()
	defer t.mu.Unlock()

	current, tracking := t.active[key]
	switch {
	case inOperation && !tracking:
		t.active[key] = activeOperation{operation: status, startedAt: time.Now()}
	case inOperation && tracking && current.operation != status:
		redkeyOperationDuration.WithLabelValues(current.operation).Observe(time.Since(current.startedAt).Seconds())
		redkeyOperationTotal.WithLabelValues(current.operation, "superseded").Inc()
		t.active[key] = activeOperation{operation: status, startedAt: time.Now()}
	case !inOperation && tracking:
		result := "success"
		if phase == redisv1.PhaseError || status == redisv1.ClusterPhaseError {
			result = "error"
		}
		redkeyOperationDuration.WithLabelValues(current.operation).Observe(time.Since(current.startedAt).Seconds())
		redkeyOperationTotal.WithLabelValues(current.operation, result).Inc()
		delete(t.active, key)
	}
}

func (t *opTracker) forget(key types.NamespacedName) {
	t.mu.Lock()
	delete(t.active, key)
	t.mu.Unlock()
}
