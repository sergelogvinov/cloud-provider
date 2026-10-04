/*
Copyright The Kubernetes Authors.

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

package ownership

import (
	"sync"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

const metricsSubsystem = "cloudprovider"

var (
	// nodeOwnershipSkipped counts nodes skipped by the cloud controller manager
	// because they are not managed by this cloud provider.
	nodeOwnershipSkipped = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      metricsSubsystem,
			Name:           "node_ownership_skipped_total",
			Help:           "Number of times a node was skipped because it is not owned by this cloud provider, labeled by controller and reason.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"controller", "reason"},
	)
)

var metricRegistration sync.Once

// RegisterMetrics registers the node ownership metrics.
func RegisterMetrics() {
	metricRegistration.Do(func() {
		legacyregistry.MustRegister(nodeOwnershipSkipped)
	})
}

// RecordSkipped increments the node ownership skipped metric.
func RecordSkipped(controller, reason string) {
	nodeOwnershipSkipped.WithLabelValues(controller, reason).Inc()
}
