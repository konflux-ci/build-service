/*
Copyright 2022.

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

package gitlab

// Allow mocking for tests
var EnsurePaCMergeRequest func(g *GitlabClient, d *PaCMergeRequestData) (string, error) = ensurePaCMergeRequest
var SetupPaCWebhook func(g *GitlabClient, projectPath, webhookUrl, webhookSecret string) error = setupPaCWebhook

type File struct {
	FullPath string
	Content  []byte
}

type PaCMergeRequestData struct {
	ProjectPath   string
	CommitMessage string
	Branch        string
	BaseBranch    string
	MrTitle       string
	MrText        string
	AuthorName    string
	AuthorEmail   string
	Files         []File
}

// ensurePaCMergeRequest creates a new merge request and returns its web URL
func ensurePaCMergeRequest(glclient *GitlabClient, d *PaCMergeRequestData) (string, error) {
	pacConfigurationUpToDate, err := glclient.filesUpToDate(d.ProjectPath, d.BaseBranch, d.Files)
	if err != nil {
		return "", err
	}
	if pacConfigurationUpToDate {
		// Nothing to do, the configuration is alredy in the main branch of the repository
		return "", nil
	}

	mrBranchExists, err := glclient.branchExist(d.ProjectPath, d.Branch)
	if err != nil {
		return "", err
	}

	if mrBranchExists {
		mrBranchUpToDate, err := glclient.filesUpToDate(d.ProjectPath, d.Branch, d.Files)
		if err != nil {
			return "", err
		}
		if !mrBranchUpToDate {
			err := glclient.commitFilesIntoBranch(d.ProjectPath, d.Branch, d.CommitMessage, d.AuthorName, d.AuthorEmail, d.Files)
			if err != nil {
				return "", err
			}
		}

		mr, err := glclient.findMergeRequestByBranches(d.ProjectPath, d.Branch, d.BaseBranch)
		if err != nil {
			return "", err
		}
		if mr != nil {
			// Merge request already exists
			return mr.WebURL, nil
		}

		diffExists, err := glclient.diffNotEmpty(d.ProjectPath, d.Branch, d.BaseBranch)
		if err != nil {
			return "", err
		}
		if !diffExists {
			// This situation occurs if an MR was merged but the branch was not deleted and main is changed after the merge.
			// Despite the fact that there is actual diff between branches, git treats it as no diff,
			// because the branch is already "included" in main.
			if err := glclient.deleteBranch(d.ProjectPath, d.Branch); err != nil {
				return "", err
			}
			return ensurePaCMergeRequest(glclient, d)
		}

		return glclient.createMergeRequestWithinRepository(d.ProjectPath, d.Branch, d.BaseBranch, d.MrTitle, d.MrText)

	} else {

		// Need to create branch and MR with Pipelines as Code configuration
		err = glclient.createBranch(d.ProjectPath, d.Branch, d.BaseBranch)
		if err != nil {
			return "", err
		}

		err = glclient.commitFilesIntoBranch(d.ProjectPath, d.Branch, d.CommitMessage, d.AuthorName, d.AuthorEmail, d.Files)
		if err != nil {
			return "", err
		}

		return glclient.createMergeRequestWithinRepository(d.ProjectPath, d.Branch, d.BaseBranch, d.MrTitle, d.MrText)
	}
}

func setupPaCWebhook(glclient *GitlabClient, projectPath, webhookUrl, webhookSecret string) error {
	existingWebhook, err := glclient.getWebhookByTargetUrl(projectPath, webhookUrl)
	if err != nil {
		return err
	}

	if existingWebhook == nil {
		_, err = glclient.createPaCWebhook(projectPath, webhookUrl, webhookSecret)
		return err
	}

	_, err = glclient.updatePaCWebhook(projectPath, existingWebhook.ID, webhookUrl, webhookSecret)
	return err
}
