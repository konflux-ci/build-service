/*
Copyright 2023-2026 Red Hat, Inc.

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
package webhook

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// PaCWebhookMapping allows to configure PaC webhook URL based on git repository URL.
type PaCWebhookMapping struct {
	// Git repository URL prefix to target webhook URL mapping
	mapping map[string]string
}

func NewPaCWebhookMappingFromMap(mapping map[string]string) *PaCWebhookMapping {
	return &PaCWebhookMapping{mapping: mapping}
}

func NewPaCWebhookMapping(webhookConfigPath string) (*PaCWebhookMapping, error) {
	webhookMapping := &PaCWebhookMapping{}

	mapping, err := readMapping(webhookConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read PaC webhook mapping: %w", err)
	}
	webhookMapping.mapping = mapping

	return webhookMapping, nil
}

// readMapping reads JSON config file with mapping between git repository URL prefix and target PaC webhook URL.
// Example config file content:
//
//	{
//	    "https://my-gitlab.com": "https://my.event.proxy.net:1234",
//	    "https://github.com/my-org/": "https://smee.io/SomeChannel1234"
//	}
func readMapping(webhookConfigPath string) (map[string]string, error) {
	if webhookConfigPath == "" {
		// No config is provided, don't do any mapping
		return nil, nil
	}

	// webhookConfigPath comes from binary parameters, not user input.
	// #nosec G304
	content, err := os.ReadFile(webhookConfigPath) //nolint:gosec
	if err != nil {
		return nil, err
	}

	var mapping map[string]string
	err = json.Unmarshal(content, &mapping)
	if err != nil {
		return nil, err
	}

	return mapping, nil
}

// GetPaCWebhookUrlForGitRepo returns PaC webhook URL to set into git repository webhook configuration for git providers
// that cannot access the cluster PaC Route directly (e.g. the cluster in behind a VPN and a proxy for git events needed).
// The URL is resolved based on the provided mapping between git repository url prefix and desired webhook URL.
// It finds the longest prefix match for the given git repository URL.
// Note, it's possible to have empty prefix entry, so it will match any git repo URL unless better match is found.
// It returns empty string if no mapping for the given git defined. In such case, cluster PaC Route should be used.
func (w *PaCWebhookMapping) GetPaCWebhookUrlForGitRepo(repositoryUrl string) string {
	longestPrefixLen := -1
	matchedTarget := ""
	for prefix, target := range w.mapping {
		if strings.HasPrefix(repositoryUrl, prefix) && len(prefix) > longestPrefixLen {
			longestPrefixLen = len(prefix)
			matchedTarget = target
		}
	}
	return matchedTarget
}
