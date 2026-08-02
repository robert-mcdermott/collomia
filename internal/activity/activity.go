// Package activity projects the stable runtime event stream into a bounded,
// presentation-neutral operator timeline. It deliberately contains no TUI
// state: terminal, browser, and future frontends can render the same items.
package activity

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/robert-mcdermott/collomia/internal/event"
)

const (
	// DefaultLimit bounds the operator timeline even when a durable session is
	// much larger. The complete event log remains on disk.
	DefaultLimit   = 500
	maxDetailRunes = 600
)

type Category string

const (
	CategorySession    Category = "session"
	CategoryTurn       Category = "turn"
	CategoryTool       Category = "tool"
	CategoryPermission Category = "permission"
	CategoryFile       Category = "file"
	CategoryPlan       Category = "plan"
	CategoryAgent      Category = "agent"
	CategoryContext    Category = "context"
	CategoryFailure    Category = "failure"
)

// Categories is the stable display order used by filtering frontends.
var Categories = []Category{
	CategorySession,
	CategoryTurn,
	CategoryTool,
	CategoryPermission,
	CategoryFile,
	CategoryPlan,
	CategoryAgent,
	CategoryContext,
	CategoryFailure,
}

type Status string

const (
	StatusInfo    Status = "info"
	StatusActive  Status = "active"
	StatusSuccess Status = "success"
	StatusWarning Status = "warning"
	StatusError   Status = "error"
)

// Item is one bounded, read-only activity entry derived from a runtime event.
// FailureID is safe to copy into support reports; Detail may contain normal
// user/session content and must pass through the active redactor before display.
type Item struct {
	Time      time.Time
	Turn      int
	Category  Category
	Status    Status
	Title     string
	Detail    string
	FailureID string
}

// Project converts events into their operator-visible subset and retains only
// the newest limit entries. A non-positive limit selects DefaultLimit.
func Project(events []event.Event, limit int) []Item {
	if limit <= 0 {
		limit = DefaultLimit
	}
	out := make([]Item, 0, min(len(events), limit))
	next := 0
	for _, e := range events {
		item, ok := FromEvent(e)
		if !ok {
			continue
		}
		if len(out) < limit {
			out = append(out, item)
			continue
		}
		out[next] = item
		next = (next + 1) % limit
	}
	if len(out) == limit && next > 0 {
		ordered := make([]Item, 0, limit)
		ordered = append(ordered, out[next:]...)
		ordered = append(ordered, out[:next]...)
		return ordered
	}
	return out
}

// Append projects one event and applies the same bound used by Project.
func Append(items []Item, e event.Event, limit int) []Item {
	if limit <= 0 {
		limit = DefaultLimit
	}
	item, ok := FromEvent(e)
	if !ok {
		return items
	}
	if len(items) >= limit {
		copy(items, items[len(items)-limit+1:])
		items = items[:limit]
		items[limit-1] = item
		return items
	}
	return append(items, item)
}

// FromEvent maps one runtime event. Streaming deltas, usage samples, tool
// argument fragments, and permission requests are excluded because their
// completed counterparts are more useful and avoid a noisy timeline.
func FromEvent(e event.Event) (Item, bool) {
	item := Item{Time: e.Time, Turn: e.Turn, FailureID: e.FailureID}
	switch e.Kind {
	case event.KindSessionStart:
		item.Category, item.Status, item.Title = CategorySession, StatusSuccess, "Session started"
		item.Detail = e.Text
	case event.KindTurnStart:
		item.Category, item.Status, item.Title = CategoryTurn, StatusActive, turnTitle("Turn started", e.Turn)
	case event.KindTurnEnd:
		item.Category, item.Status, item.Title = CategoryTurn, StatusSuccess, turnTitle("Turn completed", e.Turn)
	case event.KindToolStart:
		if e.Tool == nil {
			return Item{}, false
		}
		item.Category, item.Status = CategoryTool, StatusActive
		item.Title, item.Detail = e.Tool.Name+" started", e.Tool.Summary
	case event.KindToolResult:
		if e.Tool == nil {
			return Item{}, false
		}
		item.Category, item.Status = CategoryTool, StatusSuccess
		item.Title, item.Detail = e.Tool.Name+" completed", e.Tool.Summary
		if e.Tool.IsError {
			item.Status = StatusError
			item.Title = e.Tool.Name + " failed"
			if item.Detail == "" {
				item.Detail = e.Tool.Output
			}
		}
	case event.KindPermissionDecision:
		if e.Permission == nil {
			return Item{}, false
		}
		item.Category, item.Status = CategoryPermission, StatusWarning
		decision := "denied"
		if e.Permission.Allowed {
			decision, item.Status = "allowed", StatusSuccess
		}
		item.Title = strings.TrimSpace(e.Permission.Tool + " " + decision)
		item.Detail = e.Permission.Summary
		if e.Permission.Source != "" {
			item.Detail = strings.TrimSpace(item.Detail + " · via " + e.Permission.Source)
		}
	case event.KindFileChange:
		if e.File == nil {
			return Item{}, false
		}
		item.Category, item.Status = CategoryFile, StatusSuccess
		item.Title = strings.TrimSpace(e.File.Operation + " " + e.File.Path)
	case event.KindPlanUpdate:
		item.Category, item.Status, item.Title = CategoryPlan, StatusInfo, "Plan updated"
		item.Detail = e.Text
	case event.KindDelegateUpdate:
		if e.Delegate == nil {
			return Item{}, false
		}
		item.Category = CategoryAgent
		item.Status = delegateStatus(e.Delegate.Status)
		item.Title = strings.TrimSpace(e.Delegate.Name + " · " + e.Delegate.Status)
		item.Detail = firstNonEmpty(e.Delegate.CurrentAction, e.Delegate.Summary, e.Delegate.Error)
		if item.FailureID == "" {
			item.FailureID = e.Delegate.FailureID
		}
	case event.KindCompaction:
		item.Category, item.Status, item.Title = CategoryContext, StatusInfo, "Context compacted"
		item.Detail = e.Text
	case event.KindWarning:
		item.Category, item.Status, item.Title = CategoryFailure, StatusWarning, "Warning"
		item.Detail = e.Text
	case event.KindError:
		item.Category, item.Status, item.Title = CategoryFailure, StatusError, "Run failed"
		item.Detail = e.Error
	case event.KindRunResult:
		if e.Result == nil {
			return Item{}, false
		}
		terminal := e.Result.Status
		if e.Result.Outcome != "" {
			terminal = e.Result.Outcome
		}
		item.Category, item.Title = CategoryTurn, "Run "+terminal
		item.Status = StatusSuccess
		if e.Result.Status == "cancelled" {
			item.Status = StatusWarning
		} else if e.Result.Status != "ok" {
			item.Status = StatusError
		}
		item.Detail = e.Result.Error
		if item.FailureID == "" && e.Result.Failure != nil {
			item.FailureID = e.Result.Failure.ID
		}
	default:
		return Item{}, false
	}
	item.Title = boundedText(item.Title)
	item.Detail = boundedText(item.Detail)
	return item, true
}

func turnTitle(prefix string, turn int) string {
	if turn <= 0 {
		return prefix
	}
	return fmt.Sprintf("%s · %d", prefix, turn)
}

func delegateStatus(status string) Status {
	switch status {
	case "done":
		return StatusSuccess
	case "error", "timed-out", "budget-exhausted", "interrupted":
		return StatusError
	case "cancelled", "cancelling", "waiting-approval":
		return StatusWarning
	case "queued", "running":
		return StatusActive
	default:
		return StatusInfo
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boundedText(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) <= maxDetailRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxDetailRunes-1]) + "…"
}
