/*
Copyright 2022-2025 Red Hat, Inc.

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

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestExtractCommitHash(t *testing.T) {
	tests := []struct {
		name      string
		commitUrl string
		want      string
	}{
		{
			name:      "should extract commit hash from commit url",
			commitUrl: "https://forge.com/project/repository/-/tree/4b00cdb6ceb84d3953d8987e3e06f967a6d86e76",
			want:      "4b00cdb6ceb84d3953d8987e3e06f967a6d86e76",
		},
		{
			name:      "should return empty if commit url is invalid",
			commitUrl: "https://forge.com/project/repository",
			want:      "",
		},
		{
			name:      "should avoid erroring if the commit url has no slashes",
			commitUrl: "forge.com",
			want:      "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := extractCommitHash(test.commitUrl)
			assert.Equal(t, got, test.want)
		})
	}
}
