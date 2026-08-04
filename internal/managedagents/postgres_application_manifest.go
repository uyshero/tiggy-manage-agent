package managedagents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"tiggy-manage-agent/internal/appmanifest"
	"tiggy-manage-agent/internal/mcpregistry"
	"tiggy-manage-agent/internal/skills"
)

func (s *PostgresStore) PublishApplicationManifest(ctx context.Context, input appmanifest.PublishInput) (appmanifest.PublishResult, error) {
	input.WorkspaceID = defaultString(strings.TrimSpace(input.WorkspaceID), DefaultWorkspaceID)
	input.AppID = strings.TrimSpace(input.AppID)
	input.PublishedBy = defaultString(strings.TrimSpace(input.PublishedBy), "system")
	if input.AppID == "" {
		return appmanifest.PublishResult{}, fmt.Errorf("%w: app_id is required", ErrInvalid)
	}
	if err := appmanifest.Validate(input.Manifest); err != nil {
		return appmanifest.PublishResult{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if scope.WorkspaceID != input.WorkspaceID {
			return appmanifest.PublishResult{}, fmt.Errorf("%w: manifest workspace scope mismatch", ErrForbidden)
		}
		input.WorkspaceID = scope.WorkspaceID
	}
	if err := s.validateManifestApplicationIdentity(ctx, input.WorkspaceID, input.AppID); err != nil {
		return appmanifest.PublishResult{}, err
	}
	checksum, err := appmanifest.Checksum(input.Manifest)
	if err != nil {
		return appmanifest.PublishResult{}, fmt.Errorf("%w: calculate manifest checksum: %v", ErrInvalid, err)
	}
	result := appmanifest.PublishResult{
		SchemaVersion: appmanifest.SchemaVersionV1,
		Revision:      strings.TrimSpace(input.Manifest.Revision),
		Checksum:      checksum,
		Resources:     make([]appmanifest.ResourceResult, 0),
	}

	environmentIDs := map[string]string{}
	for _, spec := range input.Manifest.Environments {
		resource, status, reconcileErr := s.reconcileManifestEnvironment(ctx, input, spec)
		if reconcileErr != nil {
			return appmanifest.PublishResult{}, reconcileErr
		}
		environmentIDs[strings.TrimSpace(spec.ExternalRef)] = resource.ID
		result.Resources = append(result.Resources, appmanifest.ResourceResult{
			Type: appmanifest.ResourceEnvironment, ExternalRef: resource.ExternalRef, ResourceID: resource.ID, Status: status,
		})
	}
	for _, spec := range input.Manifest.Skills {
		resource, version, status, reconcileErr := s.reconcileManifestSkill(ctx, input, spec)
		if reconcileErr != nil {
			return appmanifest.PublishResult{}, reconcileErr
		}
		result.Resources = append(result.Resources, appmanifest.ResourceResult{
			Type: appmanifest.ResourceSkill, ExternalRef: resource.ExternalRef, ResourceID: resource.ID, Status: status, Version: version,
		})
	}
	for _, spec := range input.Manifest.MCPServers {
		resource, status, reconcileErr := s.reconcileManifestMCPServer(ctx, input, spec)
		if reconcileErr != nil {
			return appmanifest.PublishResult{}, reconcileErr
		}
		result.Resources = append(result.Resources, appmanifest.ResourceResult{
			Type: appmanifest.ResourceMCPServer, ExternalRef: resource.ExternalRef, ResourceID: resource.ID,
			Status: status, Version: resource.CurrentVersion,
		})
	}
	for _, spec := range input.Manifest.Agents {
		environmentID := environmentIDs[strings.TrimSpace(spec.EnvironmentRef)]
		if spec.EnvironmentRef != "" && environmentID == "" {
			environments, listErr := s.ListEnvironmentsContext(ctx)
			if listErr != nil {
				return appmanifest.PublishResult{}, listErr
			}
			for _, environment := range environments {
				if environment.AppID == input.AppID && environment.ExternalRef == strings.TrimSpace(spec.EnvironmentRef) {
					environmentID = environment.ID
					break
				}
			}
			if environmentID == "" {
				return appmanifest.PublishResult{}, fmt.Errorf("%w: Agent %q references unknown Environment %q", ErrInvalid, spec.ExternalRef, spec.EnvironmentRef)
			}
		}
		resource, status, reconcileErr := s.reconcileManifestAgent(ctx, input, spec, environmentID)
		if reconcileErr != nil {
			return appmanifest.PublishResult{}, reconcileErr
		}
		result.Resources = append(result.Resources, appmanifest.ResourceResult{
			Type: appmanifest.ResourceAgent, ExternalRef: resource.ExternalRef, ResourceID: resource.ID,
			Status: status, Version: resource.CurrentConfigVersion,
		})
	}
	return result, nil
}

func (s *PostgresStore) validateManifestApplicationIdentity(ctx context.Context, workspaceID, appID string) error {
	tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateApplicationIdentityTx(ctx, tx, workspaceID, appID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) reconcileManifestEnvironment(ctx context.Context, input appmanifest.PublishInput, spec appmanifest.EnvironmentSpec) (Environment, string, error) {
	externalRef := strings.TrimSpace(spec.ExternalRef)
	existing, found, err := s.findManifestEnvironment(ctx, input.AppID, externalRef)
	if err != nil {
		return Environment{}, "", err
	}
	config := manifestRawOrDefault(spec.Config, `{}`)
	if !found {
		created, createErr := s.CreateEnvironmentContext(ctx, CreateEnvironmentInput{
			WorkspaceID: input.WorkspaceID, AppID: input.AppID, ExternalRef: externalRef,
			Labels: spec.Labels, Name: spec.Name, Config: config,
		})
		return created, appmanifest.StatusCreated, createErr
	}
	if existing.Name == strings.TrimSpace(spec.Name) && manifestJSONEqual(existing.Config, config, `{}`) && reflect.DeepEqual(existing.Labels, manifestLabels(spec.Labels)) {
		return existing, appmanifest.StatusUnchanged, nil
	}
	updated, err := s.updateManifestEnvironment(ctx, existing, spec, config)
	return updated, appmanifest.StatusUpdated, err
}

func (s *PostgresStore) findManifestEnvironment(ctx context.Context, appID, externalRef string) (Environment, bool, error) {
	items, err := s.ListEnvironmentsContext(ctx)
	if err != nil {
		return Environment{}, false, err
	}
	for _, item := range items {
		if item.AppID == appID && item.ExternalRef == externalRef && item.ArchivedAt == nil {
			return item, true, nil
		}
	}
	return Environment{}, false, nil
}

func (s *PostgresStore) updateManifestEnvironment(ctx context.Context, current Environment, spec appmanifest.EnvironmentSpec, config json.RawMessage) (Environment, error) {
	labels, err := marshalResourceLabels(manifestLabels(spec.Labels))
	if err != nil {
		return Environment{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, current.WorkspaceID)
	if err != nil {
		return Environment{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE environments SET name = $2, config_json = $3, labels_json = $4
		WHERE id = $1 AND app_id = $5 AND external_ref = $6 AND archived_at IS NULL
	`, current.ID, strings.TrimSpace(spec.Name), config, labels, current.AppID, current.ExternalRef); err != nil {
		return Environment{}, normalizeApplicationResourceWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return Environment{}, err
	}
	return s.GetEnvironmentContext(ctx, current.ID)
}

func (s *PostgresStore) reconcileManifestSkill(ctx context.Context, input appmanifest.PublishInput, spec appmanifest.SkillSpec) (skills.Skill, int, string, error) {
	externalRef := strings.TrimSpace(spec.ExternalRef)
	items, err := s.ListSkills(ctx, skills.ListSkillsInput{WorkspaceID: input.WorkspaceID, AppID: input.AppID, ExternalRef: externalRef, IncludeArchived: true})
	if err != nil {
		return skills.Skill{}, 0, "", err
	}
	if len(items) > 0 && items[0].Status != skills.StatusActive {
		return skills.Skill{}, 0, "", fmt.Errorf("%w: manifest Skill %q is archived", ErrConflict, externalRef)
	}
	versionInput := manifestSkillVersionInput(spec.Version, input.PublishedBy)
	if len(items) == 0 {
		created, createErr := s.CreateSkill(ctx, manifestCreateSkillInput(input, spec))
		if createErr != nil {
			return skills.Skill{}, 0, "", createErr
		}
		versionInput.SkillID = created.ID
		version, versionErr := s.CreateSkillVersion(ctx, versionInput)
		return created, version.Version, appmanifest.StatusCreated, versionErr
	}
	current := items[0]
	versions, err := s.ListSkillVersions(ctx, current.ID)
	if err != nil {
		return skills.Skill{}, 0, "", err
	}
	baseChanged := !manifestSkillBaseEqual(current, spec)
	versionChanged := len(versions) == 0 || !manifestSkillVersionEqual(versions[0], spec.Version)
	if !baseChanged && !versionChanged {
		return current, versions[0].Version, appmanifest.StatusUnchanged, nil
	}
	if baseChanged {
		current, err = s.updateManifestSkill(ctx, current, spec)
		if err != nil {
			return skills.Skill{}, 0, "", err
		}
	}
	version := 0
	if len(versions) > 0 {
		version = versions[0].Version
	}
	if versionChanged {
		versionInput.SkillID = current.ID
		createdVersion, createErr := s.CreateSkillVersion(ctx, versionInput)
		if createErr != nil {
			return skills.Skill{}, 0, "", createErr
		}
		version = createdVersion.Version
	}
	return current, version, appmanifest.StatusUpdated, nil
}

func manifestCreateSkillInput(input appmanifest.PublishInput, spec appmanifest.SkillSpec) skills.CreateSkillInput {
	return skills.CreateSkillInput{
		WorkspaceID: input.WorkspaceID, AppID: input.AppID, ExternalRef: strings.TrimSpace(spec.ExternalRef), Labels: spec.Labels,
		Identifier: strings.TrimSpace(spec.Identifier), Title: strings.TrimSpace(spec.Title), Description: strings.TrimSpace(spec.Description),
		OwnerType: skills.OwnerTypeWorkspace, Visibility: skills.VisibilityWorkspace,
		SourceType: strings.TrimSpace(spec.SourceType), SourceLocator: strings.TrimSpace(spec.SourceLocator), SourcePath: strings.TrimSpace(spec.SourcePath),
		CreatedBy: input.PublishedBy,
	}
}

func manifestSkillVersionInput(spec appmanifest.SkillVersionSpec, actor string) skills.CreateVersionInput {
	return skills.CreateVersionInput{
		ContentFormat: strings.TrimSpace(spec.ContentFormat), Manifest: manifestRawOrDefault(spec.Manifest, `{}`),
		ContentText: spec.ContentText, Assets: manifestRawOrDefault(spec.Assets, `[]`),
		SourceRef: strings.TrimSpace(spec.SourceRef), SourceRevision: strings.TrimSpace(spec.SourceRevision),
		SourceURL: strings.TrimSpace(spec.SourceURL), CreatedBy: actor,
	}
}

func manifestSkillBaseEqual(current skills.Skill, spec appmanifest.SkillSpec) bool {
	return current.Identifier == strings.TrimSpace(spec.Identifier) && current.Title == strings.TrimSpace(spec.Title) &&
		current.Description == strings.TrimSpace(spec.Description) && current.SourceType == manifestSkillSourceType(spec.SourceType) &&
		current.SourceLocator == strings.TrimSpace(spec.SourceLocator) && current.SourcePath == strings.TrimSpace(spec.SourcePath) &&
		reflect.DeepEqual(current.Labels, manifestLabels(spec.Labels))
}

func manifestSkillVersionEqual(current skills.Version, spec appmanifest.SkillVersionSpec) bool {
	contentFormat := strings.TrimSpace(spec.ContentFormat)
	if contentFormat == "" {
		contentFormat = "hybrid"
	}
	return current.ContentFormat == contentFormat && current.Checksum == manifestSkillChecksum(spec) &&
		current.SourceRef == strings.TrimSpace(spec.SourceRef) && current.SourceRevision == strings.TrimSpace(spec.SourceRevision) &&
		current.SourceURL == strings.TrimSpace(spec.SourceURL)
}

func manifestSkillSourceType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return skills.SourceTypeInline
	}
	return value
}

func (s *PostgresStore) updateManifestSkill(ctx context.Context, current skills.Skill, spec appmanifest.SkillSpec) (skills.Skill, error) {
	labels, err := marshalResourceLabels(manifestLabels(spec.Labels))
	if err != nil {
		return skills.Skill{}, err
	}
	tx, err := s.beginSkillScopeTx(ctx, current.WorkspaceID)
	if err != nil {
		return skills.Skill{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE skills SET identifier = $2, title = $3, description = $4, source_type = $5,
			source_locator = $6, source_path = $7, labels_json = $8
		WHERE id = $1 AND app_id = $9 AND external_ref = $10 AND status = 'active'
	`, current.ID, strings.TrimSpace(spec.Identifier), strings.TrimSpace(spec.Title), strings.TrimSpace(spec.Description),
		manifestSkillSourceType(spec.SourceType), strings.TrimSpace(spec.SourceLocator), strings.TrimSpace(spec.SourcePath), labels,
		current.AppID, current.ExternalRef); err != nil {
		return skills.Skill{}, normalizeApplicationResourceWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return skills.Skill{}, err
	}
	return s.GetSkill(ctx, current.ID)
}

func (s *PostgresStore) reconcileManifestMCPServer(ctx context.Context, input appmanifest.PublishInput, spec appmanifest.MCPServerSpec) (mcpregistry.Server, string, error) {
	identifier := strings.TrimSpace(spec.Identifier)
	config, err := mcpregistry.NormalizeServerConfig(identifier, spec.Config)
	if err != nil {
		return mcpregistry.Server{}, "", err
	}
	items, err := s.ListMCPRegistryServers(ctx, input.WorkspaceID)
	if err != nil {
		return mcpregistry.Server{}, "", err
	}
	var current mcpregistry.Server
	for _, item := range items {
		if item.AppID == input.AppID && item.ExternalRef == strings.TrimSpace(spec.ExternalRef) {
			current = item
			break
		}
	}
	if current.ID == "" {
		created, createErr := s.CreateMCPRegistryServer(ctx, mcpregistry.CreateInput{
			WorkspaceID: input.WorkspaceID, AppID: input.AppID, ExternalRef: strings.TrimSpace(spec.ExternalRef), Labels: spec.Labels,
			Identifier: identifier, Name: strings.TrimSpace(spec.Name), Description: strings.TrimSpace(spec.Description), Config: config, CreatedBy: input.PublishedBy,
		})
		return created, appmanifest.StatusCreated, createErr
	}
	changed := current.Identifier != identifier || current.Name != strings.TrimSpace(spec.Name) ||
		current.Description != strings.TrimSpace(spec.Description) || !manifestJSONEqual(current.Config, config, `{}`) ||
		!reflect.DeepEqual(current.Labels, manifestLabels(spec.Labels)) || current.Status != mcpregistry.StatusActive
	if !changed {
		return current, appmanifest.StatusUnchanged, nil
	}
	updateConfig := json.RawMessage(nil)
	if !manifestJSONEqual(current.Config, config, `{}`) {
		updateConfig = config
	}
	current, err = s.UpdateMCPRegistryServer(ctx, mcpregistry.UpdateInput{
		ServerID: current.ID, Name: strings.TrimSpace(spec.Name), Description: strings.TrimSpace(spec.Description),
		Config: updateConfig, UpdatedBy: input.PublishedBy,
	})
	if err != nil {
		return mcpregistry.Server{}, "", err
	}
	current, err = s.updateManifestMCPMetadata(ctx, current, identifier, strings.TrimSpace(spec.Description), spec.Labels)
	if err != nil {
		return mcpregistry.Server{}, "", err
	}
	if current.Status != mcpregistry.StatusActive {
		current, err = s.SetMCPRegistryServerStatus(ctx, current.ID, mcpregistry.StatusActive, input.PublishedBy)
	}
	return current, appmanifest.StatusUpdated, err
}

func (s *PostgresStore) updateManifestMCPMetadata(ctx context.Context, current mcpregistry.Server, identifier, description string, labels map[string]string) (mcpregistry.Server, error) {
	labelsJSON, err := marshalResourceLabels(manifestLabels(labels))
	if err != nil {
		return mcpregistry.Server{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, current.WorkspaceID)
	if err != nil {
		return mcpregistry.Server{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE mcp_registry_servers SET identifier = $2, description = $3, labels_json = $4
		WHERE id = $1 AND app_id = $5 AND external_ref = $6
	`, current.ID, identifier, description, labelsJSON, current.AppID, current.ExternalRef); err != nil {
		return mcpregistry.Server{}, normalizeApplicationResourceWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return mcpregistry.Server{}, err
	}
	return s.GetMCPRegistryServer(ctx, current.ID)
}

func (s *PostgresStore) reconcileManifestAgent(ctx context.Context, input appmanifest.PublishInput, spec appmanifest.AgentSpec, environmentID string) (Agent, string, error) {
	items, err := s.ListAgentsContext(ctx)
	if err != nil {
		return Agent{}, "", err
	}
	var current Agent
	for _, item := range items {
		if item.AppID == input.AppID && item.ExternalRef == strings.TrimSpace(spec.ExternalRef) && item.ArchivedAt == nil {
			current = item
			break
		}
	}
	if current.ID == "" {
		created, createErr := s.CreateAgentContext(ctx, CreateAgentInput{
			WorkspaceID: input.WorkspaceID, AppID: input.AppID, ExternalRef: strings.TrimSpace(spec.ExternalRef), Labels: spec.Labels,
			EnvironmentID: environmentID, OwnerType: AgentOwnerWorkspace, OwnerID: input.WorkspaceID,
			Visibility: AgentVisibilityWorkspace, AgentKind: AgentKindCustom,
			Name: strings.TrimSpace(spec.Name), LLMProvider: strings.TrimSpace(spec.LLMProvider), LLMModel: strings.TrimSpace(spec.LLMModel),
			System: spec.System, Tools: spec.Tools, MCP: spec.MCP, Skills: spec.Skills,
		})
		return created, appmanifest.StatusCreated, createErr
	}
	if manifestAgentEqual(current, spec, environmentID) {
		return current, appmanifest.StatusUnchanged, nil
	}
	update := UpdateAgentInput{AgentID: current.ID}
	if current.Name != strings.TrimSpace(spec.Name) {
		update.Name = strings.TrimSpace(spec.Name)
	}
	if environmentID != "" && current.EnvironmentID != environmentID {
		update.EnvironmentID = &environmentID
	}
	if spec.LLMProvider != "" && current.ConfigVersion.LLMProvider != strings.TrimSpace(spec.LLMProvider) {
		update.LLMProvider = strings.TrimSpace(spec.LLMProvider)
	}
	if current.ConfigVersion.LLMModel != strings.TrimSpace(spec.LLMModel) {
		update.LLMModel = strings.TrimSpace(spec.LLMModel)
	}
	if current.ConfigVersion.System != spec.System {
		update.System = spec.System
	}
	if !manifestJSONEqual(current.ConfigVersion.Tools, spec.Tools, `{}`) {
		update.Tools = manifestRawOrDefault(spec.Tools, `{}`)
	}
	if !manifestJSONEqual(current.ConfigVersion.MCP, spec.MCP, `{}`) {
		update.MCP = manifestRawOrDefault(spec.MCP, `{}`)
	}
	if !manifestJSONEqual(current.ConfigVersion.Skills, spec.Skills, `{}`) {
		update.Skills = manifestRawOrDefault(spec.Skills, `{}`)
	}
	current, err = s.UpdateAgentContext(ctx, update)
	if err != nil {
		return Agent{}, "", err
	}
	if !reflect.DeepEqual(current.Labels, manifestLabels(spec.Labels)) {
		current, err = s.updateManifestAgentLabels(ctx, current, spec.Labels)
	}
	return current, appmanifest.StatusUpdated, err
}

func manifestAgentEqual(current Agent, spec appmanifest.AgentSpec, environmentID string) bool {
	providerEqual := strings.TrimSpace(spec.LLMProvider) == "" || current.ConfigVersion.LLMProvider == strings.TrimSpace(spec.LLMProvider)
	environmentEqual := environmentID == "" || current.EnvironmentID == environmentID
	return current.Name == strings.TrimSpace(spec.Name) && providerEqual && environmentEqual &&
		current.ConfigVersion.LLMModel == strings.TrimSpace(spec.LLMModel) && current.ConfigVersion.System == spec.System &&
		manifestJSONEqual(current.ConfigVersion.Tools, spec.Tools, `{}`) && manifestJSONEqual(current.ConfigVersion.MCP, spec.MCP, `{}`) &&
		manifestJSONEqual(current.ConfigVersion.Skills, spec.Skills, `{}`) && reflect.DeepEqual(current.Labels, manifestLabels(spec.Labels))
}

func (s *PostgresStore) updateManifestAgentLabels(ctx context.Context, current Agent, labels map[string]string) (Agent, error) {
	labelsJSON, err := marshalResourceLabels(manifestLabels(labels))
	if err != nil {
		return Agent{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, current.WorkspaceID)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE agents SET labels_json = $2 WHERE id = $1 AND app_id = $3`, current.ID, labelsJSON, current.AppID); err != nil {
		return Agent{}, err
	}
	if err := tx.Commit(); err != nil {
		return Agent{}, err
	}
	return s.GetAgentContext(ctx, current.ID)
}

func manifestLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return map[string]string{}
	}
	return labels
}

func manifestRawOrDefault(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(fallback)
	}
	return raw
}

func manifestJSONEqual(left, right json.RawMessage, fallback string) bool {
	left = manifestRawOrDefault(left, fallback)
	right = manifestRawOrDefault(right, fallback)
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return string(left) == string(right)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func manifestSkillChecksum(spec appmanifest.SkillVersionSpec) string {
	manifest := manifestRawOrDefault(spec.Manifest, `{}`)
	assets := manifestRawOrDefault(spec.Assets, `[]`)
	sum := sha256.Sum256([]byte(string(manifest) + "\x00" + spec.ContentText + "\x00" + string(assets)))
	return hex.EncodeToString(sum[:])
}
