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

// Package ownership implements node ownership checks that allow multiple
// cloud controller managers to coexist in a single cluster (KEP-5778).
package ownership

import (
	"errors"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/cloud-provider/features"
	"k8s.io/klog/v2"
)

const (
	// DefaultNodeLifecycleWaitTimeout is the default time the NodeLifecycle controller
	// waits for a node without ProviderID to be initialized by its own cloud controller
	// manager in hybrid mode.
	DefaultNodeLifecycleWaitTimeout = 30 * time.Second

	// AllPlatforms is the platform identifier that matches every ProviderID.
	// A cloud provider that reports it manages nodes of any platform in hybrid mode,
	// and must return cloudprovider.NotOwned for nodes it does not manage.
	AllPlatforms = "*"

	providerIDSeparator = "://"
)

// Reasons reported by the node ownership skipped metric.
const (
	// ReasonSchemeMismatch is used when the node ProviderID platform is not supported by the cloud provider.
	ReasonSchemeMismatch = "scheme_mismatch"
	// ReasonNotOwned is used when the cloud provider returns the cloudprovider.NotOwned error.
	ReasonNotOwned = "not_owned"
	// ReasonNoProviderID is used when a node without ProviderID cannot be found in the cloud provider.
	ReasonNoProviderID = "no_provider_id"
	// ReasonTimeoutWait is used when a node without ProviderID is waiting for its own cloud controller manager.
	ReasonTimeoutWait = "timeout_wait"
)

// Controllers reported by the node ownership skipped metric.
const (
	ControllerNode          = "node"
	ControllerNodeLifecycle = "nodelifecycle"
)

// Platforms returns the list of platform identifiers managed by the cloud provider.
// It returns nil if the CloudProviderNodeOwnership feature gate is disabled.
func Platforms(cloud cloudprovider.Interface) []string {
	if cloud == nil || !utilfeature.DefaultFeatureGate.Enabled(features.CloudProviderNodeOwnership) {
		return nil
	}

	return cloud.ProviderPlatforms()
}

// PlatformFromProviderID returns the platform identifier (the scheme) of the ProviderID.
// It returns false if the ProviderID is not in the "<platform>://<provider specific part>" format.
func PlatformFromProviderID(providerID string) (string, bool) {
	platform, _, found := strings.Cut(providerID, providerIDSeparator)
	if !found || platform == "" {
		return "", false
	}

	return platform, true
}

// IsNotOwned returns true if the error reports that the node belongs to another
// installation of the same platform.
func IsNotOwned(err error) bool {
	return errors.Is(err, cloudprovider.NotOwned)
}

// Checker determines whether a node is managed by this cloud controller manager.
// A nil Checker, or a Checker with an empty platform list, manages all nodes.
type Checker struct {
	platforms sets.Set[string]
}

// NewChecker returns a Checker for the given platform identifiers.
func NewChecker(platforms []string) *Checker {
	c := &Checker{platforms: sets.New[string]()}
	for _, p := range platforms {
		if p = strings.TrimSpace(p); p != "" {
			c.platforms.Insert(p)
		}
	}

	return c
}

// HybridMode returns true if the node ownership checks are active.
func (c *Checker) HybridMode() bool {
	return c != nil && c.platforms.Len() > 0
}

// Platforms returns the sorted list of supported platform identifiers.
func (c *Checker) Platforms() []string {
	if c == nil {
		return nil
	}

	return sets.List(c.platforms)
}

// OwnsProviderID returns true if the ProviderID belongs to one of the supported platforms.
// Outside of hybrid mode, or with the AllPlatforms identifier, every ProviderID is owned.
func (c *Checker) OwnsProviderID(providerID string) bool {
	if !c.HybridMode() || c.platforms.Has(AllPlatforms) {
		return true
	}

	platform, ok := PlatformFromProviderID(providerID)

	return ok && c.platforms.Has(platform)
}

// OwnsNode returns true if the node is managed by this cloud controller manager.
// A node without ProviderID is reported as owned, callers must handle such nodes separately.
func (c *Checker) OwnsNode(node *v1.Node) bool {
	if node.Spec.ProviderID == "" {
		return true
	}

	return c.OwnsProviderID(node.Spec.ProviderID)
}

// SkipNode returns true if the node has a ProviderID of a platform that is not
// managed by this cloud controller manager, and records the skip for the given controller.
// A node without ProviderID is not skipped, callers must handle such nodes separately.
func (c *Checker) SkipNode(controller string, node *v1.Node) bool {
	if c.OwnsNode(node) {
		return false
	}

	klog.V(4).Infof("Skipping node %s with ProviderID %q, platform is not managed by this cloud provider", node.Name, node.Spec.ProviderID)
	RecordSkipped(controller, ReasonSchemeMismatch)
	return true
}

// RecordNotOwned records a node that belongs to another installation of the same platform,
// as reported by the cloudprovider.NotOwned error, for the given controller.
func RecordNotOwned(controller string, node *v1.Node) {
	klog.V(2).Infof("Skipping node %s with ProviderID %q, node is not owned by this cloud provider", node.Name, node.Spec.ProviderID)
	RecordSkipped(controller, ReasonNotOwned)
}

// WithPlatforms returns a cloud provider that reports the given platform identifiers
// instead of the ones returned by the cloud provider implementation.
// The original cloud provider is returned when the list is empty.
func WithPlatforms(cloud cloudprovider.Interface, platforms []string) cloudprovider.Interface {
	if len(platforms) == 0 {
		return cloud
	}

	return &platformsOverride{Interface: cloud, platforms: platforms}
}

type platformsOverride struct {
	cloudprovider.Interface
	platforms []string
}

func (p *platformsOverride) ProviderPlatforms() []string {
	return p.platforms
}
