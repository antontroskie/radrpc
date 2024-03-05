package utils

import (
	"context"
	"fmt"
	"time"
)

// RunWithTimeout runs a task with a timeout.
func RunWithTimeout[T any](task func() T, timeout time.Duration) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result := make(chan T, 1)
	go func() {
		result <- task()
	}()

	select {
	case res := <-result:
		return res, nil
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("error running task: %w", ctx.Err())
	}
}

// RetryOperation retries an operation a maximum of maxRetries times, waiting waitTime between each retry.
func RetryOperation(
	operation func() error,
	maxRetries int,
	waitTime time.Duration,
	handleError func(error),
) error {
	var err error

	tryOperation := func() error {
		if err = operation(); err != nil {
			handleError(err)
			return err
		}
		return nil
	}

	// We retry indefinitely if maxRetries is 0
	if maxRetries == 0 {
		for {
			if operationErr := tryOperation(); operationErr == nil {
				return nil
			}
			time.Sleep(waitTime)
		}
	} else {
		for i := 0; i < maxRetries; i++ {
			if operationErr := tryOperation(); operationErr == nil {
				return nil
			}
			time.Sleep(waitTime)
		}
	}
	return err // All retries failed
}
