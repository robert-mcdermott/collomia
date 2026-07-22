package config

import (
	"encoding/json"
	"testing"
)

func FuzzConfigValidation(f *testing.F) {
	f.Add([]byte(`{"schema_version":1,"permissions":{"mode":"ask"}}`))
	f.Add([]byte(`{"providers":{"x":{"type":"openai","base_url":"http://localhost"}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 128<<10 {
			t.Skip()
		}
		var cfg Config
		if json.Unmarshal(data, &cfg) == nil {
			_ = cfg.ValidateFields()
		}
	})
}
