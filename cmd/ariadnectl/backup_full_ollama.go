package main

import (
	"ariadne/internal/processgroup"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func checkFullOllama(restored string, plan fullBackupPlan) (returnErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	port, err := unusedLoopbackPort(ctx)
	if err != nil {
		return err
	}
	home, err := os.MkdirTemp(filepath.Dir(restored), "ollama-check-")
	if err != nil {
		return err
	}
	//nolint:gosec // new private test workspace
	log, err := os.OpenFile(filepath.Join(home, "restore.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	binary := filepath.Join(restored, filepath.FromSlash(plan.OllamaBinary))
	command := exec.CommandContext(ctx, binary, "serve") //nolint:gosec // explicitly captured model runtime, separate loopback/storage/home
	command.Dir = home
	command.Stdout = log
	command.Stderr = log
	command.Env = []string{
		"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH"),
		"OLLAMA_HOST=127.0.0.1:" + strconv.Itoa(port), "OLLAMA_MODELS=" + filepath.Join(restored, filepath.FromSlash(plan.ModelsRoot)),
		"OLLAMA_NO_CLOUD=true", "OLLAMA_KEEP_ALIVE=0", "OLLAMA_MAX_LOADED_MODELS=1",
	}
	if root := os.Getenv("SystemRoot"); root != "" {
		command.Env = append(command.Env, "SystemRoot="+root)
	}
	processgroup.Configure(command)
	if err = command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	defer func() { cancel(); _ = command.Cancel(); <-done }()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: 3 * time.Minute, CheckRedirect: noQdrantRedirect}
	ready := false
	for range 300 {
		select {
		case err = <-done:
			done <- err
			return fmt.Errorf("isolated Ollama exited: %w", err)
		default:
		}
		_, err = isolatedJSON(ctx, client, base+"/api/tags", nil)
		if err == nil {
			ready = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ready {
		return errors.New("isolated Ollama did not become ready")
	}
	response, err := isolatedJSON(ctx, client, base+"/api/embed", map[string]any{
		"model": plan.EmbeddingModel, "input": "Ariadne isolated full recovery verification.", "keep_alive": 0,
	})
	if err != nil {
		return err
	}
	embeddings, ok := response["embeddings"].([]any)
	if !ok || len(embeddings) != 1 {
		return errors.New("restored embedding response missing")
	}
	vector, ok := embeddings[0].([]any)
	if !ok || len(vector) != plan.EmbeddingDimensions {
		return errors.New("restored embedding dimension mismatch")
	}
	for _, value := range vector {
		number, ok := value.(json.Number)
		if !ok {
			return errors.New("restored embedding value invalid")
		}
		f, parseErr := number.Float64()
		if parseErr != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return errors.New("restored embedding is not finite")
		}
	}
	return nil
}
