package event

import _ "embed"

// eventsV1Schema is the published JSON Schema for Event's schema-v1 wire
// format. Keeping it embedded makes the contract available from the same
// single binary that emits the events.
//
//go:embed schema/events-v1.schema.json
var eventsV1Schema []byte

// JSONSchema returns a defensive copy of the schema-v1 event contract.
func JSONSchema() []byte {
	return append([]byte(nil), eventsV1Schema...)
}
