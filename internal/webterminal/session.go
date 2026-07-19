package webterminal

import (
	"context"
	"io"
)

type processSpec struct {
	Executable string
	Args       []string
	Dir        string
	Env        []string
	Cols       int
	Rows       int
}

type terminalProcess interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Wait() (int, error)
}

type processStarter func(context.Context, processSpec) (terminalProcess, error)
