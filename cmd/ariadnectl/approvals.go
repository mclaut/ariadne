package main

import (
	"ariadne/internal/approval"
	"encoding/json"
	"fmt"
	"os"
)

func approvalsCmd(asJSON bool) int {
	pending, err := approval.New("").Pending()
	if err != nil {
		fmt.Fprintln(os.Stderr, "approvals:", err)
		return 1
	}
	if asJSON {
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
