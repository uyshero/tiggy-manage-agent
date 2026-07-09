package main

import (
	"fmt"
	"net/http"
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
		var response any
		if err := client.do(http.MethodGet, "/v1/observability/status", nil, &response); err != nil {
			return err
		}
		return printJSON(response)
	default:
		return fmt.Errorf("unknown observability subcommand %q", args[0])
	}
}
