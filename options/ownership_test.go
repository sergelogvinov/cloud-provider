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
	"testing"
	"time"

	"github.com/spf13/pflag"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	cpconfig "k8s.io/cloud-provider/config"
	nodelifecycleconfig "k8s.io/cloud-provider/controllers/nodelifecycle/config"
	"k8s.io/cloud-provider/features"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
)

func TestCloudProviderPlatformsFlag(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		enableFeature bool
		expected      []string
		expectedErrs  int
	}{
		{
			name:          "not set",
			args:          []string{},
			enableFeature: false,
			expected:      nil,
		},
		{
			name:          "set with feature gate",
			args:          []string{"--cloud-provider-platforms=proxmox,baremetal"},
			enableFeature: true,
			expected:      []string{"proxmox", "baremetal"},
		},
		{
			name:          "set without feature gate",
			args:          []string{"--cloud-provider-platforms=proxmox"},
			enableFeature: false,
			expected:      []string{"proxmox"},
			expectedErrs:  1,
		},
		{
			name:          "invalid platform",
			args:          []string{"--cloud-provider-platforms=proxmox://,"},
			enableFeature: true,
			expected:      []string{"proxmox://", ""},
			expectedErrs:  2,
		},
		{
			name:          "all platforms",
			args:          []string{"--cloud-provider-platforms=*"},
			enableFeature: true,
			expected:      []string{"*"},
		},
		{
			name:          "whitespace platform",
			args:          []string{"--cloud-provider-platforms= ,proxmox, baremetal,bare metal"},
			enableFeature: true,
			expected:      []string{" ", "proxmox", " baremetal", "bare metal"},
			expectedErrs:  3,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CloudProviderNodeOwnership, test.enableFeature)

			o := &CloudProviderOptions{CloudProviderConfiguration: &cpconfig.CloudProviderConfiguration{}}
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			o.AddFlags(fs)
			if err := fs.Parse(test.args); err != nil {
				t.Fatal(err)
			}

			cfg := &cpconfig.CloudProviderConfiguration{}
			if err := o.ApplyTo(cfg); err != nil {
				t.Fatal(err)
			}
			if len(cfg.Platforms) != len(test.expected) {
				t.Fatalf("unexpected platforms %q, expected %q", cfg.Platforms, test.expected)
			}
			for i := range test.expected {
				if cfg.Platforms[i] != test.expected[i] {
					t.Errorf("unexpected platforms %q, expected %q", cfg.Platforms, test.expected)
				}
			}
			if errs := o.Validate(); len(errs) != test.expectedErrs {
				t.Errorf("unexpected validation errors %v, expected %d errors", errs, test.expectedErrs)
			}
		})
	}
}

func TestNodeLifecycleWaitTimeoutFlag(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		expected     *metav1.Duration
		expectedErrs int
	}{
		{
			name:     "not set",
			args:     []string{},
			expected: nil,
		},
		{
			name:     "zero",
			args:     []string{"--node-lifecycle-wait-timeout=0s"},
			expected: &metav1.Duration{Duration: 0},
		},
		{
			name:     "one minute",
			args:     []string{"--node-lifecycle-wait-timeout=1m"},
			expected: &metav1.Duration{Duration: time.Minute},
		},
		{
			name:         "negative",
			args:         []string{"--node-lifecycle-wait-timeout=-1s"},
			expected:     &metav1.Duration{Duration: -time.Second},
			expectedErrs: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			o := &NodeLifecycleControllerOptions{
				NodeLifecycleControllerConfiguration: &nodelifecycleconfig.NodeLifecycleControllerConfiguration{
					ConcurrentNodeLifecycleSyncs: 1,
				},
			}
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			o.AddFlags(fs)
			if err := fs.Parse(test.args); err != nil {
				t.Fatal(err)
			}

			cfg := &nodelifecycleconfig.NodeLifecycleControllerConfiguration{}
			if err := o.ApplyTo(cfg); err != nil {
				t.Fatal(err)
			}
			if (cfg.NodeLifecycleWaitTimeout == nil) != (test.expected == nil) ||
				(test.expected != nil && *cfg.NodeLifecycleWaitTimeout != *test.expected) {
				t.Errorf("unexpected wait timeout %v, expected %v", cfg.NodeLifecycleWaitTimeout, test.expected)
			}
			if errs := o.Validate(); len(errs) != test.expectedErrs {
				t.Errorf("unexpected validation errors %v, expected %d errors", errs, test.expectedErrs)
			}
		})
	}
}
