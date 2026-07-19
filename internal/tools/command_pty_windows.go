//go:build windows

package tools

import (
	"context"
	"errors"
	"io"
)

// ptySupported reports whether this platform can run commands under a
// pseudo-terminal. Windows ConPTY support is future work.
const ptySupported = false

func runUnderPTY(context.Context, []string, string, []string, io.Writer) error {
	return errors.New("pty execution is not supported on Windows; run the command without pty")
}
