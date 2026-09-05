package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/robert-mcdermott/collomia/internal/provider"
)

// Hash the complete operation arguments, not the human-facing summary. JSON
// key order/spacing is immaterial, but large numeric IDs must retain precision.
// Only the digest is kept in observations; payloads may contain private data.
func toolRetryKey(call provider.ToolCall) string {
	decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
	decoder.UseNumber()
	var args any
	if decoder.Decode(&args) != nil {
		return ""
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return ""
	}
	canonical, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append([]byte(call.Name+"\x00"), canonical...))
	return hex.EncodeToString(digest[:])
}

func matchesRetry(failure unresolvedToolFailure, success toolObservation) bool {
	if failure.tool != success.Name {
		return false
	}
	// update_plan replaces one task-local board. A valid corrected update
	// repairs an invalid update; it never stands in for a workspace/tool effect.
	if success.Name == "update_plan" {
		return true
	}
	return failure.retryKey != "" && failure.retryKey == success.RetryKey
}
