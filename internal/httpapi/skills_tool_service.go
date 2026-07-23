package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skillpackage"
	skillspkg "tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tools"
)

const (
	maxSkillToolTitleChars       = 200
	maxSkillToolDescriptionChars = 4000
	maxSkillToolContentChars     = 100000
	maxSkillToolJSONBytes        = 512 << 10
	defaultSkillInspectChars     = 8000
	maxSkillInspectChars         = 8000
)

type skillsToolService struct {
	store             managedagents.Store
	marketplace       skillmarketplace.Client
	policy            skillmarketplace.Policy
	objectStore       objectstore.Client
	objectStoreBucket string
	binaryScanner     skillmarketplace.BinaryScanner
}

func newSkillsToolService(store managedagents.Store) tools.SkillsToolService {
	return newSkillsToolServiceWithDependencies(
		store,
		skillmarketplace.NewGitHubClient(os.Getenv("TMA_SKILLS_GITHUB_TOKEN")),
		skillMarketplacePolicyFromEnv(),
		objectstore.NewNoopClient(objectstore.Config{}),
		"",
	)
}

func newSkillsToolServiceWithMarketplace(store managedagents.Store, marketplace skillmarketplace.Client) skillsToolService {
	return newSkillsToolServiceWithMarketplaceAndPolicy(store, marketplace, skillmarketplace.Policy{})
}

func newSkillsToolServiceWithMarketplaceAndPolicy(store managedagents.Store, marketplace skillmarketplace.Client, policy skillmarketplace.Policy) skillsToolService {
	return newSkillsToolServiceWithDependencies(store, marketplace, policy, objectstore.NewNoopClient(objectstore.Config{}), "")
}

func newSkillsToolServiceWithDependencies(store managedagents.Store, marketplace skillmarketplace.Client, policy skillmarketplace.Policy, objectStore objectstore.Client, objectStoreBucket string) skillsToolService {
	return newSkillsToolServiceWithDependenciesAndBinaryScanner(store, marketplace, policy, objectStore, objectStoreBucket, nil)
}

func newSkillsToolServiceWithDependenciesAndBinaryScanner(store managedagents.Store, marketplace skillmarketplace.Client, policy skillmarketplace.Policy, objectStore objectstore.Client, objectStoreBucket string, binaryScanner skillmarketplace.BinaryScanner) skillsToolService {
	if objectStore == nil {
		objectStore = objectstore.NewNoopClient(objectstore.Config{})
	}
	return skillsToolService{
		store: store, marketplace: marketplace, policy: policy,
		objectStore: objectStore, objectStoreBucket: strings.TrimSpace(objectStoreBucket), binaryScanner: binaryScanner,
	}
}

func (s skillsToolService) Search(ctx context.Context, request tools.SkillsSearchRequest) (tools.SkillsSearchResponse, error) {
	registry, err := s.registry()
	if err != nil {
		return tools.SkillsSearchResponse{}, err
	}
	_, workspaceID, err := s.scope(ctx, request.SessionID, request.WorkspaceID)
	if err != nil {
		return tools.SkillsSearchResponse{}, err
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		return tools.SkillsSearchResponse{}, fmt.Errorf("%w: skills search limit cannot exceed 50", managedagents.ErrInvalid)
	}
	query := strings.ToLower(strings.TrimSpace(request.Query))
	items, err := registry.ListSkills(ctx, skillspkg.ListSkillsInput{WorkspaceID: workspaceID, IncludeArchived: request.IncludeArchived})
	if err != nil {
		return tools.SkillsSearchResponse{}, err
	}
	response := tools.SkillsSearchResponse{Query: strings.TrimSpace(request.Query), Items: make([]tools.SkillsSearchItem, 0)}
	for _, item := range items {
		if query != "" && !strings.Contains(strings.ToLower(item.Identifier+" "+item.Title+" "+item.Description), query) {
			continue
		}
		if len(response.Items) >= limit {
			response.HasMore = true
			break
		}
		searchItem := tools.SkillsSearchItem{Skill: item}
		versions, listErr := registry.ListSkillVersions(ctx, item.ID)
		if listErr != nil {
			return tools.SkillsSearchResponse{}, listErr
		}
		if len(versions) > 0 {
			latest := skillVersionForTool(versions[0])
			searchItem.LatestVersion = &latest
		}
		response.Items = append(response.Items, searchItem)
	}
	response.Count = len(response.Items)
	return response, nil
}

func (s skillsToolService) Inspect(ctx context.Context, request tools.SkillsInspectRequest) (tools.SkillsInspectResponse, error) {
	registry, err := s.registry()
	if err != nil {
		return tools.SkillsInspectResponse{}, err
	}
	_, workspaceID, err := s.scope(ctx, request.SessionID, request.WorkspaceID)
	if err != nil {
		return tools.SkillsInspectResponse{}, err
	}
	identifier := strings.TrimSpace(request.Identifier)
	if err := skillspkg.ValidateIdentifier(identifier); err != nil {
		return tools.SkillsInspectResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	skill, err := registry.GetSkillByIdentifier(ctx, workspaceID, identifier)
	if err != nil {
		return tools.SkillsInspectResponse{}, err
	}
	version, err := resolveRequestedSkillVersion(ctx, registry, skill.ID, request.Version)
	if err != nil {
		return tools.SkillsInspectResponse{}, err
	}
	projected, page, err := pagedSkillVersionContent(skillVersionForTool(version), request.ContentOffset, request.ContentMaxChars)
	if err != nil {
		return tools.SkillsInspectResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	return tools.SkillsInspectResponse{
		Skill: skill, Version: projected, ContentOffset: page.Offset, ContentChars: page.Chars,
		TotalContentChars: page.TotalChars, NextOffset: page.NextOffset, HasMore: page.HasMore,
	}, nil
}

type skillContentPage struct {
	Offset     int
	Chars      int
	TotalChars int
	NextOffset int
	HasMore    bool
}

func pagedSkillVersionContent(version skillspkg.Version, offset int, maxChars int) (skillspkg.Version, skillContentPage, error) {
	if offset < 0 {
		return skillspkg.Version{}, skillContentPage{}, errors.New("content_offset must not be negative")
	}
	if maxChars <= 0 {
		maxChars = defaultSkillInspectChars
	}
	if maxChars > maxSkillInspectChars {
		return skillspkg.Version{}, skillContentPage{}, fmt.Errorf("content_max_chars must not exceed %d", maxSkillInspectChars)
	}
	runes := []rune(version.ContentText)
	if offset > len(runes) {
		return skillspkg.Version{}, skillContentPage{}, fmt.Errorf("content_offset %d exceeds content length %d", offset, len(runes))
	}
	end := min(offset+maxChars, len(runes))
	version.ContentText = string(runes[offset:end])
	page := skillContentPage{Offset: offset, Chars: end - offset, TotalChars: len(runes), HasMore: end < len(runes)}
	if page.HasMore {
		page.NextOffset = end
	}
	return version, page, nil
}

func (s skillsToolService) Discover(ctx context.Context, request tools.SkillsDiscoverRequest) (tools.SkillsDiscoverResponse, error) {
	_, workspaceID, err := s.scope(ctx, request.SessionID, request.WorkspaceID)
	if err != nil {
		return tools.SkillsDiscoverResponse{}, err
	}
	provider := strings.ToLower(strings.TrimSpace(request.Provider))
	if provider == "" {
		provider = skillmarketplace.CatalogProvider
		if strings.TrimSpace(request.Repository) != "" {
			provider = skillmarketplace.GitHubProvider
		}
	}
	switch provider {
	case skillmarketplace.CatalogProvider:
		if strings.TrimSpace(request.Repository) != "" {
			return tools.SkillsDiscoverResponse{}, fmt.Errorf("%w: repository filter requires provider github", managedagents.ErrInvalid)
		}
		catalog, ok := s.store.(skillmarketplace.MarketplaceCatalogStore)
		if !ok {
			return tools.SkillsDiscoverResponse{}, errors.New("internal marketplace catalog is unavailable")
		}
		entries, err := catalog.BrowsePublishedMarketplaceEntries(ctx, skillmarketplace.BrowseMarketplaceEntriesInput{
			WorkspaceID: workspaceID, Query: request.Query, Category: request.Category, Tags: request.Tags, Limit: request.Limit,
		})
		if err != nil {
			return tools.SkillsDiscoverResponse{}, fmt.Errorf("discover internal skills: %w", err)
		}
		items := make([]skillmarketplace.Candidate, 0, len(entries))
		for _, entry := range entries {
			description := entry.Summary
			if description == "" {
				description = entry.SkillDescription
			}
			items = append(items, skillmarketplace.Candidate{
				Provider: skillmarketplace.CatalogProvider, Path: "SKILL.md",
				CatalogEntryID: entry.ID, CatalogSkillID: entry.SkillID, SkillVersion: entry.SkillVersion,
				Title: entry.SkillTitle, Category: entry.Category, Tags: entry.Tags, VersionChecksum: entry.VersionChecksum,
				Description: description, SuggestedIdentifier: entry.SkillIdentifier, Verified: true,
			})
		}
		return tools.SkillsDiscoverResponse{
			Provider: skillmarketplace.CatalogProvider, SearchMode: "organization_catalog", Items: items, Count: len(items),
		}, nil
	case skillmarketplace.GitHubProvider:
		if strings.TrimSpace(request.Query) == "" && strings.TrimSpace(request.Repository) == "" {
			return tools.SkillsDiscoverResponse{}, fmt.Errorf("%w: github discovery requires query or repository", managedagents.ErrInvalid)
		}
		if s.marketplace == nil {
			return tools.SkillsDiscoverResponse{}, errors.New("skills marketplace is not configured")
		}
		result, err := s.marketplace.Discover(ctx, skillmarketplace.DiscoverInput{
			Query: request.Query, Repository: request.Repository, Limit: request.Limit,
		})
		if err != nil {
			return tools.SkillsDiscoverResponse{}, fmt.Errorf("discover remote skills: %w", err)
		}
		return tools.SkillsDiscoverResponse{
			Provider: result.Provider, SearchMode: result.SearchMode, Items: result.Items, Count: result.Count,
		}, nil
	default:
		return tools.SkillsDiscoverResponse{}, fmt.Errorf("%w: unsupported skills discovery provider %q", managedagents.ErrInvalid, provider)
	}
}

func (s skillsToolService) Preview(ctx context.Context, request tools.SkillsPreviewRequest) (tools.SkillsPreviewResponse, error) {
	registry, err := s.registry()
	if err != nil {
		return tools.SkillsPreviewResponse{}, err
	}
	session, workspaceID, err := s.scope(ctx, request.SessionID, request.WorkspaceID)
	if err != nil {
		return tools.SkillsPreviewResponse{}, err
	}
	if identifier := strings.TrimSpace(request.Identifier); identifier != "" {
		if err := skillspkg.ValidateIdentifier(identifier); err != nil {
			return tools.SkillsPreviewResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
		}
	}
	effective, err := s.resolveMarketplacePolicy(ctx, workspaceID)
	if err != nil {
		return tools.SkillsPreviewResponse{}, err
	}
	request.Source = canonicalSkillInstallSource(request.Source)
	if err := validateSkillInstallSource(request.Source); err != nil {
		return tools.SkillsPreviewResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	sourceDecision := skillmarketplace.BindPolicyDecision(effective.Config.EvaluateSource(request.Source), effective)
	if !sourceDecision.Allowed {
		if err := s.auditMarketplacePolicyEvaluation(ctx, session, request.SessionID, "", "preview", request.Source, "", sourceDecision, nil); err != nil {
			return tools.SkillsPreviewResponse{}, err
		}
		identifier := strings.TrimSpace(request.Identifier)
		if identifier == "" {
			identifier = suggestedPackageIdentifier("", request.Source)
		}
		return tools.SkillsPreviewResponse{
			Identifier: identifier, Source: request.Source, Policy: sourceDecision,
			Assets:       tools.SkillsAssetIndex{Files: []tools.SkillsAssetIndexFile{}},
			InstallState: "blocked", BlockReason: strings.Join(sourceDecision.Violations, "; "),
			Changes: tools.SkillsPreviewChanges{AddedFiles: []string{}, RemovedFiles: []string{}, ChangedFiles: []string{}},
		}, nil
	}
	pkg, err := s.fetchSkillInstallPackage(ctx, session, workspaceID, request.Source)
	if err != nil {
		return tools.SkillsPreviewResponse{}, fmt.Errorf("preview skill package: %w", err)
	}
	if err := skillspkg.ValidateManifest(pkg.Manifest); err != nil {
		return tools.SkillsPreviewResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	packageDecision, securityReport := effective.Config.EvaluatePackageSecurity(pkg.Source, pkg.License, pkg)
	bundle, err := remoteSkillAssetBundle(pkg, securityReport)
	if err != nil {
		return tools.SkillsPreviewResponse{}, fmt.Errorf("preview remote skill assets: %w", err)
	}
	identifier := strings.TrimSpace(request.Identifier)
	if identifier == "" {
		identifier = suggestedPackageIdentifier(pkg.Name, pkg.Source)
	}
	if err := skillspkg.ValidateIdentifier(identifier); err != nil {
		return tools.SkillsPreviewResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	title := strings.TrimSpace(pkg.Name)
	if title == "" {
		title = identifier
	}
	response := tools.SkillsPreviewResponse{
		Identifier: identifier, Title: title, Description: pkg.Description, License: pkg.License,
		Source: pkg.Source, Revision: pkg.Revision, SourceURL: pkg.HTMLURL, ContentBytes: len([]byte(pkg.Content)),
		Assets: skillAssetIndex(bundle), Policy: skillmarketplace.BindPolicyDecision(packageDecision, effective), Security: securityReport, InstallState: "new_install",
		Changes: tools.SkillsPreviewChanges{AddedFiles: assetPaths(bundle), RemovedFiles: []string{}, ChangedFiles: []string{}},
	}
	if err := s.auditMarketplacePolicyEvaluation(ctx, session, request.SessionID, "", "preview", pkg.Source, pkg.Revision, response.Policy, &response.Security); err != nil {
		return tools.SkillsPreviewResponse{}, err
	}
	if !response.Policy.Allowed {
		response.InstallState = "blocked"
		response.BlockReason = strings.Join(response.Policy.Violations, "; ")
		return response, nil
	}

	skill, err := registry.GetSkillByIdentifier(ctx, workspaceID, identifier)
	if errors.Is(err, managedagents.ErrNotFound) {
		return response, nil
	}
	if err != nil {
		return tools.SkillsPreviewResponse{}, err
	}
	response.Existing = &tools.SkillsPreviewExisting{
		SkillID: skill.ID, Status: skill.Status, SourceType: skill.SourceType,
		SourceLocator: skill.SourceLocator, SourcePath: skill.SourcePath,
	}
	if skill.Status != skillspkg.StatusActive {
		response.InstallState = "blocked"
		response.BlockReason = "archived skill cannot be upgraded"
		return response, nil
	}
	publisherViewingOwnCatalogEntry := pkg.Source.Provider == skillmarketplace.CatalogProvider &&
		skill.ID == pkg.Source.CatalogSkillID
	if !publisherViewingOwnCatalogEntry && !installedSkillMatchesPackageSource(skill, pkg.Source) {
		response.InstallState = "blocked"
		response.BlockReason = "package source does not match installed skill provenance"
		return response, nil
	}
	versions, err := registry.ListSkillVersions(ctx, skill.ID)
	if err != nil {
		return tools.SkillsPreviewResponse{}, err
	}
	if len(versions) == 0 {
		response.InstallState = "blocked"
		response.BlockReason = "installed skill has no published versions"
		return response, nil
	}
	current := versions[0]
	response.Existing.Version = current.Version
	response.Existing.SourceRef = current.SourceRef
	response.Existing.SourceRevision = current.SourceRevision
	currentBundle, err := skillspkg.DecodeAssetBundle(current.Assets)
	if err != nil {
		return tools.SkillsPreviewResponse{}, fmt.Errorf("decode installed skill assets: %w", err)
	}
	response.Changes = compareSkillPackage(current, currentBundle, pkg, bundle)
	response.InstallState = "upgrade"
	if publisherViewingOwnCatalogEntry || (!response.Changes.ContentChanged && len(response.Changes.AddedFiles) == 0 && len(response.Changes.RemovedFiles) == 0 && len(response.Changes.ChangedFiles) == 0) {
		response.InstallState = "unchanged"
	}
	return response, nil
}

func (s skillsToolService) ReadAsset(ctx context.Context, request tools.SkillsReadAssetRequest) (tools.SkillsReadAssetResponse, error) {
	registry, err := s.registry()
	if err != nil {
		return tools.SkillsReadAssetResponse{}, err
	}
	_, workspaceID, err := s.scope(ctx, request.SessionID, request.WorkspaceID)
	if err != nil {
		return tools.SkillsReadAssetResponse{}, err
	}
	identifier := strings.TrimSpace(request.Identifier)
	if err := skillspkg.ValidateIdentifier(identifier); err != nil {
		return tools.SkillsReadAssetResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	skill, err := registry.GetSkillByIdentifier(ctx, workspaceID, identifier)
	if err != nil {
		return tools.SkillsReadAssetResponse{}, err
	}
	version, err := resolveRequestedSkillVersion(ctx, registry, skill.ID, request.Version)
	if err != nil {
		return tools.SkillsReadAssetResponse{}, err
	}
	if isSkillMarkdownAssetPath(request.Path) {
		content := []byte(version.ContentText)
		checksum := sha256.Sum256(content)
		return tools.SkillsReadAssetResponse{
			SkillIdentifier: identifier,
			SkillVersion:    version.Version,
			Found:           true,
			File: skillspkg.AssetFile{
				Path:           "SKILL.md",
				Content:        version.ContentText,
				ContentType:    "text/markdown",
				ChecksumSHA256: fmt.Sprintf("%x", checksum),
				Size:           len(content),
				Revision:       version.SourceRevision,
				SourceURL:      version.SourceURL,
			},
		}, nil
	}
	bundle, err := skillspkg.DecodeAssetBundle(version.Assets)
	if err != nil {
		return tools.SkillsReadAssetResponse{}, fmt.Errorf("decode skill assets: %w", err)
	}
	file, ok := skillspkg.FindAsset(bundle, request.Path)
	if !ok {
		return tools.SkillsReadAssetResponse{
			SkillIdentifier: identifier,
			SkillVersion:    version.Version,
			Found:           false,
			RequestedPath:   strings.TrimSpace(request.Path),
			AvailablePaths:  installedSkillAssetPaths(bundle),
		}, nil
	}
	if file.Binary {
		return tools.SkillsReadAssetResponse{}, fmt.Errorf("%w: binary asset %q cannot be returned by skills_read_asset; use its controlled object download", managedagents.ErrForbidden, request.Path)
	}
	return tools.SkillsReadAssetResponse{SkillIdentifier: identifier, SkillVersion: version.Version, Found: true, File: file}, nil
}

func isSkillMarkdownAssetPath(assetPath string) bool {
	_, ok := skillspkg.FindAsset(skillspkg.AssetBundle{
		Files: []skillspkg.AssetFile{{Path: "SKILL.md"}},
	}, assetPath)
	return ok
}

func installedSkillAssetPaths(bundle skillspkg.AssetBundle) []string {
	paths := make([]string, 0, len(bundle.Files)+1)
	paths = append(paths, "SKILL.md")
	for _, file := range bundle.Files {
		if file.Path != "SKILL.md" {
			paths = append(paths, file.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

func (s skillsToolService) Install(ctx context.Context, request tools.SkillsInstallRequest) (tools.SkillsInstallResponse, error) {
	registry, err := s.registry()
	if err != nil {
		return tools.SkillsInstallResponse{}, err
	}
	session, workspaceID, err := s.scope(ctx, request.SessionID, request.WorkspaceID)
	if err != nil {
		return tools.SkillsInstallResponse{}, err
	}
	uploadedAssets := make([]uploadedSkillAsset, 0)
	installCommitted := false
	defer func() {
		if !installCommitted {
			s.cleanupUploadedSkillAssets(ctx, uploadedAssets)
		}
	}()
	var remotePackage *skillmarketplace.Package
	var remotePolicy *skillmarketplace.PolicyDecision
	var remoteSecurity *skillmarketplace.PackageSecurityReport
	if request.Source != nil {
		if strings.TrimSpace(request.ContentText) != "" || len(request.Manifest) > 0 || len(request.Assets) > 0 || strings.TrimSpace(request.ContentFormat) != "" {
			return tools.SkillsInstallResponse{}, fmt.Errorf("%w: sourced skill install cannot include inline content fields", managedagents.ErrInvalid)
		}
		canonicalSource := canonicalSkillInstallSource(*request.Source)
		if err := validateSkillInstallSource(canonicalSource); err != nil {
			return tools.SkillsInstallResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
		}
		request.Source = &canonicalSource
		effective, resolveErr := s.resolveMarketplacePolicy(ctx, workspaceID)
		if resolveErr != nil {
			return tools.SkillsInstallResponse{}, resolveErr
		}
		if err := validatePinnedMarketplacePolicy(request, effective); err != nil {
			decision := skillmarketplace.BindPolicyDecision(skillmarketplace.PolicyDecision{
				Allowed: false, Checks: []skillmarketplace.PolicyCheck{}, Violations: []string{err.Error()},
			}, effective)
			if auditErr := s.auditMarketplacePolicyEvaluation(ctx, session, request.SessionID, request.TurnID, "install", *request.Source, "", decision, nil); auditErr != nil {
				return tools.SkillsInstallResponse{}, auditErr
			}
			return tools.SkillsInstallResponse{}, err
		}
		sourceDecision := skillmarketplace.BindPolicyDecision(effective.Config.EvaluateSource(*request.Source), effective)
		if !sourceDecision.Allowed {
			if err := s.auditMarketplacePolicyEvaluation(ctx, session, request.SessionID, request.TurnID, "install", *request.Source, "", sourceDecision, nil); err != nil {
				return tools.SkillsInstallResponse{}, err
			}
			return tools.SkillsInstallResponse{}, fmt.Errorf("%w: skill marketplace policy blocked install: %s", managedagents.ErrForbidden, strings.Join(sourceDecision.Violations, "; "))
		}
		pkg, fetchErr := s.fetchSkillInstallPackage(ctx, session, workspaceID, *request.Source)
		if fetchErr != nil {
			return tools.SkillsInstallResponse{}, fmt.Errorf("fetch skill package: %w", fetchErr)
		}
		if err := skillspkg.ValidateManifest(pkg.Manifest); err != nil {
			return tools.SkillsInstallResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
		}
		evaluatedDecision, securityReport := effective.Config.EvaluatePackageSecurity(pkg.Source, pkg.License, pkg)
		packageDecision := skillmarketplace.BindPolicyDecision(evaluatedDecision, effective)
		var externalScanErr error
		if packageDecision.Allowed && s.binaryScanner != nil && len(securityReport.BinaryFiles) > 0 {
			securityReport, externalScanErr = skillmarketplace.ApplyExternalBinaryScanner(ctx, pkg, securityReport, s.binaryScanner)
			evaluatedDecision = skillmarketplace.UpdateBinaryScanPolicyDecision(evaluatedDecision, securityReport)
			packageDecision = skillmarketplace.BindPolicyDecision(evaluatedDecision, effective)
			if err := s.auditExternalBinaryScan(ctx, session, request.SessionID, request.TurnID, pkg, securityReport, externalScanErr); err != nil {
				return tools.SkillsInstallResponse{}, err
			}
		}
		if err := s.auditMarketplacePolicyEvaluation(ctx, session, request.SessionID, request.TurnID, "install", pkg.Source, pkg.Revision, packageDecision, &securityReport); err != nil {
			return tools.SkillsInstallResponse{}, err
		}
		if externalScanErr != nil {
			return tools.SkillsInstallResponse{}, fmt.Errorf("external binary scan: %w", externalScanErr)
		}
		if !packageDecision.Allowed {
			return tools.SkillsInstallResponse{}, fmt.Errorf("%w: skill marketplace policy blocked install: %s", managedagents.ErrForbidden, strings.Join(packageDecision.Violations, "; "))
		}
		remotePolicy = &packageDecision
		remoteSecurity = &securityReport
		remotePackage = &pkg
		request.Source = &pkg.Source
		request.ContentFormat = "markdown"
		request.ContentText = pkg.Content
		request.Manifest = append(json.RawMessage(nil), pkg.Manifest...)
		if len(request.Manifest) == 0 {
			request.Manifest = json.RawMessage(`{}`)
		}
		bundle, bundleErr := remoteSkillAssetBundle(pkg, securityReport)
		if bundleErr != nil {
			return tools.SkillsInstallResponse{}, fmt.Errorf("encode remote skill assets: %w", bundleErr)
		}
		assetIdentifier := strings.TrimSpace(request.Identifier)
		if assetIdentifier == "" {
			assetIdentifier = suggestedPackageIdentifier(pkg.Name, pkg.Source)
		}
		bundle, uploads, persistErr := s.persistBinarySkillAssets(ctx, workspaceID, assetIdentifier, pkg, bundle)
		uploadedAssets = append(uploadedAssets, uploads...)
		if persistErr != nil {
			return tools.SkillsInstallResponse{}, fmt.Errorf("persist binary skill assets: %w", persistErr)
		}
		encodedAssets, encodeErr := skillspkg.EncodeAssetBundle(bundle)
		if encodeErr != nil {
			return tools.SkillsInstallResponse{}, fmt.Errorf("encode remote skill assets: %w", encodeErr)
		}
		request.Assets = encodedAssets
		if strings.TrimSpace(request.Identifier) == "" {
			request.Identifier = suggestedPackageIdentifier(pkg.Name, pkg.Source)
		}
		if strings.TrimSpace(request.Title) == "" {
			request.Title = strings.TrimSpace(pkg.Name)
			if request.Title == "" {
				request.Title = request.Identifier
			}
		}
		if strings.TrimSpace(request.Description) == "" {
			request.Description = pkg.Description
		}
	}
	if err := validateSkillToolPackage(request); err != nil {
		return tools.SkillsInstallResponse{}, err
	}
	identifier := strings.TrimSpace(request.Identifier)
	createdBy := fmt.Sprintf("skills.install:%s:%s", request.SessionID, request.TurnID)
	skill, getErr := registry.GetSkillByIdentifier(ctx, workspaceID, identifier)
	upgraded := false
	switch {
	case getErr == nil:
		if skill.Status != skillspkg.StatusActive {
			return tools.SkillsInstallResponse{}, fmt.Errorf("%w: archived skill %q cannot be upgraded", managedagents.ErrConflict, identifier)
		}
		if !request.UpgradeExisting {
			return tools.SkillsInstallResponse{}, fmt.Errorf("%w: skill %q is already installed; set upgrade_existing to publish a new version", managedagents.ErrConflict, identifier)
		}
		if remotePackage != nil {
			if !installedSkillMatchesPackageSource(skill, remotePackage.Source) {
				return tools.SkillsInstallResponse{}, fmt.Errorf("%w: package upgrade source does not match the installed skill provenance", managedagents.ErrConflict)
			}
		} else if skill.SourceType == skillspkg.SourceTypeGitHub || skill.SourceType == skillspkg.SourceTypeArtifact {
			return tools.SkillsInstallResponse{}, fmt.Errorf("%w: sourced skill versions must be upgraded from their package source", managedagents.ErrConflict)
		}
		upgraded = true
	case errors.Is(getErr, managedagents.ErrNotFound):
		sourceType := skillspkg.SourceTypeInline
		sourceLocator := ""
		sourcePath := ""
		if remotePackage != nil {
			sourceType, sourceLocator, sourcePath = packageSkillProvenance(remotePackage.Source)
		}
		skill, err = registry.CreateSkill(ctx, skillspkg.CreateSkillInput{
			WorkspaceID: workspaceID, Identifier: identifier, Title: strings.TrimSpace(request.Title),
			Description: strings.TrimSpace(request.Description), OwnerType: skillspkg.OwnerTypeWorkspace,
			SourceType: sourceType, SourceLocator: sourceLocator, SourcePath: sourcePath, CreatedBy: createdBy,
		})
		if err != nil {
			return tools.SkillsInstallResponse{}, err
		}
	default:
		return tools.SkillsInstallResponse{}, getErr
	}
	versionInput := skillspkg.CreateVersionInput{
		SkillID: skill.ID, ContentFormat: strings.TrimSpace(request.ContentFormat), Manifest: request.Manifest,
		ContentText: request.ContentText, Assets: request.Assets, CreatedBy: createdBy,
	}
	if remotePackage != nil {
		versionInput.SourceRef = packageVersionSourceRef(remotePackage.Source)
		versionInput.SourceRevision = remotePackage.Revision
		versionInput.SourceURL = remotePackage.HTMLURL
	}
	version, err := registry.CreateSkillVersion(ctx, versionInput)
	if err != nil {
		return tools.SkillsInstallResponse{}, err
	}
	installCommitted = true
	return tools.SkillsInstallResponse{Skill: skill, Version: skillVersionForTool(version), Upgraded: upgraded, Policy: remotePolicy, Security: remoteSecurity}, nil
}

func skillMarketplacePolicyFromEnv() skillmarketplace.Policy {
	return skillmarketplace.Policy{
		AllowedOwners:           splitSkillPolicyCSV(os.Getenv("TMA_SKILLS_GITHUB_ALLOWED_OWNERS")),
		AllowedRepositories:     splitSkillPolicyCSV(os.Getenv("TMA_SKILLS_GITHUB_ALLOWED_REPOSITORIES")),
		RequireCommitSHA:        skillPolicyEnvBool("TMA_SKILLS_GITHUB_REQUIRE_COMMIT_SHA"),
		AllowedLicenses:         splitSkillPolicyCSV(os.Getenv("TMA_SKILLS_ALLOWED_LICENSES")),
		DeniedLicenses:          splitSkillPolicyCSV(os.Getenv("TMA_SKILLS_DENIED_LICENSES")),
		RequireLicense:          skillPolicyEnvBool("TMA_SKILLS_REQUIRE_LICENSE"),
		RequireAttestation:      skillPolicyEnvBool("TMA_SKILLS_REQUIRE_ATTESTATION"),
		TrustedAttestationKeys:  skillPolicyEnvJSONMap("TMA_SKILLS_TRUSTED_ATTESTATION_KEYS"),
		StaticScanBlockSeverity: strings.TrimSpace(os.Getenv("TMA_SKILLS_STATIC_SCAN_BLOCK_SEVERITY")),
	}
}

func canonicalSkillInstallSource(source skillmarketplace.Source) skillmarketplace.Source {
	source.Provider = strings.ToLower(strings.TrimSpace(source.Provider))
	if source.Provider == "" {
		source.Provider = skillmarketplace.GitHubProvider
	}
	source.Repository = strings.TrimSpace(source.Repository)
	source.Ref = strings.TrimSpace(source.Ref)
	source.Path = strings.TrimSpace(source.Path)
	source.ArtifactID = strings.TrimSpace(source.ArtifactID)
	source.CatalogEntryID = strings.TrimSpace(source.CatalogEntryID)
	source.CatalogSkillID = ""
	switch source.Provider {
	case skillmarketplace.ArtifactProvider:
		source.Repository = ""
		source.Ref = ""
		source.Path = "SKILL.md"
		source.CatalogEntryID = ""
	case skillmarketplace.CatalogProvider:
		source.Repository = ""
		source.Ref = ""
		source.Path = "SKILL.md"
		source.ArtifactID = ""
	default:
		source.ArtifactID = ""
		source.CatalogEntryID = ""
	}
	return source
}

func validateSkillInstallSource(source skillmarketplace.Source) error {
	switch source.Provider {
	case skillmarketplace.GitHubProvider:
		if source.Repository == "" {
			return fmt.Errorf("github skill source requires repository")
		}
	case skillmarketplace.ArtifactProvider:
		if source.ArtifactID == "" {
			return fmt.Errorf("artifact skill source requires artifact_id")
		}
	case skillmarketplace.CatalogProvider:
		if source.CatalogEntryID == "" {
			return fmt.Errorf("catalog skill source requires catalog_entry_id")
		}
	default:
		return fmt.Errorf("unsupported skill source provider %q", source.Provider)
	}
	return nil
}

func (s skillsToolService) fetchSkillInstallPackage(ctx context.Context, session managedagents.Session, workspaceID string, source skillmarketplace.Source) (skillmarketplace.Package, error) {
	switch source.Provider {
	case skillmarketplace.GitHubProvider:
		if s.marketplace == nil {
			return skillmarketplace.Package{}, errors.New("skills marketplace is not configured")
		}
		return s.marketplace.Fetch(ctx, source)
	case skillmarketplace.ArtifactProvider:
		artifact, err := managedagents.GetSessionArtifactWithContext(ctx, s.store, session.ID, source.ArtifactID)
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		if artifact.WorkspaceID != workspaceID || artifact.SessionID != session.ID {
			return skillmarketplace.Package{}, fmt.Errorf("%w: artifact skill package belongs to another session or workspace", managedagents.ErrForbidden)
		}
		if artifact.ArtifactType != managedagents.ArtifactTypeFile {
			return skillmarketplace.Package{}, fmt.Errorf("%w: artifact skill package must be a file", managedagents.ErrInvalid)
		}
		if !strings.EqualFold(path.Ext(strings.TrimSpace(artifact.Name)), ".zip") {
			return skillmarketplace.Package{}, fmt.Errorf("%w: artifact skill package must be a .zip file", managedagents.ErrInvalid)
		}
		objectRef, err := managedagents.GetObjectRefWithContext(ctx, s.store, artifact.ObjectRefID)
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		if objectRef.WorkspaceID != workspaceID || objectRef.SizeBytes <= 0 || objectRef.SizeBytes > skillmarketplace.MaxArtifactPackageArchiveBytes {
			return skillmarketplace.Package{}, fmt.Errorf("%w: artifact skill package object is outside workspace or size limits", managedagents.ErrInvalid)
		}
		object, err := s.objectStore.GetObject(ctx, objectstore.GetObjectInput{
			Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion,
		})
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		defer object.Body.Close()
		content, err := io.ReadAll(io.LimitReader(object.Body, int64(skillmarketplace.MaxArtifactPackageArchiveBytes)+1))
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		if len(content) == 0 || len(content) > skillmarketplace.MaxArtifactPackageArchiveBytes {
			return skillmarketplace.Package{}, fmt.Errorf("%w: artifact skill package ZIP exceeds size limit", managedagents.ErrInvalid)
		}
		if int64(len(content)) != objectRef.SizeBytes {
			return skillmarketplace.Package{}, fmt.Errorf("%w: artifact skill package object size mismatch", managedagents.ErrInvalid)
		}
		pkg, err := skillmarketplace.ParseArtifactPackage(content, artifact.ID, artifact.Name)
		if err != nil {
			return skillmarketplace.Package{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
		}
		if objectRef.ChecksumSHA256 != "" && !strings.EqualFold(objectRef.ChecksumSHA256, pkg.Revision) {
			return skillmarketplace.Package{}, fmt.Errorf("%w: artifact skill package checksum mismatch", managedagents.ErrInvalid)
		}
		return pkg, nil
	case skillmarketplace.CatalogProvider:
		catalog, ok := s.store.(skillmarketplace.MarketplaceCatalogStore)
		if !ok {
			return skillmarketplace.Package{}, errors.New("internal marketplace catalog is unavailable")
		}
		entry, err := catalog.GetPublishedMarketplaceEntry(ctx, workspaceID, source.CatalogEntryID)
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		publisherCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{WorkspaceID: entry.WorkspaceID})
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		registry, err := s.registry()
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		version, err := registry.GetSkillVersion(publisherCtx, entry.SkillID, entry.SkillVersion)
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		if version.Checksum != entry.VersionChecksum || version.PackageFormat != skillpackage.FormatV1 || version.PackageObjectRefID == "" {
			return skillmarketplace.Package{}, fmt.Errorf("%w: internal marketplace entry does not reference a complete immutable package", managedagents.ErrConflict)
		}
		objectRef, err := managedagents.GetObjectRefWithContext(publisherCtx, s.store, version.PackageObjectRefID)
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		if objectRef.WorkspaceID != entry.WorkspaceID || objectRef.SizeBytes <= 0 || objectRef.SizeBytes > skillmarketplace.MaxArtifactPackageArchiveBytes {
			return skillmarketplace.Package{}, fmt.Errorf("%w: internal marketplace package object is outside publisher workspace or size limits", managedagents.ErrInvalid)
		}
		object, err := s.objectStore.GetObject(ctx, objectstore.GetObjectInput{
			Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion,
		})
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		defer object.Body.Close()
		content, err := io.ReadAll(io.LimitReader(object.Body, int64(skillmarketplace.MaxArtifactPackageArchiveBytes)+1))
		if err != nil {
			return skillmarketplace.Package{}, err
		}
		if len(content) == 0 || len(content) > skillmarketplace.MaxArtifactPackageArchiveBytes || int64(len(content)) != objectRef.SizeBytes {
			return skillmarketplace.Package{}, fmt.Errorf("%w: internal marketplace package object size mismatch", managedagents.ErrInvalid)
		}
		pkg, err := skillmarketplace.ParseArtifactPackage(content, entry.ID, entry.SkillIdentifier+".zip")
		if err != nil {
			return skillmarketplace.Package{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
		}
		if objectRef.ChecksumSHA256 != "" && !strings.EqualFold(objectRef.ChecksumSHA256, pkg.Revision) {
			return skillmarketplace.Package{}, fmt.Errorf("%w: internal marketplace package checksum mismatch", managedagents.ErrInvalid)
		}
		pkg.Source = skillmarketplace.Source{
			Provider: skillmarketplace.CatalogProvider, CatalogEntryID: entry.ID,
			CatalogSkillID: entry.SkillID, Path: "SKILL.md",
		}
		pkg.Name = entry.SkillTitle
		pkg.Description = entry.Summary
		if pkg.Description == "" {
			pkg.Description = entry.SkillDescription
		}
		pkg.HTMLURL = ""
		return pkg, nil
	default:
		return skillmarketplace.Package{}, fmt.Errorf("%w: unsupported skill source provider %q", managedagents.ErrInvalid, source.Provider)
	}
}

func suggestedPackageIdentifier(name string, source skillmarketplace.Source) string {
	fallback := source.Repository
	if source.Provider == skillmarketplace.ArtifactProvider {
		fallback = source.ArtifactID
	} else if source.Provider == skillmarketplace.CatalogProvider {
		fallback = source.CatalogEntryID
	}
	return skillmarketplace.SuggestedIdentifier(name, fallback)
}

func packageSkillProvenance(source skillmarketplace.Source) (string, string, string) {
	if source.Provider == skillmarketplace.ArtifactProvider {
		return skillspkg.SourceTypeArtifact, "session-artifact", "SKILL.md"
	}
	if source.Provider == skillmarketplace.CatalogProvider {
		return skillspkg.SourceTypeCatalog, source.CatalogSkillID, "SKILL.md"
	}
	return skillspkg.SourceTypeGitHub, source.Repository, source.Path
}

func installedSkillMatchesPackageSource(skill skillspkg.Skill, source skillmarketplace.Source) bool {
	sourceType, locator, sourcePath := packageSkillProvenance(source)
	return skill.SourceType == sourceType && skill.SourceLocator == locator && skill.SourcePath == sourcePath
}

func packageVersionSourceRef(source skillmarketplace.Source) string {
	if source.Provider == skillmarketplace.ArtifactProvider {
		return source.ArtifactID
	}
	if source.Provider == skillmarketplace.CatalogProvider {
		return source.CatalogEntryID
	}
	return source.Ref
}

func skillSourceResourceID(source skillmarketplace.Source) string {
	if source.Provider == skillmarketplace.ArtifactProvider {
		return source.ArtifactID
	}
	if source.Provider == skillmarketplace.CatalogProvider {
		return source.CatalogEntryID
	}
	return source.Repository
}

func (s skillsToolService) resolveMarketplacePolicy(ctx context.Context, workspaceID string) (skillmarketplace.EffectivePolicy, error) {
	if store, ok := s.store.(skillmarketplace.PolicyStore); ok {
		effective, err := store.ResolveMarketplacePolicy(ctx, workspaceID)
		if err == nil {
			return effective, nil
		}
		if !errors.Is(err, managedagents.ErrNotFound) {
			return skillmarketplace.EffectivePolicy{}, err
		}
	}
	normalized, err := skillmarketplace.NormalizePolicy(s.policy)
	if err != nil {
		return skillmarketplace.EffectivePolicy{}, fmt.Errorf("invalid server marketplace policy: %w", err)
	}
	revision, err := skillmarketplace.PolicyRevision(normalized)
	if err != nil {
		return skillmarketplace.EffectivePolicy{}, err
	}
	return skillmarketplace.EffectivePolicy{
		Source: skillmarketplace.PolicySourceServer, Config: normalized, Revision: revision,
	}, nil
}

func validatePinnedMarketplacePolicy(request tools.SkillsInstallRequest, effective skillmarketplace.EffectivePolicy) error {
	request.PolicyID = strings.TrimSpace(request.PolicyID)
	request.PolicyRevision = strings.TrimSpace(request.PolicyRevision)
	if effective.Policy.ID != "" {
		if request.PolicyID == "" || request.PolicyVersion <= 0 || request.PolicyRevision == "" {
			return fmt.Errorf("%w: preview is required before install under a persisted marketplace policy", managedagents.ErrConflict)
		}
		if request.PolicyID != effective.Policy.ID || request.PolicyVersion != effective.Version.Version || request.PolicyRevision != effective.Revision {
			return fmt.Errorf("%w: marketplace policy changed; preview the package again before install", managedagents.ErrConflict)
		}
		return nil
	}
	if request.PolicyRevision != "" && request.PolicyRevision != effective.Revision {
		return fmt.Errorf("%w: server marketplace policy changed; preview the package again before install", managedagents.ErrConflict)
	}
	return nil
}

func (s skillsToolService) auditMarketplacePolicyEvaluation(ctx context.Context, session managedagents.Session, sessionID string, turnID string, operation string, source skillmarketplace.Source, packageRevision string, decision skillmarketplace.PolicyDecision, security *skillmarketplace.PackageSecurityReport) error {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return nil
	}
	details, err := json.Marshal(map[string]any{
		"turn_id": turnID, "operation": operation, "source": source,
		"package_revision": packageRevision, "policy": decision, "security": security,
	})
	if err != nil {
		return err
	}
	outcome := "succeeded"
	errorMessage := ""
	if !decision.Allowed {
		outcome = "failed"
		errorMessage = strings.Join(decision.Violations, "; ")
	}
	principalID := strings.TrimSpace(session.OwnerID)
	if principalID == "" {
		principalID = "system"
	}
	_, err = managedagents.RecordOperatorAuditWithContext(ctx, store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: session.WorkspaceID, SessionID: sessionID, PrincipalID: principalID, Role: "agent",
		Action: "skills.marketplace.policy_evaluate", ResourceType: "skill_marketplace_source",
		ResourceID: skillSourceResourceID(source), Outcome: outcome, ErrorMessage: errorMessage, Details: details,
	})
	return err
}

func (s skillsToolService) auditExternalBinaryScan(ctx context.Context, session managedagents.Session, sessionID string, turnID string, pkg skillmarketplace.Package, report skillmarketplace.PackageSecurityReport, scanErr error) error {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return nil
	}
	outcome := "succeeded"
	errorMessage := ""
	if scanErr != nil {
		outcome = "failed"
		errorMessage = "external binary scanner did not return a trusted verdict"
	} else {
		for _, file := range report.BinaryFiles {
			if file.ExternalScan != nil && file.ExternalScan.Status != skillmarketplace.BinaryScanPassed {
				outcome = "failed"
				errorMessage = "external binary scanner blocked one or more assets"
				break
			}
		}
	}
	details, err := json.Marshal(map[string]any{
		"turn_id": turnID, "operation": "install", "source": pkg.Source,
		"package_revision": pkg.Revision, "binary_files": report.BinaryFiles,
	})
	if err != nil {
		return err
	}
	principalID := strings.TrimSpace(session.OwnerID)
	if principalID == "" {
		principalID = "system"
	}
	_, err = managedagents.RecordOperatorAuditWithContext(ctx, store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: session.WorkspaceID, SessionID: sessionID, PrincipalID: principalID, Role: "agent",
		Action: "skills.binary_scan", ResourceType: "skill_marketplace_package",
		ResourceID: skillSourceResourceID(pkg.Source) + "@" + pkg.Revision,
		Outcome:    outcome, ErrorMessage: errorMessage, Details: details,
	})
	return err
}

func skillPolicyEnvJSONMap(key string) map[string]string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	var result map[string]string
	if json.Unmarshal([]byte(value), &result) != nil {
		return map[string]string{"invalid": value}
	}
	return result
}

func splitSkillPolicyCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func skillPolicyEnvBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s skillsToolService) Enable(ctx context.Context, request tools.SkillsEnableRequest) (tools.SkillsEnableResponse, error) {
	registry, err := s.registry()
	if err != nil {
		return tools.SkillsEnableResponse{}, err
	}
	session, workspaceID, err := s.scope(ctx, request.SessionID, request.WorkspaceID)
	if err != nil {
		return tools.SkillsEnableResponse{}, err
	}
	agentCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{
		WorkspaceID: workspaceID,
		OwnerID:     session.OwnerID,
	})
	if err != nil {
		return tools.SkillsEnableResponse{}, err
	}
	identifier := strings.TrimSpace(request.Identifier)
	if err := skillspkg.ValidateIdentifier(identifier); err != nil {
		return tools.SkillsEnableResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	skill, err := registry.GetSkillByIdentifier(ctx, workspaceID, identifier)
	if err != nil {
		return tools.SkillsEnableResponse{}, err
	}
	if skill.Status != skillspkg.StatusActive {
		return tools.SkillsEnableResponse{}, fmt.Errorf("%w: skill %q is archived", managedagents.ErrInvalid, identifier)
	}
	version, err := resolveRequestedSkillVersion(ctx, registry, skill.ID, request.Version)
	if err != nil {
		return tools.SkillsEnableResponse{}, err
	}
	if err := skillspkg.ValidateVersionInputs(version, request.Inputs); err != nil {
		return tools.SkillsEnableResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	agent, err := managedagents.GetAgentWithContext(agentCtx, s.store, session.AgentID)
	if err != nil {
		return tools.SkillsEnableResponse{}, err
	}
	if agent.WorkspaceID != workspaceID {
		return tools.SkillsEnableResponse{}, fmt.Errorf("%w: cross-workspace Agent access is not allowed", managedagents.ErrInvalid)
	}
	config, err := editableSkillConfig(agent.ConfigVersion.Skills)
	if err != nil {
		return tools.SkillsEnableResponse{}, err
	}
	binding := skillspkg.EnabledSkill{
		Skill: identifier, Version: version.Version, Mode: strings.TrimSpace(request.Mode),
		Priority: request.Priority, Inputs: append(json.RawMessage(nil), request.Inputs...),
	}
	if binding.Mode == "" {
		binding.Mode = skillspkg.DefaultMode
	}
	if binding.Priority == 0 {
		binding.Priority = skillspkg.DefaultPriority
	}
	replaced := false
	unchanged := false
	for index := range config.Enabled {
		if config.Enabled[index].Skill != identifier {
			continue
		}
		unchanged = skillBindingsEqual(config.Enabled[index], binding)
		config.Enabled[index] = binding
		replaced = true
		break
	}
	if !replaced {
		config.Enabled = append(config.Enabled, binding)
	}
	normalizedRaw, err := normalizeSkillConfigJSON(config)
	if err != nil {
		return tools.SkillsEnableResponse{}, err
	}
	previousVersion := agent.CurrentConfigVersion
	if !unchanged {
		agent, err = managedagents.CreateAgentConfigVersionWithContext(agentCtx, s.store, managedagents.CreateAgentConfigVersionInput{
			AgentID: agent.ID, ExpectedCurrentVersion: previousVersion,
			LLMProvider: agent.ConfigVersion.LLMProvider, LLMModel: agent.ConfigVersion.LLMModel,
			System: agent.ConfigVersion.System, Tools: agent.ConfigVersion.Tools, MCP: agent.ConfigVersion.MCP, Skills: normalizedRaw,
		})
		if err != nil {
			return tools.SkillsEnableResponse{}, err
		}
	}
	requiresSessionUpgrade, err := requiresManualSessionConfigUpgrade(session, agent.CurrentConfigVersion)
	if err != nil {
		return tools.SkillsEnableResponse{}, err
	}
	return tools.SkillsEnableResponse{
		AgentID: agent.ID, PreviousConfigVersion: previousVersion, NewConfigVersion: agent.CurrentConfigVersion,
		CurrentSessionVersion: session.AgentConfigVersion, Binding: binding, Changed: !unchanged,
		RequiresSessionUpgrade: requiresSessionUpgrade,
	}, nil
}

func (s skillsToolService) Disable(ctx context.Context, request tools.SkillsDisableRequest) (tools.SkillsDisableResponse, error) {
	registry, err := s.registry()
	if err != nil {
		return tools.SkillsDisableResponse{}, err
	}
	session, workspaceID, err := s.scope(ctx, request.SessionID, request.WorkspaceID)
	if err != nil {
		return tools.SkillsDisableResponse{}, err
	}
	agentCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{
		WorkspaceID: workspaceID,
		OwnerID:     session.OwnerID,
	})
	if err != nil {
		return tools.SkillsDisableResponse{}, err
	}
	identifier := strings.TrimSpace(request.Identifier)
	if err := skillspkg.ValidateIdentifier(identifier); err != nil {
		return tools.SkillsDisableResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	if _, err := registry.GetSkillByIdentifier(ctx, workspaceID, identifier); err != nil {
		return tools.SkillsDisableResponse{}, err
	}
	agent, err := managedagents.GetAgentWithContext(agentCtx, s.store, session.AgentID)
	if err != nil {
		return tools.SkillsDisableResponse{}, err
	}
	if agent.WorkspaceID != workspaceID {
		return tools.SkillsDisableResponse{}, fmt.Errorf("%w: cross-workspace Agent access is not allowed", managedagents.ErrInvalid)
	}
	config, err := editableSkillConfig(agent.ConfigVersion.Skills)
	if err != nil {
		return tools.SkillsDisableResponse{}, err
	}
	binding := skillspkg.EnabledSkill{Skill: identifier}
	remaining := make([]skillspkg.EnabledSkill, 0, len(config.Enabled))
	removed := false
	for _, enabled := range config.Enabled {
		if enabled.Skill == identifier {
			binding = enabled
			removed = true
			continue
		}
		remaining = append(remaining, enabled)
	}
	previousVersion := agent.CurrentConfigVersion
	if removed {
		config.Enabled = remaining
		normalizedRaw, normalizeErr := normalizeSkillConfigJSON(config)
		if normalizeErr != nil {
			return tools.SkillsDisableResponse{}, normalizeErr
		}
		agent, err = managedagents.CreateAgentConfigVersionWithContext(agentCtx, s.store, managedagents.CreateAgentConfigVersionInput{
			AgentID: agent.ID, ExpectedCurrentVersion: previousVersion,
			LLMProvider: agent.ConfigVersion.LLMProvider, LLMModel: agent.ConfigVersion.LLMModel,
			System: agent.ConfigVersion.System, Tools: agent.ConfigVersion.Tools, MCP: agent.ConfigVersion.MCP, Skills: normalizedRaw,
		})
		if err != nil {
			return tools.SkillsDisableResponse{}, err
		}
	}
	requiresSessionUpgrade, err := requiresManualSessionConfigUpgrade(session, agent.CurrentConfigVersion)
	if err != nil {
		return tools.SkillsDisableResponse{}, err
	}
	return tools.SkillsDisableResponse{
		AgentID: agent.ID, PreviousConfigVersion: previousVersion, NewConfigVersion: agent.CurrentConfigVersion,
		CurrentSessionVersion: session.AgentConfigVersion, Binding: binding, Removed: removed,
		RequiresSessionUpgrade: requiresSessionUpgrade,
	}, nil
}

func requiresManualSessionConfigUpgrade(session managedagents.Session, targetVersion int) (bool, error) {
	if session.AgentConfigVersion >= targetVersion {
		return false, nil
	}
	policy, err := managedagents.AgentConfigUpdatePolicy(session.RuntimeSettings)
	if err != nil {
		return false, err
	}
	return policy == managedagents.AgentConfigUpdatePinned, nil
}

func (s skillsToolService) registry() (skillspkg.Registry, error) {
	registry, ok := s.store.(skillspkg.Registry)
	if !ok {
		return nil, fmt.Errorf("skills registry is unavailable")
	}
	return registry, nil
}

func (s skillsToolService) scope(ctx context.Context, sessionID string, requestedWorkspaceID string) (managedagents.Session, string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return managedagents.Session{}, "", fmt.Errorf("%w: session context is required", managedagents.ErrInvalid)
	}
	session, err := managedagents.GetSessionWithContext(ctx, s.store, sessionID)
	if err != nil {
		return managedagents.Session{}, "", err
	}
	requestedWorkspaceID = strings.TrimSpace(requestedWorkspaceID)
	if requestedWorkspaceID != "" && requestedWorkspaceID != session.WorkspaceID {
		return managedagents.Session{}, "", fmt.Errorf("%w: cross-workspace skills access is not allowed", managedagents.ErrInvalid)
	}
	return session, session.WorkspaceID, nil
}

func resolveRequestedSkillVersion(ctx context.Context, registry skillspkg.Registry, skillID string, requested int) (skillspkg.Version, error) {
	if requested > 0 {
		return registry.GetSkillVersion(ctx, skillID, requested)
	}
	versions, err := registry.ListSkillVersions(ctx, skillID)
	if err != nil {
		return skillspkg.Version{}, err
	}
	if len(versions) == 0 {
		return skillspkg.Version{}, fmt.Errorf("%w: skill has no published versions", managedagents.ErrNotFound)
	}
	return versions[0], nil
}

func validateSkillToolPackage(request tools.SkillsInstallRequest) error {
	if err := skillspkg.ValidateIdentifier(strings.TrimSpace(request.Identifier)); err != nil {
		return fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	title := strings.TrimSpace(request.Title)
	if title == "" || len([]rune(title)) > maxSkillToolTitleChars {
		return fmt.Errorf("%w: skill title is required and must not exceed %d characters", managedagents.ErrInvalid, maxSkillToolTitleChars)
	}
	if len([]rune(request.Description)) > maxSkillToolDescriptionChars {
		return fmt.Errorf("%w: skill description must not exceed %d characters", managedagents.ErrInvalid, maxSkillToolDescriptionChars)
	}
	if len([]rune(request.ContentText)) > maxSkillToolContentChars {
		return fmt.Errorf("%w: skill content must not exceed %d characters", managedagents.ErrInvalid, maxSkillToolContentChars)
	}
	if len(request.Manifest) > maxSkillToolJSONBytes || len(request.Assets) > maxSkillToolJSONBytes {
		return fmt.Errorf("%w: skill manifest and assets must not exceed %d bytes", managedagents.ErrInvalid, maxSkillToolJSONBytes)
	}
	if err := skillspkg.ValidateManifest(request.Manifest); err != nil {
		return fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	if len(request.Assets) > 0 && string(request.Assets) != "null" && !json.Valid(request.Assets) {
		return fmt.Errorf("%w: invalid skill assets", managedagents.ErrInvalid)
	}
	switch strings.TrimSpace(request.ContentFormat) {
	case "", "markdown", "json", "hybrid":
	default:
		return fmt.Errorf("%w: unsupported skill content_format", managedagents.ErrInvalid)
	}
	return nil
}

func editableSkillConfig(raw json.RawMessage) (skillspkg.Config, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return skillspkg.Config{Enabled: []skillspkg.EnabledSkill{}}, nil
	}
	config, err := skillspkg.ValidateConfig(raw)
	if err != nil {
		return skillspkg.Config{}, fmt.Errorf("%w: current Agent uses a legacy skills config; migrate it before changing skills through tools", managedagents.ErrInvalid)
	}
	return config, nil
}

func normalizeSkillConfigJSON(config skillspkg.Config) (json.RawMessage, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	normalized, err := skillspkg.ValidateConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	return json.Marshal(normalized)
}

func skillBindingsEqual(left skillspkg.EnabledSkill, right skillspkg.EnabledSkill) bool {
	return left.Skill == right.Skill && left.Version == right.Version && left.Mode == right.Mode &&
		left.Priority == right.Priority && rawJSONEqual(left.Inputs, right.Inputs)
}

func rawJSONEqual(left json.RawMessage, right json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, error) {
		if len(raw) == 0 || string(raw) == "null" {
			return map[string]any{}, nil
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
	leftValue, leftErr := decode(left)
	rightValue, rightErr := decode(right)
	if leftErr != nil || rightErr != nil {
		return bytes.Equal(left, right)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func skillVersionForTool(version skillspkg.Version) skillspkg.Version {
	bundle, err := skillspkg.DecodeAssetBundle(version.Assets)
	if err != nil || len(bundle.Files) == 0 {
		return version
	}
	index := skillAssetIndex(bundle)
	if encoded, encodeErr := json.Marshal(index); encodeErr == nil {
		version.Assets = encoded
	}
	return version
}

func remoteSkillAssetBundle(pkg skillmarketplace.Package, security skillmarketplace.PackageSecurityReport) (skillspkg.AssetBundle, error) {
	bundle := skillspkg.AssetBundle{
		Files: make([]skillspkg.AssetFile, 0, len(pkg.Files)), Warnings: append([]string(nil), pkg.Warnings...),
		SBOM: skillAssetSBOM(security.SBOM),
	}
	scanResults := make(map[string]skillmarketplace.BinaryScanResult, len(security.BinaryFiles))
	for _, result := range security.BinaryFiles {
		scanResults[result.Path] = result
	}
	for _, file := range pkg.Files {
		scanStatus := ""
		scanProvider := ""
		scanVersion := ""
		if result, ok := scanResults[file.Path]; ok {
			scanStatus = result.Status
			scanProvider = skillmarketplace.BinaryScannerProviderBuiltin
			scanVersion = result.Scanner
			if result.ExternalScan != nil {
				scanProvider = result.ExternalScan.Provider
				scanVersion = result.ExternalScan.Scanner
				if scanVersion == "" {
					scanVersion = result.ExternalScan.Provider
				}
			}
			file.ContentType = result.ContentType
			file.ChecksumSHA256 = result.ChecksumSHA256
			file.Size = result.Size
		}
		bundle.Files = append(bundle.Files, skillspkg.AssetFile{
			Path: file.Path, Content: file.Content, ContentBase64: file.ContentBase64,
			ContentType: file.ContentType, ChecksumSHA256: file.ChecksumSHA256, ScanStatus: scanStatus,
			ScanProvider: scanProvider, ScanVersion: scanVersion,
			Size: file.Size, Revision: file.Revision, SourceURL: file.SourceURL,
			Executable: file.Executable, Binary: file.Binary,
		})
	}
	encoded, err := skillspkg.EncodeAssetBundle(bundle)
	if err != nil {
		return skillspkg.AssetBundle{}, err
	}
	return skillspkg.DecodeAssetBundle(encoded)
}

type uploadedSkillAsset struct {
	ObjectRefID string
	Bucket      string
	Key         string
	Version     string
}

func (s skillsToolService) persistBinarySkillAssets(ctx context.Context, workspaceID string, identifier string, pkg skillmarketplace.Package, bundle skillspkg.AssetBundle) (skillspkg.AssetBundle, []uploadedSkillAsset, error) {
	hasBinary := false
	for _, file := range bundle.Files {
		if file.Binary {
			hasBinary = true
			break
		}
	}
	if !hasBinary {
		return bundle, nil, nil
	}
	if strings.TrimSpace(s.objectStoreBucket) == "" {
		return bundle, nil, fmt.Errorf("%w: binary skill assets require a configured object store bucket", objectstore.ErrNotConfigured)
	}
	uploads := make([]uploadedSkillAsset, 0)
	for index := range bundle.Files {
		file := &bundle.Files[index]
		if !file.Binary {
			continue
		}
		if file.ScanStatus != skillmarketplace.BinaryScanPassed {
			return bundle, uploads, fmt.Errorf("%w: binary skill asset %q did not pass security scanning", managedagents.ErrForbidden, file.Path)
		}
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil {
			return bundle, uploads, fmt.Errorf("decode binary skill asset %q: %w", file.Path, err)
		}
		objectKey := path.Join(
			"skills", safeSkillObjectSegment(workspaceID), safeSkillObjectSegment(identifier),
			safeSkillObjectSegment(pkg.Revision), file.ChecksumSHA256, file.Path,
		)
		if err := objectstore.ValidateObjectKey(objectKey); err != nil {
			return bundle, uploads, err
		}
		putResult, err := s.objectStore.PutObject(ctx, objectstore.PutObjectInput{
			Bucket: s.objectStoreBucket, Key: objectKey, Body: bytes.NewReader(content),
			ContentType: file.ContentType, SizeBytes: int64(len(content)), ChecksumSHA256: file.ChecksumSHA256,
			Metadata: map[string]string{
				"tma-kind": "skill-asset", "skill-identifier": identifier,
				"source-repository": pkg.Source.Repository, "source-path": pkg.Source.Path,
				"scan-provider": file.ScanProvider, "scan-version": file.ScanVersion,
			},
		})
		if err != nil {
			return bundle, uploads, err
		}
		metadata, _ := json.Marshal(map[string]any{
			"kind": "skill_asset", "skill_identifier": identifier, "asset_path": file.Path,
			"source": pkg.Source, "package_revision": pkg.Revision,
			"scan_provider": file.ScanProvider, "scan_version": file.ScanVersion,
		})
		objectRef, err := managedagents.CreateObjectRefWithContext(ctx, s.store, managedagents.CreateObjectRefInput{
			WorkspaceID: workspaceID, StorageProvider: s.skillObjectStorageProvider(),
			Bucket: fallbackString(putResult.Bucket, s.objectStoreBucket), ObjectKey: fallbackString(putResult.Key, objectKey),
			ObjectVersion: putResult.Version, ContentType: file.ContentType, SizeBytes: int64(len(content)),
			ChecksumSHA256: fallbackString(putResult.ChecksumSHA256, file.ChecksumSHA256), ETag: putResult.ETag,
			Visibility: managedagents.ObjectVisibilityWorkspace, Metadata: metadata,
			CreatedBy: "skills.install:" + identifier,
		})
		if err != nil {
			_ = s.objectStore.DeleteObject(context.Background(), objectstore.DeleteObjectInput{
				Bucket: fallbackString(putResult.Bucket, s.objectStoreBucket), Key: fallbackString(putResult.Key, objectKey), Version: putResult.Version,
			})
			return bundle, uploads, err
		}
		upload := uploadedSkillAsset{ObjectRefID: objectRef.ID, Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion}
		uploads = append(uploads, upload)
		file.ObjectRefID = objectRef.ID
		file.ContentBase64 = ""
		for componentIndex := range bundle.SBOM.Components {
			if bundle.SBOM.Components[componentIndex].Path == file.Path {
				bundle.SBOM.Components[componentIndex].ObjectRefID = objectRef.ID
			}
		}
	}
	encoded, err := skillspkg.EncodeAssetBundle(bundle)
	if err != nil {
		return bundle, uploads, err
	}
	persisted, err := skillspkg.DecodeAssetBundle(encoded)
	return persisted, uploads, err
}

func (s skillsToolService) cleanupUploadedSkillAssets(ctx context.Context, uploads []uploadedSkillAsset) {
	for index := len(uploads) - 1; index >= 0; index-- {
		upload := uploads[index]
		_ = s.objectStore.DeleteObject(context.Background(), objectstore.DeleteObjectInput{
			Bucket: upload.Bucket, Key: upload.Key, Version: upload.Version,
		})
		_ = managedagents.DeleteObjectRefWithContext(ctx, s.store, upload.ObjectRefID)
	}
}

func (s skillsToolService) skillObjectStorageProvider() string {
	type configuredClient interface {
		Config() objectstore.Config
	}
	if client, ok := s.objectStore.(configuredClient); ok {
		if provider := strings.TrimSpace(client.Config().Provider); provider != "" {
			return provider
		}
	}
	return managedagents.ObjectStorageProviderS3
}

func safeSkillObjectSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return strings.Map(func(char rune) rune {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '-', char == '_', char == '.':
			return char
		default:
			return '_'
		}
	}, value)
}

func skillAssetIndex(bundle skillspkg.AssetBundle) tools.SkillsAssetIndex {
	index := tools.SkillsAssetIndex{
		Files:      make([]tools.SkillsAssetIndexFile, 0, len(bundle.Files)),
		TotalBytes: bundle.TotalBytes, Warnings: append([]string(nil), bundle.Warnings...), SBOM: bundle.SBOM,
	}
	for _, file := range bundle.Files {
		index.Files = append(index.Files, tools.SkillsAssetIndexFile{
			Path: file.Path, Size: file.Size, Revision: file.Revision, SourceURL: file.SourceURL,
			Executable: file.Executable, Binary: file.Binary, ContentType: file.ContentType,
			ChecksumSHA256: file.ChecksumSHA256, ObjectRefID: file.ObjectRefID, ScanStatus: file.ScanStatus,
			ScanProvider: file.ScanProvider, ScanVersion: file.ScanVersion,
		})
	}
	return index
}

func assetPaths(bundle skillspkg.AssetBundle) []string {
	paths := make([]string, 0, len(bundle.Files))
	for _, file := range bundle.Files {
		paths = append(paths, file.Path)
	}
	sort.Strings(paths)
	return paths
}

func compareSkillPackage(current skillspkg.Version, currentBundle skillspkg.AssetBundle, pkg skillmarketplace.Package, remoteBundle skillspkg.AssetBundle) tools.SkillsPreviewChanges {
	changes := tools.SkillsPreviewChanges{
		ContentChanged: current.ContentText != pkg.Content,
		AddedFiles:     []string{}, RemovedFiles: []string{}, ChangedFiles: []string{},
	}
	currentFiles := make(map[string]skillspkg.AssetFile, len(currentBundle.Files))
	remoteFiles := make(map[string]skillspkg.AssetFile, len(remoteBundle.Files))
	for _, file := range currentBundle.Files {
		currentFiles[file.Path] = file
	}
	for _, file := range remoteBundle.Files {
		remoteFiles[file.Path] = file
		installed, ok := currentFiles[file.Path]
		switch {
		case !ok:
			changes.AddedFiles = append(changes.AddedFiles, file.Path)
		case !strings.EqualFold(installed.ChecksumSHA256, file.ChecksumSHA256) || installed.Executable != file.Executable || installed.Binary != file.Binary:
			changes.ChangedFiles = append(changes.ChangedFiles, file.Path)
		}
	}
	for _, file := range currentBundle.Files {
		if _, ok := remoteFiles[file.Path]; !ok {
			changes.RemovedFiles = append(changes.RemovedFiles, file.Path)
		}
	}
	sort.Strings(changes.AddedFiles)
	sort.Strings(changes.RemovedFiles)
	sort.Strings(changes.ChangedFiles)
	return changes
}

func skillAssetSBOM(sbom skillmarketplace.PackageSBOM) skillspkg.AssetSBOM {
	result := skillspkg.AssetSBOM{
		Format: sbom.Format, PackageDigestSHA256: sbom.PackageDigestSHA256,
		Components: make([]skillspkg.AssetSBOMComponent, 0, len(sbom.Components)),
	}
	for _, component := range sbom.Components {
		result.Components = append(result.Components, skillspkg.AssetSBOMComponent{
			Path: component.Path, Kind: component.Kind, ContentType: component.ContentType,
			Size: component.Size, ChecksumSHA256: component.ChecksumSHA256,
			Revision: component.Revision, SourceURL: component.SourceURL,
		})
	}
	return result
}
