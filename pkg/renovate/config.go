package renovate

import (
	"fmt"
	"os"

	"k8s.io/utils/strings/slices"
)

const (
	RenovateMatchPatternEnvName = "RENOVATE_PATTERN"
	DefaultRenovateMatchPattern = "^quay.io/redhat-appstudio-tekton-catalog/"
)

var (
	DisableAllPackageRules = PackageRule{MatchPackagePatterns: []string{"[*]"}, Enabled: false}
)

type JobConfig struct {
	Platform            string        `json:"platform"`
	Username            string        `json:"username"`
	GitAuthor           string        `json:"gitAuthor"`
	Onboarding          bool          `json:"onboarding"`
	RequireConfig       string        `json:"requireConfig"`
	EnabledManagers     []string      `json:"enabledManagers"`
	Repositories        []*Repository `json:"repositories"`
	Tekton              Tekton        `json:"tekton"`
	ForkProcessing      string        `json:"forkProcessing"`
	DependencyDashboard bool          `json:"dependencyDashboard"`
}

type Repository struct {
	Repository   string   `json:"repository"`
	BaseBranches []string `json:"baseBranches"`
}

func (r *Repository) AddBranch(branch string) {
	if !slices.Contains(r.BaseBranches, branch) {
		r.BaseBranches = append(r.BaseBranches, branch)
	}
}

type Tekton struct {
	FileMatch    []string      `json:"fileMatch"`
	IncludePaths []string      `json:"includePaths"`
	PackageRules []PackageRule `json:"packageRules"`
}

type PackageRule struct {
	MatchPackagePatterns []string `json:"matchPackagePatterns"`
	Enabled              bool     `json:"enabled"`
	MatchDepPatterns     []string `json:"matchDepPatterns,omitempty"`
	GroupName            string   `json:"groupName,omitempty"`
	BranchName           string   `json:"branchName,omitempty"`
	CommitMessageExtra   string   `json:"commitMessageExtra,omitempty"`
	CommitMessageTopic   string   `json:"commitMessageTopic,omitempty"`
	SemanticCommits      string   `json:"semanticCommits,omitempty"`
	PRFooter             string   `json:"prFooter,omitempty"`
	PRBodyColumns        []string `json:"prBodyColumns,omitempty"`
	PRBodyDefinitions    string   `json:"prBodyDefinitions,omitempty"`
	PRBodyTemplate       string   `json:"prBodyTemplate,omitempty"`
	RecreateWhen         string   `json:"recreateWhen,omitempty"`
	RebaseWhen           string   `json:"rebaseWhen,omitempty"`
}

func NewTektonJobConfig(platform, username, gitAuthor, renovatePattern string, repositories []*Repository) JobConfig {
	return JobConfig{
		Platform:        platform,
		Username:        username,
		GitAuthor:       gitAuthor,
		Onboarding:      false,
		RequireConfig:   "ignored",
		EnabledManagers: []string{"tekton"},
		Repositories:    repositories,
		Tekton: Tekton{FileMatch: []string{"\\.yaml$", "\\.yml$"}, IncludePaths: []string{".tekton/**"}, PackageRules: []PackageRule{DisableAllPackageRules, {
			MatchPackagePatterns: []string{renovatePattern},
			MatchDepPatterns:     []string{renovatePattern},
			GroupName:            "RHTAP references",
			BranchName:           "konflux/references/{{baseBranch}}",
			CommitMessageExtra:   "",
			CommitMessageTopic:   "RHTAP references",
			SemanticCommits:      "enabled",
			PRFooter:             "To execute skipped test pipelines write comment `/ok-to-test`",
			PRBodyColumns:        []string{"Package", "Change", "Notes"},
			PRBodyDefinitions:    fmt.Sprintf("{ \"Notes\": \"{{#if (or (containsString updateType 'minor') (containsString updateType 'major'))}}:warning:[migration](https://github.com/redhat-appstudio/build-definitions/blob/main/task/{{{replace '%stask-' '' packageName}}}/{{{newVersion}}}/MIGRATION.md):warning:{{/if}}\" }", renovatePattern),
			PRBodyTemplate:       "{{{header}}}{{{table}}}{{{notes}}}{{{changelogs}}}{{{footer}}}",
			RecreateWhen:         "always",
			RebaseWhen:           "behind-base-branch",
			Enabled:              true,
		}}},
		ForkProcessing:      "enabled",
		DependencyDashboard: false,
	}
}
func GetRenovatePatternConfiguration() string {
	renovatePattern := os.Getenv(RenovateMatchPatternEnvName)
	if renovatePattern == "" {
		renovatePattern = DefaultRenovateMatchPattern
	}
	return renovatePattern
}
