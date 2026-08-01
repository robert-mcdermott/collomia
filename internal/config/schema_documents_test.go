package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The tests here check the promise the schema actually makes to a user: that
// the files Collomia itself produces validate against it, and that the
// mistakes people make in a hand-written configuration do not.
//
// The checker below is deliberately small and lives in the test rather than
// pulling in a JSON Schema library, which would be a new module dependency for
// something only the tests need. It covers exactly the keywords the generator
// emits — type, enum, required, properties, additionalProperties, $ref, items,
// and the numeric bounds — so it cannot silently pass a schema using a keyword
// it does not understand.

type schemaChecker struct {
	root map[string]any
	defs map[string]any
}

func newSchemaChecker(t *testing.T) *schemaChecker {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(JSONSchema(), &root); err != nil {
		t.Fatal(err)
	}
	defs, _ := root["$defs"].(map[string]any)
	return &schemaChecker{root: root, defs: defs}
}

func (c *schemaChecker) check(document any) []string {
	return c.node(c.root, document, "")
}

func (c *schemaChecker) node(schema map[string]any, value any, path string) []string {
	if ref, ok := schema["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		target, ok := c.defs[name].(map[string]any)
		if !ok {
			return []string{path + ": dangling $ref " + ref}
		}
		return c.node(target, value, path)
	}
	var problems []string
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return []string{path + ": expected an object"}
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range asStrings(schema["required"]) {
			if _, present := object[name]; !present {
				problems = append(problems, path+": missing required "+name)
			}
		}
		additional, hasAdditional := schema["additionalProperties"]
		for key, child := range object {
			childPath := strings.TrimPrefix(path+"."+key, ".")
			if property, ok := properties[key].(map[string]any); ok {
				problems = append(problems, c.node(property, child, childPath)...)
				continue
			}
			// A map-valued field carries its element schema here; a struct
			// carries `false`, which is what makes a typo an error.
			if elem, ok := additional.(map[string]any); ok {
				problems = append(problems, c.node(elem, child, childPath)...)
				continue
			}
			if hasAdditional && additional == false {
				problems = append(problems, childPath+": unknown field")
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return []string{path + ": expected an array"}
		}
		if elem, ok := schema["items"].(map[string]any); ok {
			for i, child := range items {
				problems = append(problems, c.node(elem, child, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return []string{path + ": expected a string"}
		}
		if enum := asStrings(schema["enum"]); len(enum) > 0 && !slices.Contains(enum, text) {
			problems = append(problems, fmt.Sprintf("%s: %q is not one of %v", path, text, enum))
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			problems = append(problems, path+": expected a boolean")
		}
	case "integer", "number":
		number, ok := value.(float64)
		if !ok {
			return []string{path + ": expected a number"}
		}
		if minimum, ok := schema["minimum"].(float64); ok && number < minimum {
			problems = append(problems, fmt.Sprintf("%s: %v is below the minimum %v", path, number, minimum))
		}
		if maximum, ok := schema["maximum"].(float64); ok && number > maximum {
			problems = append(problems, fmt.Sprintf("%s: %v is above the maximum %v", path, number, maximum))
		}
		if exclusive, ok := schema["exclusiveMinimum"].(float64); ok && number <= exclusive {
			problems = append(problems, fmt.Sprintf("%s: %v is not above %v", path, number, exclusive))
		}
	}
	return problems
}

func asStrings(value any) []string {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func TestSchemaAcceptsEveryFileCollomiaWrites(t *testing.T) {
	// A schema that flags the tool's own output is worse than none: the first
	// thing a user would see after `collo init` is their editor underlining a
	// file they did not write.
	checker := newSchemaChecker(t)
	for _, global := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := WriteStarter(path, global); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		if problems := checker.check(document); len(problems) > 0 {
			t.Errorf("starter (global=%t) does not validate against the schema it points at:\n  %s",
				global, strings.Join(problems, "\n  "))
		}
	}
}

func TestSchemaAcceptsTheExhaustiveReference(t *testing.T) {
	// The reference names every field there is, so this is the broadest
	// document available and the one most likely to expose a field the
	// generator mistyped.
	checker := newSchemaChecker(t)
	var uncommented strings.Builder
	for _, line := range strings.Split(ConfigReference(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		uncommented.WriteString(line + "\n")
	}
	var document any
	if err := json.Unmarshal([]byte(uncommented.String()), &document); err != nil {
		t.Fatal(err)
	}
	if problems := checker.check(document); len(problems) > 0 {
		t.Errorf("the configuration reference does not validate against the generated schema:\n  %s",
			strings.Join(problems, "\n  "))
	}
}

func TestSchemaRejectsTheMistakesPeopleActuallyMake(t *testing.T) {
	// Each of these is a real shape of error: a misspelled field that the
	// ordinary loader ignores in silence, a misspelled enum value, a value of
	// the wrong type, a bound the loader enforces, and — the one a single
	// shared Rule definition would have missed — an "allow" rule inside a
	// delegated agent, where only prompt and deny are accepted.
	checker := newSchemaChecker(t)
	for _, tc := range []struct{ name, document, wantSubstring string }{
		{"misspelled field", `{"providers":{"p":{"type":"openai","max_token":100}}}`, "unknown field"},
		{"misspelled enum", `{"permissions":{"mode":"autopilate"}}`, "not one of"},
		{"wrong type", `{"options":{"mouse":"yes"}}`, "expected a boolean"},
		{"bound exceeded", `{"options":{"delegate_max_concurrency":12}}`, "above the maximum"},
		{"missing provider type", `{"providers":{"p":{"base_url":"http://x/v1"}}}`, "missing required type"},
		{"allow in a delegated rule", `{"agents":{"r":{"permissions":{"rules":[{"action":"allow"}]}}}}`, "not one of"},
	} {
		var document any
		if err := json.Unmarshal([]byte(tc.document), &document); err != nil {
			t.Fatal(err)
		}
		problems := checker.check(document)
		if len(problems) == 0 {
			t.Errorf("%s: the schema accepted it", tc.name)
			continue
		}
		if !strings.Contains(strings.Join(problems, "\n"), tc.wantSubstring) {
			t.Errorf("%s: reported %v, wanted something mentioning %q", tc.name, problems, tc.wantSubstring)
		}
	}
}

func TestSchemaAllowsATopLevelRuleToAllow(t *testing.T) {
	// The mirror of the delegated case above. Narrowing the wrong one would be
	// invisible until someone's working allow rule started being underlined.
	checker := newSchemaChecker(t)
	var document any
	if err := json.Unmarshal([]byte(`{"permissions":{"rules":[{"action":"allow","tool":"read_file"}]}}`), &document); err != nil {
		t.Fatal(err)
	}
	if problems := checker.check(document); len(problems) > 0 {
		t.Errorf("a top-level allow rule must remain valid: %v", problems)
	}
}
