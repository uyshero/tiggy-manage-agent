package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"tiggy-manage-agent/sdk/tma"
)

func commandSkill(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("skill command requires a subcommand")
	}
	switch args[0] {
	case "create":
		return commandSkillCreate(client, args[1:])
	case "list":
		return commandSkillList(client, args[1:])
	case "get", "archive":
		return commandSkillReadAction(client, args[0], args[1:])
	case "version":
		return commandSkillVersion(client, args[1:])
	case "resolve":
		return commandSkillResolve(client, args[1:])
	case "usage":
		return commandSkillUsage(client, args[1:])
	case "package":
		return commandSkillPackage(client, args[1:])
	case "retention":
		return commandSkillRetention(client, args[1:])
	case "gc":
		return commandSkillGC(client, args[1:])
	default:
		return fmt.Errorf("unknown skill subcommand %q", args[0])
	}
}

func commandSkillCreate(client *apiClient, args []string) error {
	flags := newCLIFlagSet("skill create")
	var request tma.CreateSkillRequest
	flags.StringVar(&request.WorkspaceID, "workspace", "", "workspace id")
	flags.StringVar(&request.Identifier, "identifier", "", "stable skill identifier")
	flags.StringVar(&request.Title, "title", "", "skill title")
	flags.StringVar(&request.Description, "description", "", "skill description")
	flags.StringVar(&request.OwnerType, "owner-type", "", "builtin | workspace | plugin")
	flags.StringVar(&request.SourceType, "source-type", "", "inline | github | artifact | catalog | plugin | builtin")
	flags.StringVar(&request.SourceLocator, "source-locator", "", "source locator")
	flags.StringVar(&request.SourcePath, "source-path", "", "source path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if request.Identifier == "" || request.Title == "" {
		return fmt.Errorf("skill create requires --identifier and --title")
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	result, err := sdk.Skills.Create(context.Background(), request)
	if err != nil {
		return err
	}
	return printJSON(result)
}

func commandSkillList(client *apiClient, args []string) error {
	flags := newCLIFlagSet("skill list")
	var query tma.SkillListQuery
	flags.StringVar(&query.WorkspaceID, "workspace", "", "workspace id")
	flags.BoolVar(&query.IncludeArchived, "include-archived", false, "include archived skills")
	if err := flags.Parse(args); err != nil {
		return err
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	items, err := sdk.Skills.List(context.Background(), query)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"skills": items})
}

func commandSkillReadAction(client *apiClient, action string, args []string) error {
	flags := newCLIFlagSet("skill " + action)
	var skillID string
	flags.StringVar(&skillID, "skill", "", "skill id")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if skillID == "" {
		return fmt.Errorf("skill %s requires --skill", action)
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	var result tma.Skill
	if action == "archive" {
		result, err = sdk.Skills.Archive(context.Background(), skillID)
	} else {
		result, err = sdk.Skills.Get(context.Background(), skillID)
	}
	if err != nil {
		return err
	}
	return printJSON(result)
}

func commandSkillVersion(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("skill version command requires a subcommand")
	}
	switch args[0] {
	case "create":
		return commandSkillVersionCreate(client, args[1:])
	case "list", "get", "download":
		return commandSkillVersionRead(client, args[0], args[1:])
	default:
		return fmt.Errorf("unknown skill version subcommand %q", args[0])
	}
}

func commandSkillVersionCreate(client *apiClient, args []string) error {
	flags := newCLIFlagSet("skill version create")
	var skillID, contentFormat, content, manifestJSON, assetsJSON, sourceRef, sourceRevision, sourceURL string
	flags.StringVar(&skillID, "skill", "", "skill id")
	flags.StringVar(&contentFormat, "format", "hybrid", "content format")
	flags.StringVar(&content, "content", "", "skill content")
	flags.StringVar(&manifestJSON, "manifest", `{}`, "manifest JSON")
	flags.StringVar(&assetsJSON, "assets", "", "asset bundle JSON")
	flags.StringVar(&sourceRef, "source-ref", "", "source reference")
	flags.StringVar(&sourceRevision, "source-revision", "", "source revision")
	flags.StringVar(&sourceURL, "source-url", "", "source URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if skillID == "" || content == "" {
		return fmt.Errorf("skill version create requires --skill and --content")
	}
	manifest, err := decodeJSONFlag[tma.SkillManifest](manifestJSON, "manifest")
	if err != nil {
		return err
	}
	var assets *tma.SkillAssetBundle
	if assetsJSON != "" {
		decoded, decodeErr := decodeJSONFlag[tma.SkillAssetBundle](assetsJSON, "assets")
		if decodeErr != nil {
			return decodeErr
		}
		assets = &decoded
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	result, err := sdk.Skills.CreateVersion(context.Background(), skillID, tma.CreateSkillVersionRequest{
		ContentFormat: contentFormat, Manifest: manifest, ContentText: content, Assets: assets,
		SourceRef: sourceRef, SourceRevision: sourceRevision, SourceURL: sourceURL,
	})
	if err != nil {
		return err
	}
	return printJSON(result)
}

func commandSkillVersionRead(client *apiClient, action string, args []string) error {
	flags := newCLIFlagSet("skill version " + action)
	var skillID, output string
	var version int
	flags.StringVar(&skillID, "skill", "", "skill id")
	flags.IntVar(&version, "version", 0, "skill version")
	flags.StringVar(&output, "output", "", "output package path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if skillID == "" {
		return fmt.Errorf("skill version %s requires --skill", action)
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	if action == "list" {
		items, listErr := sdk.Skills.ListVersions(context.Background(), skillID)
		if listErr != nil {
			return listErr
		}
		return printJSON(map[string]any{"versions": items})
	}
	version32, err := requiredInt32(version, "version")
	if err != nil {
		return err
	}
	if action == "get" {
		result, getErr := sdk.Skills.GetVersion(context.Background(), skillID, version32)
		if getErr != nil {
			return getErr
		}
		return printJSON(result)
	}
	if output == "" {
		return fmt.Errorf("skill version download requires --output")
	}
	file, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer file.Close()
	return sdk.Skills.DownloadPackage(context.Background(), skillID, version32, file)
}

func commandSkillResolve(client *apiClient, args []string) error {
	flags := newCLIFlagSet("skill resolve")
	var workspaceID, skillsJSON string
	var maxTokens int
	flags.StringVar(&workspaceID, "workspace", "", "workspace id")
	flags.StringVar(&skillsJSON, "skills", "", "SkillConfig JSON")
	flags.IntVar(&maxTokens, "max-tokens", 0, "maximum rendered tokens")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if skillsJSON == "" {
		return fmt.Errorf("skill resolve requires --skills")
	}
	config, err := decodeJSONFlag[tma.SkillConfig](skillsJSON, "skills")
	if err != nil {
		return err
	}
	maxTokens32, err := optionalInt32(maxTokens, "max-tokens")
	if err != nil {
		return err
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	result, err := sdk.Skills.ResolvePreview(context.Background(), tma.ResolveSkillsPreviewRequest{WorkspaceID: workspaceID, Skills: config, MaxTokens: maxTokens32})
	if err != nil {
		return err
	}
	return printJSON(result)
}

func commandSkillUsage(client *apiClient, args []string) error {
	flags := newCLIFlagSet("skill usage")
	var sessionID, turnID string
	flags.StringVar(&sessionID, "session", "", "session id")
	flags.StringVar(&turnID, "turn", "", "turn id")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if sessionID == "" {
		return fmt.Errorf("skill usage requires --session")
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	items, err := sdk.Skills.ListUsages(context.Background(), sessionID, turnID)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"skill_usages": items})
}

func commandSkillPackage(client *apiClient, args []string) error {
	if len(args) == 0 || args[0] != "backfill" {
		return fmt.Errorf("skill package requires backfill subcommand")
	}
	flags := newCLIFlagSet("skill package backfill")
	var workspaceID string
	var limit int
	flags.StringVar(&workspaceID, "workspace", "", "workspace id")
	flags.IntVar(&limit, "limit", 0, "maximum versions to backfill")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	limit32, err := optionalInt32(limit, "limit")
	if err != nil {
		return err
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	result, err := sdk.Skills.BackfillPackages(context.Background(), tma.SkillPackageBackfillRequest{WorkspaceID: workspaceID, Limit: limit32})
	if err != nil {
		return err
	}
	return printJSON(result)
}

func commandSkillRetention(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("skill retention command requires a subcommand")
	}
	if args[0] == "effective" {
		flags := newCLIFlagSet("skill retention effective")
		var workspaceID string
		flags.StringVar(&workspaceID, "workspace", "", "workspace id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if workspaceID == "" {
			return fmt.Errorf("skill retention effective requires --workspace")
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		result, err := sdk.Skills.EffectiveRetentionPolicy(context.Background(), workspaceID)
		if err != nil {
			return err
		}
		return printJSON(result)
	}
	return commandSkillRetentionPolicy(client, args[0], args[1:])
}

func commandSkillRetentionPolicy(client *apiClient, action string, args []string) error {
	flags := newCLIFlagSet("skill retention " + action)
	var policyID, scope, organizationID, workspaceID, configJSON string
	var version int
	var includeArchived bool
	flags.StringVar(&policyID, "policy", "", "policy id")
	flags.StringVar(&scope, "scope", "", "organization | workspace")
	flags.StringVar(&organizationID, "organization", "", "organization id")
	flags.StringVar(&workspaceID, "workspace", "", "workspace id")
	flags.StringVar(&configJSON, "config", "", "retention config JSON")
	flags.IntVar(&version, "version", 0, "policy version")
	flags.BoolVar(&includeArchived, "include-archived", false, "include archived policies")
	if err := flags.Parse(args); err != nil {
		return err
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch action {
	case "create":
		if scope == "" || configJSON == "" {
			return fmt.Errorf("skill retention create requires --scope and --config")
		}
		config, decodeErr := decodeJSONFlag[tma.SkillRetentionPolicyConfig](configJSON, "config")
		if decodeErr != nil {
			return decodeErr
		}
		result, callErr := sdk.Skills.CreateRetentionPolicy(ctx, tma.CreateSkillRetentionPolicyRequest{ScopeType: scope, OrganizationID: organizationID, WorkspaceID: workspaceID, Config: config})
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "list":
		items, callErr := sdk.Skills.ListRetentionPolicies(ctx, tma.SkillRetentionPolicyQuery{OrganizationID: organizationID, WorkspaceID: workspaceID, IncludeArchived: includeArchived})
		if callErr != nil {
			return callErr
		}
		return printJSON(map[string]any{"policies": items})
	case "get":
		if policyID == "" {
			return fmt.Errorf("skill retention get requires --policy")
		}
		result, callErr := sdk.Skills.GetRetentionPolicy(ctx, policyID)
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "publish":
		if policyID == "" || configJSON == "" {
			return fmt.Errorf("skill retention publish requires --policy and --config")
		}
		config, decodeErr := decodeJSONFlag[tma.SkillRetentionPolicyConfig](configJSON, "config")
		if decodeErr != nil {
			return decodeErr
		}
		result, callErr := sdk.Skills.PublishRetentionPolicyVersion(ctx, policyID, tma.PublishSkillRetentionPolicyRequest{Config: config})
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "get-version":
		version32, validationErr := requiredInt32(version, "version")
		if policyID == "" || validationErr != nil {
			return fmt.Errorf("skill retention get-version requires --policy and a positive --version")
		}
		result, callErr := sdk.Skills.GetRetentionPolicyVersion(ctx, policyID, version32)
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "archive":
		if policyID == "" {
			return fmt.Errorf("skill retention archive requires --policy")
		}
		result, callErr := sdk.Skills.ArchiveRetentionPolicy(ctx, policyID)
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	default:
		return fmt.Errorf("unknown skill retention subcommand %q", action)
	}
}

func commandSkillGC(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("skill gc command requires a subcommand")
	}
	action := args[0]
	flags := newCLIFlagSet("skill gc " + action)
	var workspaceID, runID, confirm string
	var limit int
	flags.StringVar(&workspaceID, "workspace", "", "workspace id")
	flags.StringVar(&runID, "run", "", "GC run id")
	flags.StringVar(&confirm, "confirm", "", "run confirmation")
	flags.IntVar(&limit, "limit", 0, "result limit")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	limit32, err := optionalInt32(limit, "limit")
	if err != nil {
		return err
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	request := tma.SkillAssetGCRequest{WorkspaceID: workspaceID, Limit: limit32, Confirm: confirm}
	query := tma.SkillAssetGCListQuery{WorkspaceID: workspaceID, Limit: limit32}
	switch action {
	case "preview":
		result, callErr := sdk.Skills.PreviewAssetGC(ctx, request)
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "run":
		if confirm == "" {
			return fmt.Errorf("skill gc run requires --confirm DELETE")
		}
		result, callErr := sdk.Skills.RunAssetGC(ctx, request)
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "list":
		items, callErr := sdk.Skills.ListAssetGCRuns(ctx, query)
		if callErr != nil {
			return callErr
		}
		return printJSON(map[string]any{"runs": items})
	case "get":
		if runID == "" {
			return fmt.Errorf("skill gc get requires --run")
		}
		result, callErr := sdk.Skills.GetAssetGCRun(ctx, runID)
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "tombstones":
		items, callErr := sdk.Skills.ListAssetGCTombstones(ctx, query)
		if callErr != nil {
			return callErr
		}
		return printJSON(map[string]any{"tombstones": items})
	default:
		return fmt.Errorf("unknown skill gc subcommand %q", action)
	}
}

func newCLIFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func decodeJSONFlag[T any](value string, name string) (T, error) {
	var result T
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return result, fmt.Errorf("invalid --%s JSON: %w", name, err)
	}
	return result, nil
}

func optionalInt32(value int, name string) (int32, error) {
	if value < 0 || int64(value) > math.MaxInt32 {
		return 0, fmt.Errorf("--%s must be between 0 and %d", name, int64(math.MaxInt32))
	}
	return int32(value), nil
}

func requiredInt32(value int, name string) (int32, error) {
	result, err := optionalInt32(value, name)
	if err != nil {
		return 0, err
	}
	if result == 0 {
		return 0, fmt.Errorf("--%s must be positive", name)
	}
	return result, nil
}

func splitCLIList(value string) []string {
	items := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}
