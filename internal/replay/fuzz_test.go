package replay

import (
	"strings"
	"testing"
)

func FuzzRead(f *testing.F) {
	f.Add([]byte(`{"schema":1,"time":"2026-07-21T12:00:00Z","kind":"run.result","result":{"status":"ok","duration_ms":1}}` + "\n"))
	f.Add([]byte("not json\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256<<10 {
			t.Skip()
		}
		_, _ = Read(strings.NewReader(string(data)))
	})
}
