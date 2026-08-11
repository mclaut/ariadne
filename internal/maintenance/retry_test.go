package maintenance

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestRunStageRetriesWithBoundedBackoff(t *testing.T) {
	var attempts int
	var delays []time.Duration
	got, err := RunStage(context.Background(), RetryPolicy{
		Attempts: 4, Delay: 5 * time.Minute, MaxDelay: 12 * time.Minute, Timeout: time.Minute,
	}, StageHooks{
		Run: func(context.Context) error {
			attempts++
			if attempts < 4 {
				return errors.New("temporary")
			}
			return nil
		},
		Sleep: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
	})
	if err != nil || got != 4 || !slices.Equal(delays, []time.Duration{
		5 * time.Minute, 10 * time.Minute, 12 * time.Minute,
	}) {
		t.Fatalf("attempts=%d delays=%v err=%v", got, delays, err)
	}
}

func TestRunStageStopsWithoutRetry(t *testing.T) {
	deferred := errors.New("deferred")
	attempts, err := RunStage(context.Background(), RetryPolicy{
		Attempts: 3, Timeout: time.Minute,
	}, StageHooks{
		Run:  func(context.Context) error { return deferred },
		Stop: func(err error) bool { return errors.Is(err, deferred) },
	})
	if attempts != 1 || !errors.Is(err, deferred) {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestRunStageReportsEveryRetryableFailureIncludingFinal(t *testing.T) {
	var failures []int
	attempts, err := RunStage(context.Background(), RetryPolicy{
		Attempts: 2, Timeout: time.Minute,
	}, StageHooks{
		Run: func(context.Context) error { return errors.New("unavailable") },
		OnFailure: func(attempt, _ int, _ error) {
			failures = append(failures, attempt)
		},
	})
	if attempts != 2 || err == nil || !slices.Equal(failures, []int{1, 2}) {
		t.Fatalf("attempts=%d failures=%v err=%v", attempts, failures, err)
	}
}

func TestValidateRetryPolicy(t *testing.T) {
	valid := RetryPolicy{Attempts: 3, Delay: time.Second, MaxDelay: time.Minute, Timeout: time.Minute}
	if err := ValidateRetryPolicy(valid); err != nil {
		t.Fatal(err)
	}
	valid.Attempts = 0
	if err := ValidateRetryPolicy(valid); err == nil {
		t.Fatal("zero attempts accepted")
	}
}
