/*
Copyright 2021-2026 Red Hat, Inc.

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
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	compapiv1alpha1 "github.com/konflux-ci/application-api/api/v1alpha1"
	"github.com/konflux-ci/build-service/pkg/boerrors"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// sanitizeVersionName converts version name to lowercase and removes invalid characters.
// Returns the sanitized version name string.
func sanitizeVersionName(name string) string {
	// Convert to lowercase
	sanitized := strings.ToLower(name)
	// Replace invalid characters with hyphens, keep only alphanumeric and hyphens
	reg := regexp.MustCompile(`[^a-z0-9\-]`)
	sanitized = reg.ReplaceAllString(sanitized, "-")
	// Remove leading/trailing hyphens
	sanitized = strings.Trim(sanitized, "-")
	return sanitized
}

// validateVersions performs validation checks on version definitions.
// Returns a slice of validation error messages describing any issues found,
// or an empty slice if all validations pass.
func validateVersions(component *compapiv1alpha1.Component) (validationErrors []string) {
	// Track sanitized version names for uniqueness check
	sanitizedNames := make(map[string]string) // map[sanitized]original

	for i, version := range component.Spec.Source.Versions {
		// Check version name is set
		if version.Name == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("spec.source.versions[%d].name is required", i))
			continue
		}

		// Check revision is set
		if version.Revision == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("spec.source.versions[%d].revision is required for version '%s'", i, version.Name))
		}

		// Check for duplicate sanitized names
		sanitized := sanitizeVersionName(version.Name)
		if sanitized == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("spec.source.versions[%d].name '%s' becomes empty after sanitization", i, version.Name))
			continue
		}

		if originalName, exists := sanitizedNames[sanitized]; exists {
			validationErrors = append(validationErrors, fmt.Sprintf("spec.source.versions[%d].name '%s' conflicts with version '%s' after sanitization (both become '%s')", i, version.Name, originalName, sanitized))
		} else {
			sanitizedNames[sanitized] = version.Name
		}
	}
	return validationErrors
}

// hasPipelineRefGit checks if any PipelineRefGit field is set.
// Returns true if any field is non-empty, false otherwise.
func hasPipelineRefGit(refGit compapiv1alpha1.PipelineRefGit) bool {
	return refGit.Url != "" || refGit.PathInRepo != "" || refGit.Revision != ""
}

// hasPipelineRefName checks if PipelineRefName is set.
// Returns true if the reference name is non-empty, false otherwise.
func hasPipelineRefName(refName string) bool {
	return refName != ""
}

// hasPipelineSpecFromBundle checks if any PipelineSpecFromBundle field is set.
// Returns true if any field is non-empty, false otherwise.
func hasPipelineSpecFromBundle(specFromBundle compapiv1alpha1.PipelineSpecFromBundle) bool {
	return specFromBundle.Bundle != "" || specFromBundle.Name != ""
}

// validatePipelineRefGitFields validates that all PipelineRefGit fields are set.
// Returns a slice of validation error messages for any missing required fields,
// or an empty slice if all fields are present.
func validatePipelineRefGitFields(refGit compapiv1alpha1.PipelineRefGit, errorPrefix string) (errors []string) {
	if refGit.Url == "" {
		errors = append(errors, fmt.Sprintf("%s: pipelineref-by-git-resolver.url is required", errorPrefix))
	}
	if refGit.PathInRepo == "" {
		errors = append(errors, fmt.Sprintf("%s: pipelineref-by-git-resolver.pathInRepo is required", errorPrefix))
	}
	if refGit.Revision == "" {
		errors = append(errors, fmt.Sprintf("%s: pipelineref-by-git-resolver.revision is required", errorPrefix))
	}
	return errors
}

// validatePipelineSpecFromBundleFields validates that all PipelineSpecFromBundle fields are set.
// Returns a slice of validation error messages for any missing required fields,
// or an empty slice if all fields are present.
func validatePipelineSpecFromBundleFields(specFromBundle compapiv1alpha1.PipelineSpecFromBundle, errorPrefix string) (errors []string) {
	if specFromBundle.Bundle == "" {
		errors = append(errors, fmt.Sprintf("%s: pipelinespec-from-bundle.bundle is required", errorPrefix))
	}
	if specFromBundle.Name == "" {
		errors = append(errors, fmt.Sprintf("%s: pipelinespec-from-bundle.name is required", errorPrefix))
	}
	return errors
}

// hasPipelineDefConfig checks if a pipeline definition has any configuration.
// Returns true if any pipeline configuration is present, false otherwise.
func hasPipelineDefConfig(pipelineDef compapiv1alpha1.PipelineDefinition) bool {
	return hasPipelineRefGit(pipelineDef.PipelineRefGit) ||
		hasPipelineRefName(pipelineDef.PipelineRefName) ||
		hasPipelineSpecFromBundle(pipelineDef.PipelineSpecFromBundle)
}

// hasPipelineConfig checks if any pipeline configuration is set.
// Returns true if any pipeline configuration (PullAndPush, Pull, or Push) is present, false otherwise.
func hasPipelineConfig(pipeline compapiv1alpha1.ComponentBuildPipeline) bool {
	hasPullAndPush := hasPipelineDefConfig(pipeline.PullAndPush)
	hasPull := hasPipelineDefConfig(pipeline.Pull)
	hasPush := hasPipelineDefConfig(pipeline.Push)

	return hasPullAndPush || hasPull || hasPush
}

// extractPipelineDef extracts a PipelineDef from a PipelineDefinition.
// Returns a pointer to the extracted PipelineDef structure.
func extractPipelineDef(pipelineDef compapiv1alpha1.PipelineDefinition) *PipelineDef {
	def := &PipelineDef{}

	// Check if PipelineRefGit is set
	if hasPipelineRefGit(pipelineDef.PipelineRefGit) {
		def.PipelineRefGit = &pipelineDef.PipelineRefGit
	}

	// Check if PipelineRefName is set
	if hasPipelineRefName(pipelineDef.PipelineRefName) {
		def.PipelineRefName = pipelineDef.PipelineRefName
	}

	// Check if PipelineSpecFromBundle is set
	if hasPipelineSpecFromBundle(pipelineDef.PipelineSpecFromBundle) {
		def.PipelineSpecFromBundle = &pipelineDef.PipelineSpecFromBundle
	}

	return def
}

// validatePipelineConfiguration checks pipeline configuration validity.
// If versionName is empty, validates default pipeline configuration.
// Empty pipeline configuration is valid and will pass validation.
// Performs the following validations:
// - Ensures PullAndPush is not used together with separate Pull or Push sections
// - Validates that each pipeline section has only one definition type (PipelineRefGit, PipelineRefName, or PipelineSpecFromBundle)
// - Validates that all required fields are set for PipelineRefGit and PipelineSpecFromBundle when used
// Returns a slice of validation error messages describing any issues found,
// or an empty slice if all validations pass.
func validatePipelineConfiguration(pipeline compapiv1alpha1.ComponentBuildPipeline, versionName string) (validationErrors []string) {
	// Determine the prefix for error messages
	prefix := "default pipeline"
	if versionName != "" {
		prefix = fmt.Sprintf("version '%s' pipeline", versionName)
	}

	// Check which sections have configuration
	hasPullAndPush := hasPipelineDefConfig(pipeline.PullAndPush)
	hasPull := hasPipelineDefConfig(pipeline.Pull)
	hasPush := hasPipelineDefConfig(pipeline.Push)

	// Validate that PullAndPush cannot be used together with Pull or Push
	// When using separate Pull/Push sections, either or both can be specified
	// Missing Pull or Push will be filled from default configuration
	if hasPullAndPush && (hasPull || hasPush) {
		validationErrors = append(validationErrors, fmt.Sprintf("%s: cannot specify pull-and-push together with pull or push sections", prefix))
	}

	// Validate each pipeline definition section
	checkPipelineDef := func(pipelineDef compapiv1alpha1.PipelineDefinition, pipelineType string) {
		if !hasPipelineDefConfig(pipelineDef) {
			return // Skip validation if no configuration is set
		}
		// Count how many pipeline definition types are set based on any field in the struct
		definitionCount := 0

		hasRefGit := hasPipelineRefGit(pipelineDef.PipelineRefGit)
		hasRefName := hasPipelineRefName(pipelineDef.PipelineRefName)
		hasSpecFromBundle := hasPipelineSpecFromBundle(pipelineDef.PipelineSpecFromBundle)

		if hasRefGit {
			definitionCount++
		}
		if hasRefName {
			definitionCount++
		}
		if hasSpecFromBundle {
			definitionCount++
		}

		if definitionCount > 1 {
			validationErrors = append(validationErrors, fmt.Sprintf("%s: %s section has multiple definitions specified (only one of pipelineref-by-git-resolver, pipelineref-by-name, or pipelinespec-from-bundle allowed)", prefix, pipelineType))
		}

		// Validate PipelineRefGit - all fields are required when any field is set
		if hasRefGit {
			errorPrefix := fmt.Sprintf("%s: %s section", prefix, pipelineType)
			validationErrors = append(validationErrors, validatePipelineRefGitFields(pipelineDef.PipelineRefGit, errorPrefix)...)
		}

		// Validate PipelineSpecFromBundle - all fields are required when any field is set
		if hasSpecFromBundle {
			errorPrefix := fmt.Sprintf("%s: %s section", prefix, pipelineType)
			validationErrors = append(validationErrors, validatePipelineSpecFromBundleFields(pipelineDef.PipelineSpecFromBundle, errorPrefix)...)
		}
	}

	// Check each pipeline type
	checkPipelineDef(pipeline.PullAndPush, "pull-and-push")
	checkPipelineDef(pipeline.Pull, "pull")
	checkPipelineDef(pipeline.Push, "push")

	return validationErrors
}

// validatePipelines performs validation checks on pipeline configurations and extracts pipeline definitions.
// Returns a slice of validation error messages and a map of version names to their pipeline definitions.
func validatePipelines(component *compapiv1alpha1.Component) ([]string, map[string]*VersionPipelineDefinition) {
	var validationErrors []string
	versionPipelines := make(map[string]*VersionPipelineDefinition)

	// Validate default build pipeline configuration if present
	if hasPipelineConfig(component.Spec.DefaultBuildPipeline) {
		pipelineErrors := validatePipelineConfiguration(component.Spec.DefaultBuildPipeline, "")
		validationErrors = append(validationErrors, pipelineErrors...)
	}

	defaultPipeline := component.Spec.DefaultBuildPipeline
	hasDefaultPipeline := hasPipelineConfig(defaultPipeline)

	// Validate version-specific pipeline configurations and extract pipeline definitions
	for _, version := range component.Spec.Source.Versions {
		if hasPipelineConfig(version.BuildPipeline) {
			pipelineErrors := validatePipelineConfiguration(version.BuildPipeline, version.Name)
			validationErrors = append(validationErrors, pipelineErrors...)
		}

		// Extract pipeline definition for this version, merging version-specific and default configurations
		versionPipelineDef := &VersionPipelineDefinition{}
		hasPipeline := false

		// Determine source pipelines for Pull and Push
		versionPipeline := version.BuildPipeline
		hasVersionPipeline := hasPipelineConfig(versionPipeline)

		if !hasVersionPipeline && !hasDefaultPipeline {
			// No pipeline configured for this version
			versionPipelines[version.Name] = nil
			continue
		}

		// Extract Pull and Push, preferring version-specific over default
		// Handle PullAndPush (which sets both Pull and Push to the same value)
		if hasVersionPipeline && hasPipelineDefConfig(versionPipeline.PullAndPush) {
			// Version-specific PullAndPush takes precedence
			pipelineDef := extractPipelineDef(versionPipeline.PullAndPush)
			versionPipelineDef.Pull = pipelineDef
			versionPipelineDef.Push = pipelineDef
			hasPipeline = true
		} else if hasDefaultPipeline && hasPipelineDefConfig(defaultPipeline.PullAndPush) {
			// Fall back to default PullAndPush only if version doesn't have separate Pull/Push
			if !hasPipelineDefConfig(versionPipeline.Pull) && !hasPipelineDefConfig(versionPipeline.Push) {
				pipelineDef := extractPipelineDef(defaultPipeline.PullAndPush)
				versionPipelineDef.Pull = pipelineDef
				versionPipelineDef.Push = pipelineDef
				hasPipeline = true
			}
		}

		// Extract separate Pull and Push if not already set by PullAndPush
		if versionPipelineDef.Pull == nil {
			// Try version-specific Pull first, then default Pull
			if hasVersionPipeline && hasPipelineDefConfig(versionPipeline.Pull) {
				versionPipelineDef.Pull = extractPipelineDef(versionPipeline.Pull)
				hasPipeline = true
			} else if hasDefaultPipeline && hasPipelineDefConfig(defaultPipeline.Pull) {
				versionPipelineDef.Pull = extractPipelineDef(defaultPipeline.Pull)
				hasPipeline = true
			}
		}

		if versionPipelineDef.Push == nil {
			// Try version-specific Push first, then default Push
			if hasVersionPipeline && hasPipelineDefConfig(versionPipeline.Push) {
				versionPipelineDef.Push = extractPipelineDef(versionPipeline.Push)
				hasPipeline = true
			} else if hasDefaultPipeline && hasPipelineDefConfig(defaultPipeline.Push) {
				versionPipelineDef.Push = extractPipelineDef(defaultPipeline.Push)
				hasPipeline = true
			}
		}

		if hasPipeline {
			versionPipelines[version.Name] = versionPipelineDef
		} else {
			versionPipelines[version.Name] = nil
		}
	}
	return validationErrors, versionPipelines
}

// validateDefaultPipelinesConfig validates a pipeline configuration from default pipelines config.
// Returns a slice of validation error messages describing any issues found,
// or an empty slice if all validations pass.
func validateDefaultPipelinesConfig(pipeline *BuildPipeline, index int) (errors []string) {
	if pipeline == nil {
		return errors
	}

	// Pipeline entry must always have a Name
	if pipeline.Name == "" {
		errors = append(errors, fmt.Sprintf("pipeline entry at index %d must have a name specified", index))
	}

	// Generate error prefix based on whether name is available
	var errorPrefix string
	if pipeline.Name != "" {
		errorPrefix = fmt.Sprintf("pipeline '%s'", pipeline.Name)
	} else {
		errorPrefix = fmt.Sprintf("pipeline entry at index %d", index)
	}

	hasRefGit := hasPipelineRefGit(pipeline.PipelineRefGit)
	hasSpecFromBundle := hasPipelineSpecFromBundle(pipeline.PipelineSpecFromBundle)

	// Check that PipelineRefGit and PipelineSpecFromBundle are not both specified
	if hasRefGit && hasSpecFromBundle {
		errors = append(errors, fmt.Sprintf("%s has multiple definitions specified (only one of pipelineref-by-git-resolver or pipelinespec-from-bundle allowed)", errorPrefix))
	}

	// If neither PipelineRefGit nor PipelineSpecFromBundle are specified, Name & Bundle must both be set
	if !hasRefGit && !hasSpecFromBundle {
		if pipeline.Bundle == "" {
			errors = append(errors, fmt.Sprintf("%s must have bundle specified when pipelineref-by-git-resolver and pipelinespec-from-bundle are not set", errorPrefix))
		}
	}

	// Validate PipelineRefGit - all fields are required when any field is set
	if hasRefGit {
		errors = append(errors, validatePipelineRefGitFields(pipeline.PipelineRefGit, errorPrefix)...)
	}

	// Validate PipelineSpecFromBundle - all fields are required when any field is set
	if hasSpecFromBundle {
		errors = append(errors, validatePipelineSpecFromBundleFields(pipeline.PipelineSpecFromBundle, errorPrefix)...)

		// Verify that pipeline.Name matches PipelineSpecFromBundle.Name
		if pipeline.Name != "" && pipeline.PipelineSpecFromBundle.Name != "" && pipeline.Name != pipeline.PipelineSpecFromBundle.Name {
			errors = append(errors, fmt.Sprintf("%s name must match pipelinespec-from-bundle.name '%s'", errorPrefix, pipeline.PipelineSpecFromBundle.Name))
		}
	}

	return errors
}

// validatePipelineResolution validates that pipelines using "latest" can be resolved from config.
// Returns a slice of validation error messages describing any resolution issues,
// or an empty slice if all pipelines can be resolved.
func validatePipelineResolution(versionPipelines map[string]*VersionPipelineDefinition, versionsToOnboardWithPR []string, defaultPipelinesConfig *pipelineConfig) (errors []string) {
	for _, versionName := range versionsToOnboardWithPR {
		pipelineDef, exists := versionPipelines[versionName]
		if !exists || pipelineDef == nil {
			continue
		}
		checkPipelineExists := func(def *PipelineDef, pipelineType string) {
			if def != nil && def.PipelineSpecFromBundle != nil && def.PipelineSpecFromBundle.Bundle == "latest" {
				pipelineName := def.PipelineSpecFromBundle.Name
				// Find the pipeline in config by name
				found := false
				for _, configPipeline := range defaultPipelinesConfig.Pipelines {
					if configPipeline.Name == pipelineName {
						// Check if pipeline in config has bundle defined (either in PipelineSpecFromBundle or directly)
						hasBundle := configPipeline.PipelineSpecFromBundle.Bundle != "" || configPipeline.Bundle != ""
						if !hasBundle {
							errors = append(errors, fmt.Sprintf("version '%s' %s pipeline references '%s' with bundle 'latest', but pipeline in config has no bundle specified", versionName, pipelineType, pipelineName))
						}
						found = true
						break
					}
				}
				if !found {
					errors = append(errors, fmt.Sprintf("version '%s' %s pipeline references '%s' which is not found in config", versionName, pipelineType, pipelineName))
				}
			}
		}
		checkPipelineExists(pipelineDef.Pull, "pull")
		checkPipelineExists(pipelineDef.Push, "push")
	}
	return errors
}

// resolvePipelineDefinitions resolves missing pipeline definitions to defaults and "latest" bundle references to actual values
func resolvePipelineDefinitions(versionPipelines map[string]*VersionPipelineDefinition, versionsToOnboardWithPR []string, defaultPipeline *BuildPipeline, defaultPipelinesConfig *pipelineConfig) {
	// Helper function to create a default PipelineDef from the default pipeline configuration
	createDefaultPipelineDef := func() *PipelineDef {
		def := &PipelineDef{}
		if defaultPipeline.PipelineRefGit.Url != "" {
			def.PipelineRefGit = &defaultPipeline.PipelineRefGit
		} else if defaultPipeline.PipelineSpecFromBundle.Bundle != "" {
			def.PipelineSpecFromBundle = &defaultPipeline.PipelineSpecFromBundle
		} else {
			// Fall back to Name & Bundle
			def.PipelineSpecFromBundle = &compapiv1alpha1.PipelineSpecFromBundle{
				Name:   defaultPipeline.Name,
				Bundle: defaultPipeline.Bundle,
			}
		}
		def.AdditionalParams = defaultPipeline.AdditionalParams
		return def
	}

	// Helper function to resolve pipeline definition from config (resolves "latest" bundle and applies AdditionalParams)
	resolvePipelineDefFromConfig := func(def *PipelineDef) {
		if def == nil {
			return
		}
		for _, configPipeline := range defaultPipelinesConfig.Pipelines {
			matched := false

			// Match by PipelineRefGit
			if def.PipelineRefGit != nil && configPipeline.PipelineRefGit.Url != "" {
				if def.PipelineRefGit.Url == configPipeline.PipelineRefGit.Url &&
					def.PipelineRefGit.PathInRepo == configPipeline.PipelineRefGit.PathInRepo &&
					def.PipelineRefGit.Revision == configPipeline.PipelineRefGit.Revision {
					matched = true
				}
			}

			// Match by PipelineSpecFromBundle
			if def.PipelineSpecFromBundle != nil {
				// Handle "latest" bundle - resolve to actual bundle and set AdditionalParams
				if def.PipelineSpecFromBundle.Bundle == "latest" && def.PipelineSpecFromBundle.Name == configPipeline.Name {
					// Check PipelineSpecFromBundle.Bundle first, fall back to Bundle
					bundle := configPipeline.PipelineSpecFromBundle.Bundle
					if bundle == "" {
						bundle = configPipeline.Bundle
					}
					def.PipelineSpecFromBundle.Bundle = bundle
					matched = true
				} else {
					// Match by exact bundle and name
					configBundle := configPipeline.PipelineSpecFromBundle.Bundle
					if configBundle == "" {
						configBundle = configPipeline.Bundle
					}
					if def.PipelineSpecFromBundle.Name == configPipeline.Name &&
						def.PipelineSpecFromBundle.Bundle == configBundle {
						matched = true
					}
				}
			}

			if matched {
				def.AdditionalParams = configPipeline.AdditionalParams
				break
			}
		}
	}

	for _, versionName := range versionsToOnboardWithPR {
		pipelineDef, exists := versionPipelines[versionName]
		if !exists || pipelineDef == nil {
			// Version has no pipeline configured - use the default pipeline for both Pull and Push
			newPipelineDef := &VersionPipelineDefinition{
				Pull: createDefaultPipelineDef(),
				Push: createDefaultPipelineDef(),
			}
			versionPipelines[versionName] = newPipelineDef
		} else {
			// Pipeline exists - resolve missing Pull or Push from default, and resolve from config
			if pipelineDef.Pull == nil {
				pipelineDef.Pull = createDefaultPipelineDef()
			} else {
				resolvePipelineDefFromConfig(pipelineDef.Pull)
			}

			if pipelineDef.Push == nil {
				pipelineDef.Push = createDefaultPipelineDef()
			} else {
				resolvePipelineDefFromConfig(pipelineDef.Push)
			}
		}
	}
}

// processDefaultPipelineConfig fetches, validates and resolves default pipeline configuration.
// Returns a slice of validation error messages (if any) and a general error for transient failures.
func (r *ComponentBuildReconciler) processDefaultPipelineConfig(
	ctx context.Context,
	versionPipelines map[string]*VersionPipelineDefinition,
	versionsToOnboardWithPR []string,
) ([]string, error) {
	log := ctrllog.FromContext(ctx)

	defaultPipelinesConfig, err := r.GetDefaultBuildPipelinesFromConfig(ctx)
	if err != nil {
		var boErr *boerrors.BuildOpError
		if errors.As(err, &boErr) && boErr.IsPersistent() {
			// Persistent error - treat as validation error
			errMsg := fmt.Sprintf("Failed to get default pipelines from config: %s", boErr.ShortError())
			log.Error(err, "Failed to get default pipelines from config")
			return []string{errMsg}, nil
		}
		// Transient error - return error to reconcile again
		return nil, fmt.Errorf("failed to get default pipelines from config: %w", err)
	}

	// Collect all validation errors
	var validationErrors []string

	// Validate all pipelines in the config
	for i := range defaultPipelinesConfig.Pipelines {
		pipelineErrors := validateDefaultPipelinesConfig(&defaultPipelinesConfig.Pipelines[i], i)
		for _, err := range pipelineErrors {
			validationErrors = append(validationErrors, fmt.Sprintf("Build service configuration error, contact administrator: %s", err))
		}
	}

	// Validate that config has DefaultPipelineName set
	if defaultPipelinesConfig.DefaultPipelineName == "" {
		validationErrors = append(validationErrors, "Build service configuration error, contact administrator: config must have default-pipeline-name specified")
	}

	// Find the default pipeline in the config
	var defaultPipeline *BuildPipeline
	if defaultPipelinesConfig.DefaultPipelineName != "" {
		for i, pipeline := range defaultPipelinesConfig.Pipelines {
			if pipeline.Name == defaultPipelinesConfig.DefaultPipelineName {
				defaultPipeline = &defaultPipelinesConfig.Pipelines[i]
				break
			}
		}

		// Validate that the default pipeline exists
		if defaultPipeline == nil {
			validationErrors = append(validationErrors, fmt.Sprintf("Build service configuration error, contact administrator: default pipeline '%s' not found in config", defaultPipelinesConfig.DefaultPipelineName))
		}
	}

	// If there are validation errors in config, return them before attempting pipeline resolution
	if len(validationErrors) > 0 {
		return validationErrors, nil
	}

	// Validate that pipelines using PipelineSpecFromBundle with "latest" can be resolved from config
	pipelineResolutionErrors := validatePipelineResolution(versionPipelines, versionsToOnboardWithPR, defaultPipelinesConfig)
	validationErrors = append(validationErrors, pipelineResolutionErrors...)

	// If there are validation errors, return them without resolving
	if len(validationErrors) > 0 {
		return validationErrors, nil
	}

	// Resolve missing pipeline definitions and "latest" bundle references
	resolvePipelineDefinitions(versionPipelines, versionsToOnboardWithPR, defaultPipeline, defaultPipelinesConfig)

	return nil, nil
}
