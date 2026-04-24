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
	"os"
	"path"
	"testing"

	. "github.com/onsi/gomega"
)

func Test_NewPaCWebhookMappingFromMap(t *testing.T) {
	g := NewWithT(t)
	t.Run("Should create empty mapping from data in map", func(t *testing.T) {
		mappingConfig := map[string]string{
			"https://githost.com":      "https://githost.proxy.net",
			"https://githost.com/org/": "https://githost.org.proxy.net",
		}
		m := NewPaCWebhookMappingFromMap(mappingConfig)
		g.Expect(m).ToNot(BeNil())
		g.Expect(m.mapping).To(Equal(mappingConfig))
	})

}

func Test_NewPaCWebhookMapping(t *testing.T) {
	g := NewWithT(t)

	createTempWebhookConfigJson := func(t *testing.T, webhookConfigJson string) (string, error) {
		tmpFile, err := os.CreateTemp("", "webhook-config-*.json")
		if err != nil {
			return "", err
		}
		defer func() {
			_ = tmpFile.Close()
		}()

		configPath := tmpFile.Name()
		t.Cleanup(func() {
			_ = os.Remove(configPath)
		})

		_, err = tmpFile.WriteString(webhookConfigJson)
		if err != nil {
			return "", err
		}
		return configPath, nil
	}

	t.Run("Should create empty webhook mapping", func(t *testing.T) {
		m, err := NewPaCWebhookMapping("")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(m).ToNot(BeNil())
	})

	t.Run("Should create webhook mapping", func(t *testing.T) {
		mappingConfig := `{
			"https://githost.com":      "https://githost.proxy.net",
			"https://githost.com/org/": "https://githost.org.proxy.net"
		}`
		configPath, err := createTempWebhookConfigJson(t, mappingConfig)
		g.Expect(err).ToNot(HaveOccurred())

		m, err := NewPaCWebhookMapping(configPath)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(m).ToNot(BeNil())
		g.Expect(m.mapping).To(HaveLen(2))
		g.Expect(m.mapping).To(HaveKeyWithValue("https://githost.com", "https://githost.proxy.net"))
		g.Expect(m.mapping).To(HaveKeyWithValue("https://githost.com/org/", "https://githost.org.proxy.net"))
	})

	t.Run("Should fail to create webhook mapping if config file doesn't exist", func(t *testing.T) {
		m, err := NewPaCWebhookMapping(path.Join(os.TempDir(), "non-existent-file.json"))
		g.Expect(err).To(HaveOccurred())
		g.Expect(m).To(BeNil())
	})

	t.Run("Should fail to create webhook mapping if config file has invalid json", func(t *testing.T) {
		mappingConfig := `{
			"https://githost.com":      "https://githost.proxy.net,
			"https://githost.com/org/": "https://githost.org.proxy.net"
		}`
		configPath, err := createTempWebhookConfigJson(t, mappingConfig)
		g.Expect(err).ToNot(HaveOccurred())

		m, err := NewPaCWebhookMapping(configPath)
		g.Expect(err).To(HaveOccurred())
		g.Expect(m).To(BeNil())
	})
}

func Test_GetPaCWebhookUrlForGitRepo(t *testing.T) {
	g := NewWithT(t)
	tests := []struct {
		name          string
		webhookConfig map[string]string
		gitRepoUrl    string
		expected      string
	}{
		{
			name:          "Should return empty value if config is empty",
			webhookConfig: nil,
			gitRepoUrl:    "https://githost.com/org/repo.git",
			expected:      "",
		},
		{
			name: "Should return empty value if mapping for the git provider if not defined",
			webhookConfig: map[string]string{
				"https://githost.com":      "https://githost.proxy.net",
				"https://githost.com/org/": "https://githost.org.proxy.net",
			},
			gitRepoUrl: "https://myhost.com/org/repo.git",
			expected:   "",
		},
		{
			name: "Should return defined mapping",
			webhookConfig: map[string]string{
				"https://somehost.com":     "https://somehost.proxy.net",
				"https://githost.com/":     "https://githost.proxy.net",
				"https://anotherhost.com/": "https://anotherhost.proxy.net",
			},
			gitRepoUrl: "https://githost.com/org/repo.git",
			expected:   "https://githost.proxy.net",
		},
		{
			name: "Should return most matched mapping",
			webhookConfig: map[string]string{
				"https://githost.com":      "https://githost.proxy.net",
				"https://githost.com/org/": "https://githost.org.proxy.net",
				"https://somehost.com":     "https://somehost.proxy.net",
			},
			gitRepoUrl: "https://githost.com/org/repo.git",
			expected:   "https://githost.org.proxy.net",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := PaCWebhookMapping{mapping: tc.webhookConfig}
			got := m.GetPaCWebhookUrlForGitRepo(tc.gitRepoUrl)
			g.Expect(got).To(Equal(tc.expected))
		})
	}
}
