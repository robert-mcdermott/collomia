package event

import "strings"

// CompletionNoticeSummary recognizes the stable controller diagnostic preamble
// for presentation only, including older session messages. The full text remains
// in events and model context; this grants no authority or completion evidence.
func CompletionNoticeSummary(text string) string {
	if strings.HasPrefix(text, "Collomia completion controller (intervention ") && strings.Contains(text, "): this response cannot finish the turn yet.\nRecorded gaps:\n") {
		return "Checking remaining work."
	}
	return ""
}
