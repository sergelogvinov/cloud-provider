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
	"fmt"
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	cloudprovider "k8s.io/cloud-provider"
	fakecloud "k8s.io/cloud-provider/fake"
	"k8s.io/cloud-provider/features"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/component-base/metrics/testutil"

	"github.com/google/go-cmp/cmp"
)

func TestPlatformFromProviderID(t *testing.T) {
	tests := []struct {
		providerID string
		platform   string
		ok         bool
	}{
		{providerID: "aws:///us-east-1a/i-123", platform: "aws", ok: true},
		{providerID: "proxmox://region-1/100", platform: "proxmox", ok: true},
		{providerID: "baremetal://1234", platform: "baremetal", ok: true},
		{providerID: "", platform: "", ok: false},
		{providerID: "i-123", platform: "", ok: false},
		{providerID: "://i-123", platform: "", ok: false},
		{providerID: "aws:/i-123", platform: "", ok: false},
	}

	for _, test := range tests {
		t.Run(test.providerID, func(t *testing.T) {
			platform, ok := PlatformFromProviderID(test.providerID)
			if platform != test.platform || ok != test.ok {
				t.Errorf("PlatformFromProviderID(%q) = (%q, %v), expected (%q, %v)", test.providerID, platform, ok, test.platform, test.ok)
			}
		})
	}
}

func TestChecker(t *testing.T) {
	tests := []struct {
		name       string
		checker    *Checker
		hybridMode bool
		owned      map[string]bool
	}{
		{
			name:       "nil checker manages all nodes",
			checker:    nil,
			hybridMode: false,
			owned: map[string]bool{
				"":               true,
				"aws://i-123":    true,
				"baremetal://1":  true,
				"not-a-provider": true,
			},
		},
		{
			name:       "empty platforms manage all nodes",
			checker:    NewChecker([]string{"", " "}),
			hybridMode: false,
			owned: map[string]bool{
				"":              true,
				"aws://i-123":   true,
				"baremetal://1": true,
			},
		},
		{
			name:       "hybrid mode",
			checker:    NewChecker([]string{"hcloud", " hrobot "}),
			hybridMode: true,
			owned: map[string]bool{
				"":                  true,
				"hcloud://123":      true,
				"hrobot://456":      true,
				"aws://i-123":       false,
				"hcloud-other://12": false,
				"hcloud":            false,
				"not-a-provider":    false,
			},
		},
		{
			name:       "all platforms",
			checker:    NewChecker([]string{"*"}),
			hybridMode: true,
			owned: map[string]bool{
				"":               true,
				"hcloud://123":   true,
				"aws://i-123":    true,
				"not-a-provider": true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.checker.HybridMode(); got != test.hybridMode {
				t.Errorf("HybridMode() = %v, expected %v", got, test.hybridMode)
			}

			for providerID, expected := range test.owned {
				node := &v1.Node{Spec: v1.NodeSpec{ProviderID: providerID}}
				if got := test.checker.OwnsNode(node); got != expected {
					t.Errorf("OwnsNode(%q) = %v, expected %v", providerID, got, expected)
				}
			}
		})
	}
}

func TestPlatforms(t *testing.T) {
	cloud := &fakecloud.Cloud{Platforms: []string{"test"}}

	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CloudProviderNodeOwnership, false)
	if got := Platforms(cloud); got != nil {
		t.Errorf("Platforms() with disabled feature gate = %v, expected nil", got)
	}

	featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.CloudProviderNodeOwnership, true)
	if got := Platforms(cloud); !cmp.Equal(got, []string{"test"}) {
		t.Errorf("Platforms() = %v, expected [test]", got)
	}
	if got := Platforms(WithPlatforms(cloud, []string{"other", "test"})); !cmp.Equal(got, []string{"other", "test"}) {
		t.Errorf("Platforms() with override = %v, expected [other test]", got)
	}
	if got := WithPlatforms(cloud, nil); got != cloudprovider.Interface(cloud) {
		t.Errorf("WithPlatforms() with empty list must return the original cloud provider")
	}
}

func TestIsNotOwned(t *testing.T) {
	if !IsNotOwned(cloudprovider.NotOwned) {
		t.Errorf("IsNotOwned(NotOwned) = false, expected true")
	}
	if !IsNotOwned(fmt.Errorf("wrapped: %w", cloudprovider.NotOwned)) {
		t.Errorf("IsNotOwned(wrapped NotOwned) = false, expected true")
	}
	if IsNotOwned(cloudprovider.InstanceNotFound) {
		t.Errorf("IsNotOwned(InstanceNotFound) = true, expected false")
	}
}

func TestRecordSkipped(t *testing.T) {
	RegisterMetrics()
	nodeOwnershipSkipped.Reset()

	RecordSkipped(ControllerNode, ReasonSchemeMismatch)
	RecordSkipped(ControllerNode, ReasonSchemeMismatch)
	RecordSkipped(ControllerNodeLifecycle, ReasonNotOwned)

	expected := `
# HELP cloudprovider_node_ownership_skipped_total [ALPHA] Number of times a node was skipped because it is not owned by this cloud provider, labeled by controller and reason.
# TYPE cloudprovider_node_ownership_skipped_total counter
cloudprovider_node_ownership_skipped_total{controller="node",reason="scheme_mismatch"} 2
cloudprovider_node_ownership_skipped_total{controller="nodelifecycle",reason="not_owned"} 1
`
	if err := testutil.CollectAndCompare(nodeOwnershipSkipped, strings.NewReader(expected), "cloudprovider_node_ownership_skipped_total"); err != nil {
		t.Error(err)
	}
}

func TestSkipNode(t *testing.T) {
	RegisterMetrics()
	nodeOwnershipSkipped.Reset()

	checker := NewChecker([]string{"test"})
	for providerID, expected := range map[string]bool{
		"":                false,
		"test://instance": false,
		"other://node":    true,
	} {
		node := &v1.Node{Spec: v1.NodeSpec{ProviderID: providerID}}
		if got := checker.SkipNode(ControllerNode, node); got != expected {
			t.Errorf("SkipNode(%q) = %v, expected %v", providerID, got, expected)
		}
	}
	RecordNotOwned(ControllerNodeLifecycle, &v1.Node{Spec: v1.NodeSpec{ProviderID: "test://instance"}})

	expected := `
# HELP cloudprovider_node_ownership_skipped_total [ALPHA] Number of times a node was skipped because it is not owned by this cloud provider, labeled by controller and reason.
# TYPE cloudprovider_node_ownership_skipped_total counter
cloudprovider_node_ownership_skipped_total{controller="node",reason="scheme_mismatch"} 1
cloudprovider_node_ownership_skipped_total{controller="nodelifecycle",reason="not_owned"} 1
`
	if err := testutil.CollectAndCompare(nodeOwnershipSkipped, strings.NewReader(expected), "cloudprovider_node_ownership_skipped_total"); err != nil {
		t.Error(err)
	}
}
