package main

import (
	"ariadne/internal/store"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	quarantineReason      = "deterministic-secret-pattern-v1"
	quarantineClearReason = "current-detector-no-longer-matches-v1"
)

type quarantineCollectionReport struct {
	Collection         string         `json:"collection"`
	Matched            int            `json:"matched"`
	AlreadyQuarantined int            `json:"already_quarantined"`
	Pending            int            `json:"pending"`
	Applied            int            `json:"applied"`
	NoLongerMatching   int            `json:"no_longer_matching"`
	Restored           int            `json:"restored"`
	ByWing             map[string]int `json:"by_wing,omitempty"`
	ByRule             map[string]int `json:"by_rule,omitempty"`
	Truncated          bool           `json:"truncated,omitempty"`
}

type quarantinePlan struct {
	report  quarantineCollectionReport
	groups  map[string][]uint64
	cleared map[string][]uint64
}

func quarantineSecretsCmd(args []string) int {
	fs := flag.NewFlagSet("quarantine-secrets", flag.ContinueOnError)
	collections := fs.String("collections", collection, "comma-separated Qdrant collections")
	apply := fs.Bool("apply", false, "mark matching records quarantined; default is dry-run")
	reconcile := fs.Bool("reconcile", false, "with --apply, restore prior status when current rules no longer match")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	names := collectionNames(*collections)
	if len(names) == 0 {
		fmt.Fprintln(os.Stderr, "quarantine-secrets: no collections selected")
		return 2
	}
	st, err := consolidationStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "quarantine-secrets:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	plans := make([]quarantinePlan, 0, len(names))
	for _, name := range names {
		points, truncated, err := st.SensitivePoints(ctx, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "quarantine-secrets: scan %s: %v\n", name, err)
			return 1
		}
		cleared, err := st.ClearedQuarantinePoints(ctx, name, quarantineReason)
		if err != nil {
			fmt.Fprintf(os.Stderr, "quarantine-secrets: reconcile scan %s: %v\n", name, err)
			return 1
		}
		plan := buildQuarantinePlan(name, points, cleared)
		plan.report.Truncated = truncated
		if truncated {
			fmt.Fprintf(os.Stderr, "quarantine-secrets: %s reached the 200000-point safety limit; no changes applied\n", name)
			return 1
		}
		plans = append(plans, plan)
	}
	if *apply {
		now := strconv.FormatInt(time.Now().Unix(), 10)
		for i := range plans {
			for previousStatus, ids := range plans[i].groups {
				if err := st.SetMetaByIDsInCollection(ctx, plans[i].report.Collection, ids, map[string]string{
					"status":                store.StatusQuarantined,
					"quarantine_state":      "active",
					"pre_quarantine_status": previousStatus,
					"quarantined_at":        now,
					"quarantine_reason":     quarantineReason,
				}); err != nil {
					fmt.Fprintf(os.Stderr, "quarantine-secrets: apply %s: %v\n", plans[i].report.Collection, err)
					return 1
				}
				plans[i].report.Applied += len(ids)
			}
			if *reconcile {
				for previousStatus, ids := range plans[i].cleared {
					if err := st.SetMetaByIDsInCollection(ctx, plans[i].report.Collection, ids, map[string]string{
						"status": previousStatus, "quarantine_state": "cleared",
						"quarantine_cleared_at": now, "quarantine_clear_reason": quarantineClearReason,
					}); err != nil {
						fmt.Fprintf(os.Stderr, "quarantine-secrets: reconcile %s: %v\n", plans[i].report.Collection, err)
						return 1
					}
					plans[i].report.Restored += len(ids)
				}
			}
		}
	}
	reports := make([]quarantineCollectionReport, len(plans))
	for i := range plans {
		reports[i] = plans[i].report
	}
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"apply": *apply, "collections": reports})
		return 0
	}
	mode := "dry-run"
	if *apply {
		mode = "applied"
	}
	fmt.Printf("Credential quarantine (%s)\n", mode)
	for _, report := range reports {
		fmt.Printf("  %s: matched=%d pending=%d already=%d applied=%d cleared=%d restored=%d\n",
			report.Collection, report.Matched, report.Pending, report.AlreadyQuarantined, report.Applied,
			report.NoLongerMatching, report.Restored)
	}
	return 0
}

func collectionNames(raw string) []string {
	seen := map[string]bool{}
	for _, item := range strings.Split(raw, ",") {
		name := strings.TrimSpace(item)
		if name != "" {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func buildQuarantinePlan(
	collection string, points []store.SensitivePoint, cleared []store.ClearedQuarantinePoint,
) quarantinePlan {
	plan := quarantinePlan{
		report: quarantineCollectionReport{
			Collection: collection, Matched: len(points), ByWing: map[string]int{}, ByRule: map[string]int{},
		},
		groups: map[string][]uint64{}, cleared: map[string][]uint64{},
	}
	for _, point := range points {
		plan.report.ByWing[point.Wing]++
		for _, finding := range point.Findings {
			plan.report.ByRule[finding]++
		}
		if point.Status == store.StatusQuarantined {
			plan.report.AlreadyQuarantined++
			continue
		}
		previous := point.Status
		if previous == "" {
			previous = "legacy-active"
		}
		plan.groups[previous] = append(plan.groups[previous], point.ID)
		plan.report.Pending++
	}
	for _, point := range cleared {
		previous := point.PreviousStatus
		if previous == "" || previous == "legacy-active" {
			previous = "active"
		}
		plan.cleared[previous] = append(plan.cleared[previous], point.ID)
		plan.report.NoLongerMatching++
	}
	return plan
}
