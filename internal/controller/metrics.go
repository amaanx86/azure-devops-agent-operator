/*
Copyright 2026 Amaan Ul Haq Siddiqui.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	activeAgentsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "azp",
			Subsystem: "agentpool",
			Name:      "active_agents",
			Help:      "Current count of agent pods in Running or Pending phase",
		},
		[]string{"pool", "namespace"},
	)

	pendingJobsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "azp",
			Subsystem: "agentpool",
			Name:      "pending_jobs",
			Help:      "Current count of unfinished jobs in the ADO queue (waiting + executing)",
		},
		[]string{"pool", "namespace"},
	)

	availablePVCsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "azp",
			Subsystem: "agentpool",
			Name:      "available_pvc_slots",
			Help:      "Current count of PVC slots not assigned to any agent pod",
		},
		[]string{"pool", "namespace"},
	)

	reconcileErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "azp",
			Subsystem: "agentpool",
			Name:      "reconcile_errors_total",
			Help:      "Total reconcile errors by reason",
		},
		[]string{"pool", "namespace", "reason"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		activeAgentsGauge,
		pendingJobsGauge,
		availablePVCsGauge,
		reconcileErrorsTotal,
	)
}

func recordMetrics(pool, namespace string, active, pending, availPVCSlots int) {
	labels := prometheus.Labels{"pool": pool, "namespace": namespace}
	activeAgentsGauge.With(labels).Set(float64(active))
	pendingJobsGauge.With(labels).Set(float64(pending))
	availablePVCsGauge.With(labels).Set(float64(availPVCSlots))
}

func recordReconcileError(pool, namespace, reason string) {
	reconcileErrorsTotal.With(prometheus.Labels{
		"pool":      pool,
		"namespace": namespace,
		"reason":    reason,
	}).Inc()
}
