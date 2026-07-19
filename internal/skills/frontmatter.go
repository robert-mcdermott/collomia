package skills

import (
	"strings"
)

// frontmatter holds the parsed YAML front matter of a SKILL.md. Collomia
// supports the subset of YAML that skill files actually use — string scalars
// (plain, quoted, folded `>` and literal `|` blocks, and indented
// continuation lines), string lists (block `- item` or inline `[a, b]`), and
// one level of nested maps (`metadata:`) — without taking on a YAML
// dependency.
type frontmatter struct {
	scalars map[string]string
	lists   map[string][]string
	maps    map[string]map[string]string
}

// splitFrontmatter separates the front matter lines from the body. The file
// must start with a `---` line for front matter to exist; otherwise the whole
// content is body.
func splitFrontmatter(content string) (fm []string, body string, found bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, content, false
	}
	for i := 1; i < len(lines); i++ {
		if trimmed := strings.TrimSpace(lines[i]); trimmed == "---" || trimmed == "..." {
			return lines[1:i], strings.Join(lines[i+1:], "\n"), true
		}
	}
	return nil, content, false
}

func parseFrontmatter(lines []string) frontmatter {
	fm := frontmatter{scalars: map[string]string{}, lists: map[string][]string{}, maps: map[string]map[string]string{}}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || indentOf(line) > 0 {
			continue
		}
		key, rest, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		rest = strings.TrimSpace(rest)
		switch {
		case rest == "|" || rest == "|-" || rest == "|+" || rest == ">" || rest == ">-" || rest == ">+":
			value, next := blockScalar(lines, i+1, rest[0] == '>')
			fm.scalars[key] = value
			i = next - 1
		case rest == "":
			list, nested, next := blockCollection(lines, i+1)
			if len(list) > 0 {
				fm.lists[key] = list
			} else if len(nested) > 0 {
				fm.maps[key] = nested
			}
			i = next - 1
		case strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]"):
			fm.lists[key] = splitInlineList(rest)
		default:
			value, next := plainScalar(unquote(rest), lines, i+1)
			fm.scalars[key] = value
			i = next - 1
		}
	}
	return fm
}

// blockScalar collects the indented lines of a `|` or `>` block. Literal
// blocks keep line breaks; folded blocks join lines with spaces.
func blockScalar(lines []string, start int, folded bool) (string, int) {
	var parts []string
	i := start
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			parts = append(parts, "")
			continue
		}
		if indentOf(lines[i]) == 0 {
			break
		}
		parts = append(parts, strings.TrimSpace(lines[i]))
	}
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	sep := "\n"
	if folded {
		sep = " "
	}
	return strings.TrimSpace(strings.Join(parts, sep)), i
}

// plainScalar extends an unquoted scalar with YAML plain-style continuation
// lines (indented, non-list, following lines fold in with a space).
func plainScalar(value string, lines []string, start int) (string, int) {
	i := start
	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || indentOf(lines[i]) == 0 || strings.HasPrefix(trimmed, "- ") {
			break
		}
		value += " " + trimmed
	}
	return value, i
}

// blockCollection reads the indented lines after a bare `key:` as either a
// string list or a one-level nested map, whichever the content is.
func blockCollection(lines []string, start int) (list []string, nested map[string]string, next int) {
	nested = map[string]string{}
	i := start
	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if indentOf(lines[i]) == 0 {
			break
		}
		if trimmed == "-" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if item := unquote(strings.TrimSpace(trimmed[2:])); item != "" {
				list = append(list, item)
			}
			continue
		}
		if k, v, ok := strings.Cut(trimmed, ":"); ok {
			nested[strings.ToLower(strings.TrimSpace(k))] = unquote(strings.TrimSpace(v))
		}
	}
	return list, nested, i
}

func splitInlineList(value string) []string {
	inner := strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	var items []string
	for _, item := range strings.Split(inner, ",") {
		if item = unquote(strings.TrimSpace(item)); item != "" {
			items = append(items, item)
		}
	}
	return items
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func unquote(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
