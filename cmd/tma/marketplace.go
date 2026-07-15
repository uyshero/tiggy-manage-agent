package main

import (
	"context"
	"fmt"
	"math"

	"tiggy-manage-agent/sdk/tma"
)

func commandMarketplace(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("marketplace command requires a subcommand")
	}
	switch args[0] {
	case "discover":
		return commandMarketplaceDiscover(client, args[1:])
	case "preview", "install":
		return commandMarketplacePackage(client, args[0], false, args[1:])
	case "internal":
		return commandMarketplaceInternal(client, args[1:])
	case "enable", "disable":
		return commandMarketplaceBinding(client, args[0], args[1:])
	case "entry":
		return commandMarketplaceEntry(client, args[1:])
	case "policy":
		return commandMarketplacePolicy(client, args[1:])
	default:
		return fmt.Errorf("unknown marketplace subcommand %q", args[0])
	}
}

func commandMarketplaceDiscover(client *apiClient, args []string) error {
	flags := newCLIFlagSet("marketplace discover")
	var sessionID, query, repository string
	var limit int
	flags.StringVar(&sessionID, "session", "", "session id")
	flags.StringVar(&query, "query", "", "search query")
	flags.StringVar(&repository, "repository", "", "GitHub owner/repository")
	flags.IntVar(&limit, "limit", 0, "result limit")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if sessionID == "" || query == "" && repository == "" {
		return fmt.Errorf("marketplace discover requires --session and either --query or --repository")
	}
	limit32, err := optionalInt32(limit, "limit")
	if err != nil {
		return err
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	result, err := sdk.Marketplace.Discover(context.Background(), tma.MarketplaceDiscoverQuery{SessionID: sessionID, Query: query, Repository: repository, Limit: limit32})
	if err != nil {
		return err
	}
	return printJSON(result)
}

func commandMarketplacePackage(client *apiClient, action string, internal bool, args []string) error {
	name := "marketplace " + action
	if internal {
		name = "marketplace internal " + action
	}
	flags := newCLIFlagSet(name)
	var sessionID, identifier, sourceJSON, policyID, policyRevision string
	var policyVersion int
	var upgrade bool
	flags.StringVar(&sessionID, "session", "", "session id")
	flags.StringVar(&identifier, "identifier", "", "installed skill identifier")
	flags.StringVar(&sourceJSON, "source", "", "Marketplace source JSON")
	flags.StringVar(&policyID, "policy", "", "pinned policy id")
	flags.IntVar(&policyVersion, "policy-version", 0, "pinned policy version")
	flags.StringVar(&policyRevision, "policy-revision", "", "pinned policy revision")
	flags.BoolVar(&upgrade, "upgrade", false, "upgrade an existing skill")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if sessionID == "" || sourceJSON == "" {
		return fmt.Errorf("%s requires --session and --source", name)
	}
	source, err := decodeJSONFlag[tma.MarketplaceSource](sourceJSON, "source")
	if err != nil {
		return err
	}
	policyVersion32, err := optionalInt32(policyVersion, "policy-version")
	if err != nil {
		return err
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	if action == "preview" {
		request := tma.MarketplacePreviewRequest{SessionID: sessionID, Identifier: identifier, Source: source}
		var result tma.MarketplacePreviewResult
		if internal {
			result, err = sdk.Marketplace.PreviewInternal(ctx, request)
		} else {
			result, err = sdk.Marketplace.Preview(ctx, request)
		}
		if err != nil {
			return err
		}
		return printJSON(result)
	}
	request := tma.MarketplaceInstallRequest{
		SessionID: sessionID, Identifier: identifier, Source: source, PolicyID: policyID,
		PolicyVersion: policyVersion32, PolicyRevision: policyRevision, UpgradeExisting: upgrade,
	}
	var result tma.MarketplaceInstallResult
	if internal {
		result, err = sdk.Marketplace.InstallInternal(ctx, request)
	} else {
		result, err = sdk.Marketplace.Install(ctx, request)
	}
	if err != nil {
		return err
	}
	return printJSON(result)
}

func commandMarketplaceInternal(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("marketplace internal command requires a subcommand")
	}
	if args[0] == "preview" || args[0] == "install" {
		return commandMarketplacePackage(client, args[0], true, args[1:])
	}
	if args[0] != "list" {
		return fmt.Errorf("unknown marketplace internal subcommand %q", args[0])
	}
	flags := newCLIFlagSet("marketplace internal list")
	var sessionID, query, category, tags string
	var limit int
	flags.StringVar(&sessionID, "session", "", "session id")
	flags.StringVar(&query, "query", "", "search query")
	flags.StringVar(&category, "category", "", "catalog category")
	flags.StringVar(&tags, "tags", "", "comma-separated tags")
	flags.IntVar(&limit, "limit", 0, "result limit")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if sessionID == "" {
		return fmt.Errorf("marketplace internal list requires --session")
	}
	limit32, err := optionalInt32(limit, "limit")
	if err != nil {
		return err
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	result, err := sdk.Marketplace.BrowseInternal(context.Background(), tma.MarketplaceInternalQuery{
		SessionID: sessionID, Query: query, Category: category, Tags: splitCLIList(tags), Limit: limit32,
	})
	if err != nil {
		return err
	}
	return printJSON(result)
}

func commandMarketplaceBinding(client *apiClient, action string, args []string) error {
	flags := newCLIFlagSet("marketplace " + action)
	var skillID, sessionID, mode, inputsJSON string
	var version, priority int
	flags.StringVar(&skillID, "skill", "", "installed skill id")
	flags.StringVar(&sessionID, "session", "", "session id")
	flags.IntVar(&version, "version", 0, "skill version")
	flags.StringVar(&mode, "mode", "", "full | summary | examples_only (default summary)")
	flags.IntVar(&priority, "priority", 0, "binding priority")
	flags.StringVar(&inputsJSON, "inputs", "", "skill inputs JSON object")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if skillID == "" || sessionID == "" {
		return fmt.Errorf("marketplace %s requires --skill and --session", action)
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	if action == "disable" {
		result, callErr := sdk.Marketplace.DisableInstalled(context.Background(), skillID, tma.MarketplaceDisableRequest{SessionID: sessionID})
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	}
	version32, err := optionalInt32(version, "version")
	if err != nil {
		return err
	}
	priority32, err := signedInt32(priority, "priority")
	if err != nil {
		return err
	}
	inputs, err := parseOptionalJSONObjectFlag(inputsJSON, "inputs")
	if err != nil {
		return err
	}
	result, err := sdk.Marketplace.EnableInstalled(context.Background(), skillID, tma.MarketplaceEnableRequest{
		SessionID: sessionID, Version: version32, Mode: mode, Priority: priority32, Inputs: inputs,
	})
	if err != nil {
		return err
	}
	return printJSON(result)
}

func commandMarketplaceEntry(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("marketplace entry command requires a subcommand")
	}
	action := args[0]
	flags := newCLIFlagSet("marketplace entry " + action)
	var entryID, workspaceID, skillID, summary, category, tags, status, note string
	var skillVersion int
	var includeWithdrawn bool
	flags.StringVar(&entryID, "entry", "", "Marketplace entry id")
	flags.StringVar(&workspaceID, "workspace", "", "workspace id")
	flags.StringVar(&skillID, "skill", "", "skill id")
	flags.IntVar(&skillVersion, "version", 0, "skill version")
	flags.StringVar(&summary, "summary", "", "entry summary")
	flags.StringVar(&category, "category", "", "entry category")
	flags.StringVar(&tags, "tags", "", "comma-separated tags")
	flags.StringVar(&status, "status", "", "entry status")
	flags.StringVar(&note, "note", "", "review or withdrawal note")
	flags.BoolVar(&includeWithdrawn, "include-withdrawn", false, "include withdrawn entries")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	sdk, err := client.sdkClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch action {
	case "create":
		version32, validationErr := requiredInt32(skillVersion, "version")
		if skillID == "" || validationErr != nil {
			return fmt.Errorf("marketplace entry create requires --skill and a positive --version")
		}
		result, callErr := sdk.Marketplace.CreateEntry(ctx, tma.CreateMarketplaceEntryRequest{
			WorkspaceID: workspaceID, SkillID: skillID, SkillVersion: version32,
			Summary: summary, Category: category, Tags: splitCLIList(tags),
		})
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "list":
		items, callErr := sdk.Marketplace.ListEntries(ctx, tma.MarketplaceEntryQuery{WorkspaceID: workspaceID, Status: status, IncludeWithdrawn: includeWithdrawn})
		if callErr != nil {
			return callErr
		}
		return printJSON(map[string]any{"entries": items})
	case "get":
		if entryID == "" {
			return fmt.Errorf("marketplace entry get requires --entry")
		}
		result, callErr := sdk.Marketplace.GetEntry(ctx, entryID, workspaceID)
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "update":
		if entryID == "" {
			return fmt.Errorf("marketplace entry update requires --entry")
		}
		result, callErr := sdk.Marketplace.UpdateEntry(ctx, entryID, tma.UpdateMarketplaceEntryRequest{WorkspaceID: workspaceID, Summary: summary, Category: category, Tags: splitCLIList(tags)})
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "submit", "publish", "withdraw":
		if entryID == "" {
			return fmt.Errorf("marketplace entry %s requires --entry", action)
		}
		request := tma.MarketplaceTransitionRequest{WorkspaceID: workspaceID, Note: note}
		var result tma.MarketplaceEntry
		var callErr error
		switch action {
		case "submit":
			result, callErr = sdk.Marketplace.SubmitEntry(ctx, entryID, request)
		case "publish":
			result, callErr = sdk.Marketplace.PublishEntry(ctx, entryID, request)
		case "withdraw":
			result, callErr = sdk.Marketplace.WithdrawEntry(ctx, entryID, request)
		}
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	default:
		return fmt.Errorf("unknown marketplace entry subcommand %q", action)
	}
}

func commandMarketplacePolicy(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("marketplace policy command requires a subcommand")
	}
	action := args[0]
	flags := newCLIFlagSet("marketplace policy " + action)
	var policyID, scope, organizationID, workspaceID, configJSON string
	var version int
	var includeArchived bool
	flags.StringVar(&policyID, "policy", "", "Marketplace policy id")
	flags.StringVar(&scope, "scope", "", "organization | workspace")
	flags.StringVar(&organizationID, "organization", "", "organization id")
	flags.StringVar(&workspaceID, "workspace", "", "workspace id")
	flags.StringVar(&configJSON, "config", "", "Marketplace policy config JSON")
	flags.IntVar(&version, "version", 0, "policy version")
	flags.BoolVar(&includeArchived, "include-archived", false, "include archived policies")
	if err := flags.Parse(args[1:]); err != nil {
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
			return fmt.Errorf("marketplace policy create requires --scope and --config")
		}
		config, decodeErr := decodeJSONFlag[tma.MarketplacePolicyConfig](configJSON, "config")
		if decodeErr != nil {
			return decodeErr
		}
		result, callErr := sdk.Marketplace.CreatePolicy(ctx, tma.CreateMarketplacePolicyRequest{ScopeType: scope, OrganizationID: organizationID, WorkspaceID: workspaceID, Config: config})
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "list":
		items, callErr := sdk.Marketplace.ListPolicies(ctx, tma.MarketplacePolicyQuery{OrganizationID: organizationID, WorkspaceID: workspaceID, IncludeArchived: includeArchived})
		if callErr != nil {
			return callErr
		}
		return printJSON(map[string]any{"policies": items})
	case "get":
		if policyID == "" {
			return fmt.Errorf("marketplace policy get requires --policy")
		}
		result, callErr := sdk.Marketplace.GetPolicy(ctx, policyID)
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "publish":
		if policyID == "" || configJSON == "" {
			return fmt.Errorf("marketplace policy publish requires --policy and --config")
		}
		config, decodeErr := decodeJSONFlag[tma.MarketplacePolicyConfig](configJSON, "config")
		if decodeErr != nil {
			return decodeErr
		}
		result, callErr := sdk.Marketplace.PublishPolicyVersion(ctx, policyID, tma.PublishMarketplacePolicyRequest{Config: config})
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "get-version":
		version32, validationErr := requiredInt32(version, "version")
		if policyID == "" || validationErr != nil {
			return fmt.Errorf("marketplace policy get-version requires --policy and a positive --version")
		}
		result, callErr := sdk.Marketplace.GetPolicyVersion(ctx, policyID, version32)
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	case "archive":
		if policyID == "" {
			return fmt.Errorf("marketplace policy archive requires --policy")
		}
		result, callErr := sdk.Marketplace.ArchivePolicy(ctx, policyID)
		if callErr != nil {
			return callErr
		}
		return printJSON(result)
	default:
		return fmt.Errorf("unknown marketplace policy subcommand %q", action)
	}
}

func signedInt32(value int, name string) (int32, error) {
	if int64(value) < math.MinInt32 || int64(value) > math.MaxInt32 {
		return 0, fmt.Errorf("--%s must fit int32", name)
	}
	return int32(value), nil
}
