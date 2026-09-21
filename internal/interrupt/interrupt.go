// Package interrupt shares one cancel context between the TUI and subprocesses.
// Keyboard cancel and SIGINT both cancel this context. Compose and git derive
// their command contexts from it so an in-flight Apply can restore before exit.
package interrupt

import (
	"context"
	"sync"
)

var (
	mu     sync.Mutex
	parent = context.Background()
)

// Set installs the context canceled when the operator interrupts an operation.
func Set(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	mu.Lock()
	defer mu.Unlock()
	parent = ctx
}

// Clear restores the background context so later operations are not canceled.
func Clear() {
	Set(context.Background())
}

// Context returns the current interrupt parent.
func Context() context.Context {
	mu.Lock()
	defer mu.Unlock()
	return parent
}
