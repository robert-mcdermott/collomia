// Package prompts holds Collomia's model-facing instruction text as embedded
// templates rather than Go string literals, so prompt wording can be reviewed
// and edited as prose without reading Go, and a prompt change shows up in a
// diff as a prose change rather than a code change.
//
// The templates are compiled into the binary with go:embed; there is
// deliberately no way to override them from disk at runtime. The base prompt
// carries Collomia's injection-resistance rule, and a file-based override
// would let anything able to write a file quietly delete that rule. Operator
// customization belongs in the sanctioned layers that this package composes
// on top of the base prompt: project instructions and agent profile
// instructions.
//
// Structural whitespace between fragments stays in Go, at the call sites that
// assemble them. Only prose lives in the templates, and every template
// delimits its own leading and trailing whitespace with "-" trim markers so
// an editor that strips or adds trailing newlines cannot shift the prompt
// prefix (which would change token counts and invalidate provider prompt
// caching).
package prompts

import (
	"embed"
	"strings"
	"text/template"
)

//go:embed templates/*.md
var files embed.FS

// Fragment names available to Render and Text. Referencing an unknown name,
// or a field a template does not declare, is a programmer error rather than a
// runtime condition: the templates are compiled in and cannot vary at run
// time, and TestEveryTemplateRenders exercises all of them.
const (
	System                = "system"
	ModeExecution         = "mode.execution"
	ModePlanning          = "mode.planning"
	SubagentResearch      = "subagent.research"
	SubagentImplement     = "subagent.implementation"
	ProfileInstructions   = "profile.instructions"
	PinnedState           = "pinned.state"
	CompactSystem         = "compact.system"
	CompactInstructions   = "compact.instructions"
	CompactFocus          = "compact.focus"
	DelegateRole          = "delegate.role"
	DelegateWriteContract = "delegate.write_contract"
	SkillsHeader          = "skills.header"
	SkillsEmpty           = "skills.empty"
)

// A malformed embedded template is a build defect, not a runtime condition,
// so it fails loudly at process start in the same way regexp.MustCompile does
// for a bad pattern.
var tmpl = template.Must(template.New("prompts").ParseFS(files, "templates/*.md"))

// SystemView supplies the main system prompt's substitutions. Named fields
// replace what used to be eight positional fmt.Sprintf arguments, so adding
// or reordering a substitution can no longer silently misalign the rest.
type SystemView struct {
	Workspace string
	OS        string
	Arch      string
	// Mode is a rendered ModeExecution or ModePlanning fragment.
	Mode string
	// Subagent is a rendered subagent fragment including its leading
	// newline, or empty for a top-level agent.
	Subagent string
	// ProfileInstructions, ProjectInstructions and PinnedState are already
	// wrapped in their headers and separators, or empty when unset.
	ProfileInstructions string
	ProjectInstructions string
	PinnedState         string
	SkillsSummary       string
}

// Agent renders the main agent system prompt.
func Agent(view SystemView) string { return Render(System, view) }

// Text renders a fragment that takes no substitutions.
func Text(name string) string { return Render(name, nil) }

// Render executes the named fragment. It panics if the name is unknown or the
// data does not satisfy the template: returning a partial or empty prompt
// instead would ship an agent whose safety rules had silently vanished.
func Render(name string, data any) string {
	var b strings.Builder
	if err := tmpl.ExecuteTemplate(&b, name, data); err != nil {
		panic("prompts: rendering " + name + ": " + err.Error())
	}
	return b.String()
}
