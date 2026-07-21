package tui

import appconfig "github.com/robert-mcdermott/collomia/internal/config"

func (m Model) binding(action string) string {
	if key := m.runtime.Config.Options.Keybindings[action]; key != "" {
		return key
	}
	return appconfig.DefaultKeybindings()[action]
}

func (m Model) keyIs(action, key string) bool {
	return key == m.binding(action)
}
