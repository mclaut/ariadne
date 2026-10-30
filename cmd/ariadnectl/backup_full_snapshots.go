package main

import (
	"ariadne/internal/backupretention"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Only the native snapshots recorded by this restored full set may expire.
// Existing standalone recovery copies are left for a separately approved plan.
func pruneFullNativeSnapshots(keeper string) error {
	m, err := backupretention.Verify(keeper)
	if err != nil {
		return err
	}
	if m.Object != backupObjectFull || m.Metadata["restore_verified"] != backupProofVerified {
		return errors.New("restored full required before native snapshot cleanup")
	}
	data, err := os.ReadFile(filepath.Join(keeper, "recovery.json")) //nolint:gosec // sealed full receipt
	if err != nil {
		return err
	}
	var receipt struct {
		Status      string                              `json:"status"`
		Collections map[string]backupretention.Manifest `json:"collections"`
	}
	if err = json.Unmarshal(data, &receipt); err != nil {
		return err
	}
	if receipt.Status != "PASS" || len(receipt.Collections) == 0 {
		return errors.New("successful full collection recovery required")
	}
	// Check all collection proofs before issuing any delete requests.
	for name, proof := range receipt.Collections {
		if name == "" || strings.ContainsAny(name, "/\\\x00") {
			return errors.New("invalid full collection name")
		}
		if err = checkSnapshotManifest(proof, name); err != nil {
			return err
		}
		source := filepath.Join(keeper, "snapshots", "snapshot-"+name)
		if _, statErr := os.Lstat(source); os.IsNotExist(statErr) {
			// Read-only compatibility with full sets captured before full-only mode.
			source = filepath.Join(keeper, "snapshots", name)
		} else if statErr != nil {
			return statErr
		}
		actual, verifyErr := backupretention.Verify(source)
		if verifyErr != nil {
			return verifyErr
		}
		if actual.Object != proof.Object || !actual.CreatedAt.Equal(proof.CreatedAt) ||
			!slices.Equal(actual.Entries, proof.Entries) || !maps.Equal(actual.Metadata, proof.Metadata) {
			return errors.New("full collection proof differs from captured snapshot")
		}
		native := proof.Metadata["native_source"]
		if !strings.HasPrefix(native, name+"-") || strings.ContainsAny(native, "/\\\x00") || !nativeSnapshotTimestamp.MatchString(native) {
			return errors.New("invalid full native source")
		}
	}
	for name, proof := range receipt.Collections {
		native := proof.Metadata["native_source"]
		endpoint := qdrantREST + "/collections/" + url.PathEscape(name) + "/snapshots"
		var listed struct {
			Result []struct {
				Name string `json:"name"`
			} `json:"result"`
		}
		if err = httpDo(http.MethodGet, endpoint, nil, &listed); err != nil {
			return err
		}
		found := false
		for _, item := range listed.Result {
			found = found || item.Name == native
		}
		if !found {
			continue // idempotent cleanup retry
		}
		event := map[string]any{
			"action": "remove_full_native_source", "status": "started",
			"collection": name, "name": native, "snapshot_sha256": proof.Metadata["snapshot_sha256"],
		}
		if err = appendBackupRetentionAudit(event); err != nil {
			return err
		}
		var result struct {
			Status string `json:"status"`
			Result bool   `json:"result"`
		}
		if err = httpDo(http.MethodDelete, endpoint+"/"+url.PathEscape(native)+"?wait=true", nil, &result); err != nil {
			return err
		}
		if result.Status != "ok" || !result.Result {
			return errors.New("full native snapshot cleanup did not complete")
		}
		event["status"] = backupStatusComplete
		if err = appendBackupRetentionAudit(event); err != nil {
			return err
		}
	}
	return nil
}
