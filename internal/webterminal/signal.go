package webterminal

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

type terminationCause struct {
	signal syscall.Signal
}

func (e *terminationCause) Error() string { return "received " + e.signal.String() }

func withTerminationSignals(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case received := <-signals:
			if unixSignal, ok := received.(syscall.Signal); ok {
				cancel(&terminationCause{signal: unixSignal})
			} else {
				cancel(context.Canceled)
			}
		case <-ctx.Done():
		}
	}()
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			signal.Stop(signals)
			cancel(context.Canceled)
		})
	}
}

func terminationSignal(ctx context.Context) (syscall.Signal, bool) {
	var cause *terminationCause
	if errors.As(context.Cause(ctx), &cause) {
		return cause.signal, true
	}
	return 0, false
}
