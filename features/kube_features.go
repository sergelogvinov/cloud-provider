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

// FIXME: move to kubernetes pkg/features/kube_features.go

package features

import (
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/version"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
)

func init() {
	utilruntime.Must(AddFeatureGates(utilfeature.DefaultMutableFeatureGate))
}

// Every feature gate should have an entry here following this template:
//
// // owner: @username
// MyFeature featuregate.Feature = "MyFeature"
//
// Feature gates should be listed in alphabetical, case-sensitive
// (upper before any lower case character) order. This reduces the risk
// of code conflicts because changes are more likely to be scattered
// across the file.
const (
	// owner: @sergelogvinov
	// kep:  http://kep.k8s.io/5778
	//
	// Allow multiple cloud controller managers in a single cluster by making
	// node ownership explicit, based on the ProviderID platform identifier.
	CloudProviderNodeOwnership featuregate.Feature = "CloudProviderNodeOwnership"
)

// AddFeatureGates adds the cloud-provider specific feature gates to the given feature gate.
func AddFeatureGates(featuregates featuregate.MutableVersionedFeatureGate) error {
	return featuregates.AddVersioned(versionedCloudProviderFeatureGates)
}

// versionedCloudProviderFeatureGates consists of versioned cloud-provider feature keys.
// To add a new feature, define a key for it above and add it here.
var versionedCloudProviderFeatureGates = map[featuregate.Feature]featuregate.VersionedSpecs{
	CloudProviderNodeOwnership: {
		{Version: version.MustParse("1.38"), Default: false, PreRelease: featuregate.Alpha},
	},
}
