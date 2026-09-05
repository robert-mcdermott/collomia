package quality

import _ "embed"

//go:embed results-v1.schema.json
var resultSchema []byte

func JSONSchema() []byte { return append([]byte(nil), resultSchema...) }
