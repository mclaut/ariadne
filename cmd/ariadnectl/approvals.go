package main

import (
	"ariadne/internal/approval"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func approvalsCmd(args []string) int {
	flags := flag.NewFlagSet("approvals", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print JSON")
	id := flags.String("id", "", "read one request's lifecycle without authorizing it")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "approvals: unexpected arguments")
		return 2
	}
	if *id != "" {
		status, err := approval.New("").Status(*id)
		if err != nil {
			fmt.Fprintln(os.Stderr, "approvals:", err)
			return 1
		}
		if *asJSON {
			if err := json.NewEncoder(os.Stdout).Encode(status); err != nil {
				return 1
			}
		} else {
			fmt.Printf("%s  %s  %s\n", status.ID, status.Kind, status.State)
		}
		return 0
	}
	pending, err := approval.New("").Pending()
	if err != nil {
		fmt.Fprintln(os.Stderr, "approvals:", err)
		return 1
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"pending": pending})
		return 0
	}
	if len(pending) == 0 {
		fmt.Println("No pending access requests.")
		return 0
	}
	for _, request := range pending {
		fmt.Printf("%s  %s  %s\n", request.ID, request.Kind, approvalRequestScope(request))
	}
	return 0
}

func approvalRequestScope(request approval.Request) string {
	if request.Kind == approval.KindCredential {
		return request.SourceWing + " → " + request.TargetWing + " · " + request.Resource + " · " + request.Purpose
	}
	return request.ActiveWing + " → all wings · " + request.Purpose
}
