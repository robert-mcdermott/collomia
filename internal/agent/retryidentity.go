package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/robert-mcdermott/collomia/internal/provider"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

// Hash the complete operation arguments, not the human-facing summary. JSON
// key order/spacing is immaterial, but large numeric IDs must retain precision.
// Only the digest is kept in observations; payloads may contain private data.
func toolRetryKey(call provider.ToolCall) string {
	return canonicalRetryKey(call, true)
}

func canonicalRetryKey(call provider.ToolCall, normalizeTimeout bool) string {
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
	// The timeout is an execution allowance, not a different operation. A
	// successful retry still needs identical command and PTY. Verification
	// metadata describes evidence scope, not the executed operation.
	if normalizeTimeout && call.Name == "run_command" {
		if object, ok := args.(map[string]any); ok {
			delete(object, "timeout_seconds")
			delete(object, "verification")
		}
	}
	canonical, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append([]byte(call.Name+"\x00"), canonical...))
	return hex.EncodeToString(digest[:])
}

func priorScopedRetryKey(call provider.ToolCall) string {
	var args map[string]json.RawMessage
	if json.Unmarshal(call.Arguments, &args) != nil {
		return ""
	}
	delete(args, "timeout_seconds")
	raw, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	call.Arguments = raw
	return canonicalRetryKey(call, false)
}

func matchesRetry(failure unresolvedToolFailure, success toolObservation) bool {
	if failure.tool != success.Name {
		return false
	}
	// update_plan replaces one task-local board. A valid corrected update
	// repairs an invalid update; it never stands in for a workspace/tool effect.
	if success.Name == "update_plan" || failure.argumentRejected {
		return true
	}
	if success.Name == "validate_artifact" && !success.Failed && success.ArtifactValidation && success.ArtifactEvidence != nil && tools.ValidArtifactDigest(success.ArtifactEvidence.Digest) && success.ArtifactEvidence.ArtifactRoot.Valid() && success.ValidationRequest.Covers(failure.validationRequest) {
		return true
	}
	return failure.retryKey != "" && (failure.retryKey == success.RetryKey || failure.retryKey == success.LegacyRetryKey || failure.retryKey == success.PriorScopedRetryKey)
}
