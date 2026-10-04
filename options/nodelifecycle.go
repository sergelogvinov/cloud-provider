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

package options

import (
	"fmt"
	"time"

	"github.com/spf13/pflag"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	nodelifecycleconfig "k8s.io/cloud-provider/controllers/nodelifecycle/config"
	"k8s.io/cloud-provider/names"
	"k8s.io/cloud-provider/node/ownership"
)

// NodeLifecycleControllerOptions holds the CloudNodeLifecycleController options.
type NodeLifecycleControllerOptions struct {
	*nodelifecycleconfig.NodeLifecycleControllerConfiguration
}

// AddFlags adds flags related to CloudNodeLifecycleController for controller manager to the specified FlagSet.
func (o *NodeLifecycleControllerOptions) AddFlags(fs *pflag.FlagSet) {
	if o == nil {
		return
	}

	fs.Int32Var(&o.ConcurrentNodeLifecycleSyncs, "concurrent-node-lifecycle-syncs", o.ConcurrentNodeLifecycleSyncs,
		fmt.Sprintf("The number of workers for syncing NodeStatus in %s.", names.CloudNodeLifecycleController))
	fs.DurationVar(&o.NodeMonitorPeriod.Duration, "node-monitor-period", o.NodeMonitorPeriod.Duration,
		"The period for syncing NodeStatus in CloudNodeLifecycleController.")
	fs.Var(&optionalDurationValue{target: &o.NodeLifecycleWaitTimeout}, "node-lifecycle-wait-timeout",
		fmt.Sprintf("The time CloudNodeLifecycleController waits for a node without ProviderID to be initialized by its own cloud controller manager, "+
			"counted from the node creation. If not set, defaults to %v when node ownership "+
			"(CloudProviderNodeOwnership feature gate with a non-empty platform list) is enabled and 0 otherwise.", ownership.DefaultNodeLifecycleWaitTimeout))
}

// ApplyTo fills up CloudNodeLifecycleController config with options.
func (o *NodeLifecycleControllerOptions) ApplyTo(cfg *nodelifecycleconfig.NodeLifecycleControllerConfiguration) error {
	if o == nil {
		return nil
	}

	cfg.ConcurrentNodeLifecycleSyncs = o.ConcurrentNodeLifecycleSyncs
	cfg.NodeMonitorPeriod = o.NodeMonitorPeriod
	cfg.NodeLifecycleWaitTimeout = o.NodeLifecycleWaitTimeout

	return nil
}

// Validate checks validation of NodeLifecycleControllerOptions.
func (o *NodeLifecycleControllerOptions) Validate() []error {
	if o == nil {
		return nil
	}
	var errs []error

	if o.ConcurrentNodeLifecycleSyncs < 1 {
		errs = append(errs, fmt.Errorf("concurrent-node-lifecycle-syncs must be at least 1, but got %d", o.ConcurrentNodeLifecycleSyncs))
	}
	if o.NodeLifecycleWaitTimeout != nil && o.NodeLifecycleWaitTimeout.Duration < 0 {
		errs = append(errs, fmt.Errorf("node-lifecycle-wait-timeout must not be negative, but got %v", o.NodeLifecycleWaitTimeout.Duration))
	}

	return errs
}

// optionalDurationValue is a pflag.Value for a duration that is nil when the flag is not set.
type optionalDurationValue struct {
	target **metav1.Duration
}

func (v *optionalDurationValue) String() string {
	if v.target == nil || *v.target == nil {
		return ""
	}
	return (*v.target).Duration.String()
}

func (v *optionalDurationValue) Set(value string) error {
	d, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	*v.target = &metav1.Duration{Duration: d}
	return nil
}

func (v *optionalDurationValue) Type() string {
	return "duration"
}
