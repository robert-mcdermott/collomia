package permission

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	appconfig "github.com/robert-mcdermott/collomia/internal/config"
	"github.com/robert-mcdermott/collomia/internal/tools"
)

var ErrDenied = errors.New("permission denied")

type Request struct {
	Tool   string
	Action tools.Action
}

type Decision struct {
	Allow  bool
	Always bool
}

type Approver func(context.Context, Request) (Decision, error)

type Manager struct {
	mu           sync.RWMutex
	mode         string
	allowed      map[string]bool
	denied       map[string]bool
	allowOutside bool
	approver     Approver
}

func New(cfg appconfig.Permissions, approver Approver) *Manager {
	allowed := map[string]bool{}
	for _, name := range cfg.AllowedTools {
		allowed[name] = true
	}
	denied := map[string]bool{}
	for _, name := range cfg.DeniedTools {
		denied[name] = true
	}
	return &Manager{mode: cfg.Mode, allowed: allowed, denied: denied, allowOutside: cfg.AllowOutsideWorkspace, approver: approver}
}

func (m *Manager) Mode() string { m.mu.RLock(); defer m.mu.RUnlock(); return m.mode }
func (m *Manager) SetMode(mode string) error {
	if !slices.Contains([]string{"ask", "workspace", "autopilot"}, mode) {
		return fmt.Errorf("unknown autonomy mode %q", mode)
	}
	m.mu.Lock()
	m.mode = mode
	m.mu.Unlock()
	return nil
}

func (m *Manager) Authorize(ctx context.Context, tool string, action tools.Action) error {
	m.mu.RLock()
	mode := m.mode
	allowed := m.allowed[tool]
	denied := m.denied[tool]
	approver := m.approver
	m.mu.RUnlock()
	if denied {
		return fmt.Errorf("%w: tool %s is disabled", ErrDenied, tool)
	}
	if allowed {
		return nil
	}
	if action.Risk == tools.RiskRead && !action.Outside {
		return nil
	}
	if mode == "autopilot" && action.Outside && m.allowOutside && action.Risk != tools.RiskExternal {
		return nil
	}
	if mode == "autopilot" && !action.Outside && action.Risk != tools.RiskExternal {
		return nil
	}
	if mode == "workspace" && !action.Outside && action.Risk == tools.RiskWrite {
		return nil
	}
	if approver == nil {
		return fmt.Errorf("%w: %s requires interactive approval", ErrDenied, action.Summary)
	}
	decision, err := approver(ctx, Request{Tool: tool, Action: action})
	if err != nil {
		return err
	}
	if !decision.Allow {
		return fmt.Errorf("%w: %s", ErrDenied, action.Summary)
	}
	if decision.Always {
		m.mu.Lock()
		m.allowed[tool] = true
		m.mu.Unlock()
	}
	return nil
}
