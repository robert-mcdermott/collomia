package tools

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

var errReadLineTooLarge = errors.New("line exceeds the 1 MiB read_file page size")

// readFilePage bounds returned content independently of how far into the file
// the requested lines begin. Skipped lines use fixed memory, including huge
// lines; cancellation is checked on each read fragment.
func readFilePage(ctx context.Context, input io.Reader, offset, limit int) (string, error) {
	r := bufio.NewReaderSize(input, 64*1024)
	var out strings.Builder
	shown := 0
	for line := 1; ; line++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if shown == limit {
			if _, err := r.Peek(1); errors.Is(err, io.EOF) {
				return out.String() + "[read_file: EOF]\n", nil
			} else if err != nil {
				return "", err
			}
			return out.String() + fmt.Sprintf("[read_file: line limit reached; continue with offset=%d]\n", line), nil
		}
		text, present, err := readPageLine(ctx, r, line >= offset)
		if err != nil {
			if errors.Is(err, errReadLineTooLarge) {
				if shown > 0 {
					return out.String() + fmt.Sprintf("[read_file: page ended before oversized line %d; read offset=%d for details]\n", line, line), nil
				}
				return "", fmt.Errorf("line %d exceeds the 1 MiB read_file page size; inspect this line with a data-processing tool or skip it with offset=%d", line, line+1)
			}
			return "", err
		}
		if !present {
			if shown == 0 {
				return fmt.Sprintf("(no lines)\n[read_file: EOF before offset=%d]\n", offset), nil
			}
			return out.String() + "[read_file: EOF]\n", nil
		}
		if line < offset {
			continue
		}
		formatted := fmt.Sprintf("%6d\t%s\n", line, text)
		if len(formatted) > maxReadBytes {
			if shown > 0 {
				return out.String() + fmt.Sprintf("[read_file: page ended before oversized line %d; read offset=%d for details]\n", line, line), nil
			}
			return "", fmt.Errorf("line %d including its line number exceeds the 1 MiB read_file page size; use a data-processing tool or skip it with offset=%d", line, line+1)
		}
		if out.Len()+len(formatted) > maxReadBytes {
			return out.String() + fmt.Sprintf("[read_file: 1 MiB output limit reached; continue with offset=%d]\n", line), nil
		}
		out.WriteString(formatted)
		shown++
	}
}

func readPageLine(ctx context.Context, r *bufio.Reader, retain bool) ([]byte, bool, error) {
	var line []byte
	present := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		fragment, err := r.ReadSlice('\n')
		present = present || len(fragment) > 0
		if retain {
			if len(line)+len(fragment) > maxReadBytes+2 {
				return nil, false, errReadLineTooLarge
			}
			line = append(line, fragment...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, false, err
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		line = bytes.TrimSuffix(line, []byte{'\r'})
		return line, present, nil
	}
}
