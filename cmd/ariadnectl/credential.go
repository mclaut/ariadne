package main

import (
	"ariadne/internal/approval"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func credentialCmd(args []string) int {
	if len(args) == 0 {
		credentialUsage(os.Stderr)
		return 2
	}
	action := args[0]
	if action != "trust" && action != "revoke" {
		credentialUsage(os.Stderr)
		return 2
	}
	flags := flag.NewFlagSet("credential "+action, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	sourceWing := flags.String("source-wing", "", "wing that owns the credential")
	targetWing := flags.String("target-wing", "", "wing allowed to use the credential")
	resource := flags.String("resource", "", "exact credential path or name; never its value")
	purpose := flags.String("purpose", "", "exact allowed purpose")
	yes := flags.Bool("yes", false, "confirm this local trust policy change")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if strings.TrimSpace(*sourceWing) == "" || strings.TrimSpace(*targetWing) == "" ||
		strings.TrimSpace(*resource) == "" || strings.TrimSpace(*purpose) == "" {
		fmt.Fprintln(os.Stderr, "credential:", "--source-wing, --target-wing, --resource, and --purpose are required")
		return 2
	}
	if !*yes {
		fmt.Fprintln(os.Stderr, "credential: refusing policy change without --yes")
		return 2
	}
	event, err := approval.New("").SetCredentialTrust(approval.CredentialScope{
		SourceWing: *sourceWing, TargetWing: *targetWing, Resource: *resource, Purpose: *purpose,
	}, action == "trust", "ariadnectl")
	if err != nil {
		fmt.Fprintln(os.Stderr, "credential:", err)
		return 1
	}
	fmt.Printf("credential %s recorded: %s (%s → %s · %s · %s)\n",
		event.Action, event.ID, event.SourceWing, event.TargetWing, event.Resource, event.Purpose)
	return 0
}

func credentialUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: ariadnectl credential {trust|revoke} --source-wing <wing> "+
		"--target-wing <wing> --resource <path-or-name> --purpose <exact-purpose> --yes")
}
