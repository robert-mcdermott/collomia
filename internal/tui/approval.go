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

type ApprovalBroker struct{ requests chan approvalEnvelope }

func NewApprovalBroker() *ApprovalBroker {
	return &ApprovalBroker{requests: make(chan approvalEnvelope)}
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
	return func() tea.Msg { return approvalMsg{envelope: <-b.requests} }
}
