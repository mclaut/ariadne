package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type snapshotProof struct {
	SchemaSHA256 string
	Points       int64
	PointsSHA256 string
	Log          []byte
}

func unusedLoopbackPort(ctx context.Context) (int, error) {
	var listen net.ListenConfig
	listener, err := listen.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	return port, listener.Close()
}

func verifySnapshotRestore(snapshot, home string) (proof snapshotProof, returnErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	binary := filepath.Join(home, ".ariadne", "bin", "qdrant")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if _, err := os.Stat(binary); err != nil {
		return proof, fmt.Errorf("local Qdrant required to verify recovery: %w", err)
	}
	state := filepath.Join(home, ".ariadne", "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		return proof, err
	}
	work, err := os.MkdirTemp(state, "backup-verify-")
	if err != nil {
		return proof, err
	}
	// This workspace contains only a derivative of the snapshot retained by the caller.
	defer func() {
		if err := os.RemoveAll(work); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	input := filepath.Join(work, "input.snapshot")
	before, err := os.Lstat(snapshot)
	if err != nil {
		return proof, err
	}
	if !before.Mode().IsRegular() {
		return proof, errors.New("verification input must be a regular snapshot")
	}
	if err = os.Link(snapshot, input); err != nil {
		if err = copyVerificationInput(snapshot, input, before); err != nil {
			return proof, err
		}
	} else {
		linked, statErr := os.Lstat(input)
		if statErr != nil {
			return proof, statErr
		}
		if !linked.Mode().IsRegular() || !os.SameFile(before, linked) {
			return proof, errors.New("snapshot input changed before verification")
		}
	}
	storage := filepath.Join(work, "storage")
	if err = os.MkdirAll(filepath.Join(storage, "collections"), 0o700); err != nil {
		return proof, err
	}
	hp, err := unusedLoopbackPort(ctx)
	if err != nil {
		return proof, err
	}
	gp, err := unusedLoopbackPort(ctx)
	if err != nil {
		return proof, err
	}
	for gp == hp {
		gp, err = unusedLoopbackPort(ctx)
		if err != nil {
			return proof, err
		}
	}
	config := map[string]any{
		"storage": map[string]any{
			"storage_path": storage, "snapshots_path": filepath.Join(work, "snapshots"), "temp_path": filepath.Join(work, "tmp"),
		},
		"service": map[string]any{"host": "127.0.0.1", "http_port": hp, "grpc_port": gp},
		"cluster": map[string]any{"enabled": false}, "telemetry_disabled": true, "log_level": "WARN",
	}
	data, err := json.Marshal(config)
	if err != nil {
		return proof, err
	}
	cfg := filepath.Join(work, "qdrant.yaml")
	// JSON is valid YAML and quotes platform-specific paths unambiguously.
	if err = os.WriteFile(cfg, data, 0o600); err != nil {
		return proof, err
	}
	logPath := filepath.Join(work, "restore.log")
	//nolint:gosec // exclusive log inside a newly created private verification workspace
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return proof, err
	}
	defer func() { _ = log.Close() }()
	//nolint:gosec // fixed installed verifier binary; all data/config paths are isolated
	cmd := exec.CommandContext(ctx, binary, "--config-path", cfg, "--snapshot", "input.snapshot:verified", "--disable-telemetry")
	cmd.Dir = work
	cmd.Stdout = log
	cmd.Stderr = log
	// Never inherit QDRANT__STORAGE_* or other live-service overrides.
	for _, name := range []string{"PATH", "SystemRoot", "TEMP", "TMP", "LANG"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	if err = cmd.Start(); err != nil {
		return proof, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Kill()
		waitErr := <-done
		if ctx.Err() != nil {
			returnErr = errors.Join(returnErr, ctx.Err())
		}
		// The test process is intentionally stopped after verified reads.
		_ = waitErr
		proof.Log, _ = os.ReadFile(logPath) //nolint:gosec // private verification log
	}()
	base := "http://127.0.0.1:" + strconv.Itoa(hp)
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: noQdrantRedirect}
	ready := false
	for range 300 {
		select {
		case err = <-done:
			done <- err
			return proof, fmt.Errorf("isolated Qdrant exited before verification: %w", err)
		default:
		}
		request, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
		if reqErr != nil {
			return proof, reqErr
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		select {
		case <-ctx.Done():
			return proof, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		return proof, errors.New("isolated Qdrant did not become ready")
	}
	proof.Points, proof.PointsSHA256, err = verifyRestoredPoints(ctx, client, base)
	if err != nil {
		return proof, err
	}
	info, err := isolatedJSON(ctx, client, base+"/collections/verified", nil)
	if err != nil {
		return proof, err
	}
	proof.SchemaSHA256, err = vectorSchemaDigest(info)
	return proof, err
}

func verifyRestoredPoints(ctx context.Context, client *http.Client, base string) (int64, string, error) {
	countBody, err := isolatedJSON(ctx, client, base+"/collections/verified/points/count", map[string]any{"exact": true})
	if err != nil {
		return 0, "", err
	}
	countString, _ := mapPathNumber(countBody, "result", "count")
	count, err := strconv.ParseInt(countString, 10, 64)
	if err != nil {
		return 0, "", err
	}
	info, err := isolatedJSON(ctx, client, base+"/collections/verified", nil)
	if err != nil {
		return 0, "", err
	}
	if status, _ := mapPath(info, "result", "status"); status != "green" {
		return 0, "", errors.New("restored collection is not green")
	}
	h := sha256.New()
	seen := make(map[string]bool)
	var offset any
	var total int64
	for {
		body := map[string]any{"limit": 256, "with_payload": true, "with_vector": true}
		if offset != nil {
			body["offset"] = offset
		}
		page, pageErr := isolatedJSON(ctx, client, base+"/collections/verified/points/scroll", body)
		if pageErr != nil {
			return 0, "", pageErr
		}
		result, ok := page["result"].(map[string]any)
		if !ok {
			return 0, "", errors.New("invalid restored point page")
		}
		points, ok := result["points"].([]any)
		if !ok {
			return 0, "", errors.New("missing restored points")
		}
		for _, item := range points {
			point, ok := item.(map[string]any)
			if !ok {
				return 0, "", errors.New("invalid restored point")
			}
			id, ok := point["id"]
			if !ok {
				return 0, "", errors.New("missing restored point ID")
			}
			idData, marshalErr := json.Marshal(id)
			if marshalErr != nil {
				return 0, "", marshalErr
			}
			key := string(idData)
			if seen[key] {
				return 0, "", errors.New("duplicate restored point")
			}
			seen[key] = true
			payload, payloadOK := point["payload"]
			vector, vectorOK := point["vector"]
			if !payloadOK || !vectorOK {
				return 0, "", errors.New("incomplete restored point")
			}
			canonical, marshalErr := json.Marshal(map[string]any{"id": id, "payload": payload, "vector": vector})
			if marshalErr != nil {
				return 0, "", marshalErr
			}
			_, _ = h.Write(canonical)
			_, _ = h.Write([]byte{'\n'})
			total++
		}
		offset = result["next_page_offset"]
		if offset == nil {
			break
		}
		if len(points) == 0 {
			return 0, "", errors.New("non-progressing restored point pagination")
		}
	}
	if total != count {
		return 0, "", errors.New("restored count does not match readable points")
	}
	return count, hex.EncodeToString(h.Sum(nil)), nil
}

func isolatedJSON(ctx context.Context, client *http.Client, url string, body any) (map[string]any, error) {
	method := http.MethodGet
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = strings.NewReader(string(data))
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("isolated verification HTTP %d", response.StatusCode)
	}
	var result map[string]any
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	err = decoder.Decode(&result)
	return result, err
}

func mapPathNumber(m map[string]any, keys ...string) (string, bool) {
	var current any = m
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current = object[key]
	}
	number, ok := current.(json.Number)
	return number.String(), ok
}

func vectorSchemaDigest(info map[string]any) (string, error) {
	result, ok := info["result"].(map[string]any)
	if !ok {
		return "", errors.New("missing collection result")
	}
	config, ok := result["config"].(map[string]any)
	if !ok {
		return "", errors.New("missing collection configuration")
	}
	params, ok := config["params"].(map[string]any)
	if !ok {
		return "", errors.New("missing collection parameters")
	}
	data, err := json.Marshal(map[string]any{"vectors": params["vectors"], "sparse_vectors": params["sparse_vectors"]})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func copyVerificationInput(source, destination string, before os.FileInfo) error {
	in, err := os.Open(source) //nolint:gosec // identity-pinned staged snapshot
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	opened, err := in.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, opened) {
		return errors.New("snapshot replaced before verification copy")
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // private verifier workspace
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, in)
	syncErr := out.Sync()
	closeErr := out.Close()
	if err = errors.Join(copyErr, syncErr, closeErr); err != nil {
		return err
	}
	after, err := in.Stat()
	if err != nil {
		return err
	}
	if n != before.Size() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return errors.New("snapshot changed during verification copy")
	}
	return nil
}
