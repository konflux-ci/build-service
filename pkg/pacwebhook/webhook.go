/*
Copyright 2023-2025 Red Hat, Inc.

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
	"strings"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log logr.Logger = ctrl.Log.WithName("webhook")

type WebhookURLLoader interface {
	Load(repositoryUrl string) string
}

type ConfigWebhookURLLoader struct {
	// Prefix to target url mapping
	mapping map[string]string
}

func NewConfigWebhookURLLoader(mapping map[string]string) ConfigWebhookURLLoader {
	return ConfigWebhookURLLoader{mapping: mapping}
}

// Load implements WebhookURLLoader.
// Load allows to configure the PaC webhook target url based on the repository url of the component.
// The PaC webhook target url config is read from the provided config file or environment variable (has precedence).
// In case no config file or environment variable are provided, the default PaC route in the cluster will be used.
// Find the longest prefix match of `repositoryUrl` and the keys of `mapping`, and return the value of that key.
func (c ConfigWebhookURLLoader) Load(repositoryUrl string) string {
	longestPrefixLen := 0
	matchedTarget := ""
	for prefix, target := range c.mapping {
		if strings.HasPrefix(repositoryUrl, prefix) && len(prefix) > longestPrefixLen {
			longestPrefixLen = len(prefix)
			matchedTarget = target
		}
	}

	// Provide a default using the empty string
	if matchedTarget == "" {
		if val, ok := c.mapping[""]; ok {
			matchedTarget = val
		}
	}

	return matchedTarget
}

var _ WebhookURLLoader = ConfigWebhookURLLoader{}

type FileReader func(name string) ([]byte, error)

// Load the prefix to target url from a file
func LoadMappingFromFile(path string, fileReader FileReader) (map[string]string, error) {
	if path == "" {
		log.Info("Webhook config was not provided")
		return map[string]string{}, nil
	}

	content, err := fileReader(path)
	if err != nil {
		return nil, err
	}

	var mapping map[string]string
	err = json.Unmarshal(content, &mapping)
	if err != nil {
		return nil, err
	}

	log.Info("Using webhook config", "config", mapping)

	return mapping, nil
}
