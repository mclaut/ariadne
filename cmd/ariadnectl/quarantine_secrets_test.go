package main

import (
	"ariadne/internal/store"
	"reflect"
	"testing"
)

func TestCollectionNamesAreStableAndUnique(t *testing.T) {
	if got, want := collectionNames(" sessions,ariadne,sessions "), []string{"ariadne", "sessions"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("collectionNames = %#v", got)
	}
}

func TestBuildQuarantinePlanPreservesPreviousStatus(t *testing.T) {
	plan := buildQuarantinePlan("ariadne", []store.SensitivePoint{
		{ID: 1, Wing: "app", Status: "", Findings: []string{"secret-assignment"}},
		{ID: 2, Wing: "app", Status: store.StatusActive, Findings: []string{"known-token"}},
		{ID: 3, Wing: "old", Status: store.StatusQuarantined, Findings: []string{"private-key"}},
	}, []store.ClearedQuarantinePoint{
		{ID: 4, PreviousStatus: "legacy-active"},
		{ID: 5, PreviousStatus: store.StatusArchived},
	})
	if plan.report.Matched != 3 || plan.report.Pending != 2 || plan.report.AlreadyQuarantined != 1 {
		t.Fatalf("report = %#v", plan.report)
	}
	if !reflect.DeepEqual(plan.groups["legacy-active"], []uint64{1}) ||
		!reflect.DeepEqual(plan.groups[store.StatusActive], []uint64{2}) {
		t.Fatalf("groups = %#v", plan.groups)
	}
	if plan.report.NoLongerMatching != 2 ||
		!reflect.DeepEqual(plan.cleared[store.StatusActive], []uint64{4}) ||
		!reflect.DeepEqual(plan.cleared[store.StatusArchived], []uint64{5}) {
		t.Fatalf("cleared = %#v report=%#v", plan.cleared, plan.report)
	}
}
