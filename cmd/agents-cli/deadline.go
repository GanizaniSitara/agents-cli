package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

var operationContext = context.Background()

// The watchdog is deliberately process-wide: a stuck Windows console syscall
// cannot be interrupted by checking a context inside the blocked goroutine.
func runWithDeadline(args []string, dispatch func([]string) error) error {
	limit, err := commandLimit(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	operationContext = ctx
	defer func() { operationContext = context.Background() }()
	watchdog := time.AfterFunc(limit, func() {
		verb := "help"
		if len(args) > 0 {
			verb = args[0]
		}
		message := fmt.Sprintf("%s exceeded its overall deadline of %s", verb, limit)
		if verb == "send" {
			message += "; input may already be delivered; inspect tail before retrying"
		}
		emit(map[string]interface{}{"error": map[string]string{
			"code":    "agents_command_timeout",
			"message": message,
		}})
		os.Exit(1)
	})
	defer watchdog.Stop()
	return dispatch(args)
}

func commandLimit(args []string) (time.Duration, error) {
	limit := 30 * time.Second
	if len(args) == 0 {
		return limit, nil
	}
	switch args[0] {
	case "_attach":
		return 15 * time.Second, nil
	case "ls":
		limit = 60 * time.Second
	case "wait":
		limit = 10 * time.Minute
	case "tail", "send", "key":
	default:
		return limit, nil
	}
	p, err := parseArgs(args[1:], "all", "fast", "no-enter", "force", "stdin", "allow-argv")
	if err != nil {
		return 0, err
	}
	limit, err = p.durationFlag("timeout", limit)
	if err != nil {
		return 0, err
	}
	if limit <= 0 {
		return 0, fmt.Errorf("--timeout must be positive")
	}
	// wait emits its normal reason:timeout receipt at its requested deadline.
	// The independent watchdog permits one second to write that receipt.
	if args[0] == "wait" {
		limit += time.Second
	}
	return limit, nil
}
