/*
Copyright 2018 The Kubernetes Authors.

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
	"strings"
	"unicode"

	"github.com/spf13/pflag"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	cpconfig "k8s.io/cloud-provider/config"
	"k8s.io/cloud-provider/features"
)

// CloudProviderOptions holds the cloudprovider options.
type CloudProviderOptions struct {
	*cpconfig.CloudProviderConfiguration
}

// Validate checks validation of cloudprovider options.
func (s *CloudProviderOptions) Validate() []error {
	allErrors := []error{}

	if s == nil || s.CloudProviderConfiguration == nil {
		return allErrors
	}

	if len(s.Platforms) > 0 && !utilfeature.DefaultFeatureGate.Enabled(features.CloudProviderNodeOwnership) {
		allErrors = append(allErrors, fmt.Errorf("--cloud-provider-platforms requires the %s feature gate to be enabled", features.CloudProviderNodeOwnership))
	}
	for _, platform := range s.Platforms {
		if platform == "" || strings.ContainsAny(platform, ":/") || strings.IndexFunc(platform, unicode.IsSpace) >= 0 {
			allErrors = append(allErrors, fmt.Errorf("--cloud-provider-platforms contains invalid platform identifier %q, it must be a non-empty ProviderID scheme without \"://\" or whitespace", platform))
		}
	}

	return allErrors
}

// AddFlags adds flags related to cloudprovider for controller manager to the specified FlagSet.
func (s *CloudProviderOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&s.Name, "cloud-provider", s.Name,
		"The provider for cloud services. Empty string for no provider.")

	fs.StringVar(&s.CloudConfigFile, "cloud-config", s.CloudConfigFile,
		"The path to the cloud provider configuration file. Empty string for no configuration file.")

	fs.StringSliceVar(&s.Platforms, "cloud-provider-platforms", s.Platforms,
		"A comma-separated list of platform identifiers (ProviderID schemes, the part before \"://\") managed by this cloud controller manager. "+
			"Overrides the list returned by the cloud provider. Nodes of other platforms are ignored. "+
			"Use \"*\" to manage nodes of any platform, the cloud provider must then report nodes it does not manage as not owned. "+
			"Requires the CloudProviderNodeOwnership feature gate.")
}

// ApplyTo fills up cloudprovider config with options.
func (s *CloudProviderOptions) ApplyTo(cfg *cpconfig.CloudProviderConfiguration) error {
	if s == nil {
		return nil
	}

	cfg.Name = s.Name
	cfg.CloudConfigFile = s.CloudConfigFile
	cfg.Platforms = s.Platforms

	return nil
}
