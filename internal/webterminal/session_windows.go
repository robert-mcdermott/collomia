//go:build windows

package webterminal

import (
	"context"
	"errors"
)

const platformPTYSupported = false

func startPTY(context.Context, processSpec) (terminalProcess, error) {
	return nil, errors.New("web terminal mode is not supported on Windows until a ConPTY backend is implemented")
}
