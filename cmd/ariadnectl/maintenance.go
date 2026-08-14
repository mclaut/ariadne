package main

import (
	"ariadne/internal/activity"
	maintenancecore "ariadne/internal/maintenance"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

var errMaintenanceDeferred = errors.New("maintenance stage deferred")

const (
	defaultMaintenanceAttempts       = 3
	defaultMaintenanceRetryDelay     = 5 * time.Minute
	defaultMaintenanceMaxRetryDelay  = 30 * time.Minute
	defaultMaintenanceCommandTimeout = 90 * time.Minute
)

type maintenanceConfig struct {
	attempts       int
	retryDelay     time.Duration
	maxRetryDelay  time.Duration
	commandTimeout time.Duration
	before         time.Duration
	importPath     string
	ctlPath        string
}

type maintenanceDeps struct {
	run    func(context.Context, string, ...string) error
	sleep  func(context.Context, time.Duration) error
	append func(activity.Event) error
	now    func() time.Time
}

func maintenanceCmd(args []string) int {
	fs := flag.NewFlagSet("maintenance", flag.ContinueOnError)
	config := maintenanceConfig{}
	fs.IntVar(&config.attempts, "attempts", maintenanceAttemptsEnv(), "maximum attempts per failed stage")
	fs.DurationVar(&config.retryDelay, "retry-delay",
		durationEnv("ARIADNE_MAINTENANCE_RETRY_DELAY", defaultMaintenanceRetryDelay),
		"initial delay between retries")
	fs.DurationVar(&config.maxRetryDelay, "max-retry-delay",
		durationEnv("ARIADNE_MAINTENANCE_MAX_RETRY_DELAY", defaultMaintenanceMaxRetryDelay),
		"maximum delay between retries")
	fs.DurationVar(&config.commandTimeout, "command-timeout",
		durationEnv("ARIADNE_MAINTENANCE_COMMAND_TIMEOUT", defaultMaintenanceCommandTimeout),
		"timeout for one import or consolidation attempt")
	fs.DurationVar(&config.before, "before", 24*time.Hour, "minimum diary age to consolidate")
	fs.StringVar(&config.importPath, "import-path", defaultImportPath(), "path to the Ariadne importer")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := validateMaintenanceConfig(config); err != nil {
		fmt.Fprintln(os.Stderr, "maintenance:", err)
		return 2
	}
	config.ctlPath, _ = os.Executable()
	deps := maintenanceDeps{
		run:    runMaintenanceCommand,
		sleep:  maintenancecore.SleepContext,
		append: activity.Append,
		now:    time.Now,
	}
	if err := runMaintenance(context.Background(), config, deps); err != nil {
		fmt.Fprintln(os.Stderr, "maintenance:", err)
		return 1
	}
	return 0
}

func validateMaintenanceConfig(config maintenanceConfig) error {
	if err := maintenancecore.ValidateRetryPolicy(maintenancecore.RetryPolicy{
		Attempts: config.attempts, Delay: config.retryDelay,
		MaxDelay: config.maxRetryDelay, Timeout: config.commandTimeout,
	}); err != nil {
		return err
	}
	switch {
	case config.before <= 0:
		return fmt.Errorf("before must be positive")
	case config.importPath == "":
		return fmt.Errorf("import-path is required")
	}
	return nil
}

func maintenanceAttemptsEnv() int {
	value := os.Getenv("ARIADNE_MAINTENANCE_ATTEMPTS")
	if value == "" {
		return defaultMaintenanceAttempts
	}
	attempts, err := strconv.Atoi(value)
	if err != nil || attempts < 1 || attempts > 10 {
		return defaultMaintenanceAttempts
	}
	return attempts
}

func defaultImportPath() string {
	if configured := os.Getenv("ARIADNE_IMPORT_PATH"); configured != "" {
		return configured
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ariadne", "bin", "import")
}

func runMaintenance(ctx context.Context, config maintenanceConfig, deps maintenanceDeps) error {
	if err := appendMaintenanceEvent(deps, "running", "maintenance started", map[string]int64{
		"max_attempts": int64(config.attempts),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "maintenance activity:", err)
	}

	importAttempts, err := runMaintenanceStage(ctx, "memfile_sync", config, deps,
		config.importPath, "-source", "memfiles", "-sync")
	if err != nil {
		_ = appendMaintenanceEvent(deps, "failed", "memfile sync failed after bounded retries", map[string]int64{
			"import_attempts": int64(importAttempts),
		})
		return fmt.Errorf("memfile sync failed after %d attempt(s): %w", importAttempts, err)
	}

	consolidateAttempts, err := runMaintenanceStage(ctx, "consolidate", config, deps,
		config.ctlPath, "consolidate", "--before", config.before.String())
	if err != nil {
		if errors.Is(err, errMaintenanceDeferred) {
			_ = appendMaintenanceEvent(deps, "complete_with_deferred",
				"maintenance completed; unchanged unsafe groups were deferred for this pipeline revision", map[string]int64{
					"import_attempts":      int64(importAttempts),
					"consolidate_attempts": int64(consolidateAttempts),
					"deferred_stages":      1,
				})
			return nil
		}
		_ = appendMaintenanceEvent(deps, "failed", "consolidation failed after bounded retries", map[string]int64{
			"import_attempts":      int64(importAttempts),
			"consolidate_attempts": int64(consolidateAttempts),
		})
		return fmt.Errorf("consolidation failed after %d attempt(s): %w", consolidateAttempts, err)
	}

	if err := appendMaintenanceEvent(deps, "complete", "", map[string]int64{
		"import_attempts":      int64(importAttempts),
		"consolidate_attempts": int64(consolidateAttempts),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "maintenance activity:", err)
	}
	return nil
}

func runMaintenanceStage(
	ctx context.Context,
	stage string,
	config maintenanceConfig,
	deps maintenanceDeps,
	path string,
	args ...string,
) (int, error) {
	return maintenancecore.RunStage(ctx, maintenancecore.RetryPolicy{
		Attempts: config.attempts, Delay: config.retryDelay,
		MaxDelay: config.maxRetryDelay, Timeout: config.commandTimeout,
	}, maintenancecore.StageHooks{
		Run:   func(attemptCtx context.Context) error { return deps.run(attemptCtx, path, args...) },
		Sleep: deps.sleep,
		Stop:  func(err error) bool { return errors.Is(err, errMaintenanceDeferred) },
		OnAttempt: func(attempt, maximum int) {
			_ = appendMaintenanceEvent(deps, "running", stage+" attempt in progress", map[string]int64{
				"attempt": int64(attempt), "max_attempts": int64(maximum),
			})
		},
		OnFailure: func(attempt, maximum int, err error) {
			fmt.Fprintf(os.Stderr, "maintenance: %s attempt %d/%d failed: %v\n", stage, attempt, maximum, err)
		},
		OnRetry: func(attempt, maximum int, delay time.Duration, err error) {
			_ = appendMaintenanceEvent(deps, "retrying", stage+" failed; retry scheduled", map[string]int64{
				"attempt": int64(attempt), "max_attempts": int64(maximum),
				"retry_delay_seconds": int64(delay / time.Second),
			})
		},
	})
}

func appendMaintenanceEvent(
	deps maintenanceDeps,
	statusValue, message string,
	counters map[string]int64,
) error {
	return deps.append(activity.Event{
		At: deps.now(), Operation: "maintenance", Status: statusValue,
		Message: message, Counters: counters,
	})
}

func runMaintenanceCommand(ctx context.Context, path string, args ...string) error {
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // caller selects installed Ariadne binaries only
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == consolidateDeferredExitCode {
		return fmt.Errorf("%w: %w", errMaintenanceDeferred, err)
	}
	return err
}
