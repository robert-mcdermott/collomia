package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robert-mcdermott/collomia/internal/permission"
)

type approvalEnvelope struct {
	request permission.Request
	reply   chan permission.Decision
}
type approvalMsg struct{ envelope approvalEnvelope }

// Question is a typed pause: the agent asks, the user answers, the run
// continues without ending the turn.
type Question struct {
	Text    string
	Options []string
}
type questionEnvelope struct {
	question Question
	reply    chan string
}
type questionMsg struct{ envelope questionEnvelope }

type ApprovalBroker struct {
	requests  chan approvalEnvelope
	questions chan questionEnvelope
}

func NewApprovalBroker() *ApprovalBroker {
	return &ApprovalBroker{requests: make(chan approvalEnvelope), questions: make(chan questionEnvelope)}
}

// Ask delivers a question to the TUI and blocks for the user's answer.
func (b *ApprovalBroker) Ask(ctx context.Context, question Question) (string, error) {
	reply := make(chan string, 1)
	env := questionEnvelope{question: question, reply: reply}
	select {
	case b.questions <- env:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case answer := <-reply:
		return answer, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (b *ApprovalBroker) Approve(ctx context.Context, request permission.Request) (permission.Decision, error) {
	reply := make(chan permission.Decision, 1)
	env := approvalEnvelope{request: request, reply: reply}
	select {
	case b.requests <- env:
	case <-ctx.Done():
		return permission.Decision{}, ctx.Err()
	}
	select {
	case decision := <-reply:
		return decision, nil
	case <-ctx.Done():
		return permission.Decision{}, ctx.Err()
	}
}
func (b *ApprovalBroker) wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case env := <-b.requests:
			return approvalMsg{envelope: env}
		case env := <-b.questions:
			return questionMsg{envelope: env}
		}
	}
}
