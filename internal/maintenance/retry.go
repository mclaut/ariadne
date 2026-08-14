// Package maintenance provides reusable, UI-independent maintenance
// orchestration primitives. Command packages supply the actual stages and
// activity reporting.
package maintenance

import (
	"context"
	"fmt"
	"time"
)

// RetryPolicy bounds one maintenance stage.
type RetryPolicy struct {
	Attempts int
	Delay    time.Duration
	MaxDelay time.Duration
	Timeout  time.Duration
}

// StageHooks supplies one stage attempt and optional lifecycle callbacks.
type StageHooks struct {
	Run       func(context.Context) error
	Sleep     func(context.Context, time.Duration) error
	Stop      func(error) bool
	OnAttempt func(attempt, maximum int)
	OnFailure func(attempt, maximum int, err error)
	OnRetry   func(attempt, maximum int, delay time.Duration, err error)
}

// ValidateRetryPolicy rejects unbounded or nonsensical stage policies.
func ValidateRetryPolicy(policy RetryPolicy) error {
	switch {
	case policy.Attempts < 1 || policy.Attempts > 10:
		return fmt.Errorf("attempts must be between 1 and 10")
	case policy.Delay < 0:
		return fmt.Errorf("retry-delay must not be negative")
	case policy.MaxDelay < 0:
		return fmt.Errorf("max-retry-delay must not be negative")
	case policy.Timeout <= 0:
		return fmt.Errorf("command-timeout must be positive")
	}
	return nil
}

// RunStage executes one stage with bounded exponential backoff. Stop classifies
// a non-retryable outcome such as a safely deferred consolidation.
func RunStage(ctx context.Context, policy RetryPolicy, hooks StageHooks) (int, error) {
	if err := ValidateRetryPolicy(policy); err != nil {
		return 0, err
	}
	if hooks.Run == nil {
		return 0, fmt.Errorf("stage runner is required")
	}
	sleep := hooks.Sleep
	if sleep == nil {
		sleep = SleepContext
	}
	var lastErr error
	for attempt := 1; attempt <= policy.Attempts; attempt++ {
		if hooks.OnAttempt != nil {
			hooks.OnAttempt(attempt, policy.Attempts)
		}
		attemptCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
		lastErr = hooks.Run(attemptCtx)
		cancel()
		if lastErr == nil {
			return attempt, nil
		}
		if hooks.Stop != nil && hooks.Stop(lastErr) {
			return attempt, lastErr
		}
		if hooks.OnFailure != nil {
			hooks.OnFailure(attempt, policy.Attempts, lastErr)
		}
		if attempt == policy.Attempts {
			break
		}
		delay := Backoff(policy.Delay, policy.MaxDelay, attempt)
		if hooks.OnRetry != nil {
			hooks.OnRetry(attempt, policy.Attempts, delay, lastErr)
		}
		if err := sleep(ctx, delay); err != nil {
			return attempt, err
		}
	}
	return policy.Attempts, lastErr
}

// Backoff returns a capped exponential delay for a failed attempt number.
func Backoff(initial, maximum time.Duration, failedAttempt int) time.Duration {
	delay := initial
	for i := 1; i < failedAttempt; i++ {
		if maximum > 0 && delay >= maximum/2 {
			return maximum
		}
		delay *= 2
	}
	if maximum > 0 && delay > maximum {
		return maximum
	}
	return delay
}

// SleepContext waits without losing cancellation responsiveness.
func SleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
