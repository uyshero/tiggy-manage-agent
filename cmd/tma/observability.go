package main

import (
	"context"
	"fmt"
)

func commandObservability(client *apiClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("observability command requires a subcommand")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return fmt.Errorf("observability status does not accept arguments")
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Observability.Status(context.Background())
		if err != nil {
			return err
		}
		return printJSON(response)
	case "retry":
		if len(args) != 1 {
			return fmt.Errorf("observability retry does not accept arguments")
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Observability.Retry(context.Background())
		if err != nil {
			return err
		}
		return printJSON(response)
	case "integrity-keys":
		if len(args) != 1 {
			return fmt.Errorf("observability integrity-keys does not accept arguments")
		}
		sdk, err := client.sdkClient()
		if err != nil {
			return err
		}
		response, err := sdk.Observability.IntegrityKeys(context.Background())
		if err != nil {
			return err
		}
		return printJSON(response)
	default:
		return fmt.Errorf("unknown observability subcommand %q", args[0])
	}
}
