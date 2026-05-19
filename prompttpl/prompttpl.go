// Package prompttpl provides simple {{variable}} interpolation for prompt templates.
//
// Usage:
//
//	tpl := prompttpl.New("Hello {{name}}, you are a {{role}}.")
//	out, err := tpl.Render(map[string]string{
//	    "name": "Alice",
//	    "role": "software engineer",
//	})
//	// out = "Hello Alice, you are a software engineer."
//
// Missing variables return an error; extra variables are ignored.
// Use MustRender to panic on error instead of returning it.
package prompttpl

import (
	"fmt"
	"strings"
)

// Template is a compiled prompt template with {{variable}} placeholders.
type Template struct {
	raw  string
	vars []string // deduplicated, in order of first appearance
}

// New parses a template string and returns a Template.
// Placeholder syntax is {{variableName}} — no whitespace inside braces.
func New(raw string) *Template {
	vars := extractVars(raw)
	return &Template{raw: raw, vars: vars}
}

// Render substitutes all {{variable}} placeholders using the provided values map.
// Returns an error if any placeholder in the template has no corresponding key in vars.
func (t *Template) Render(vars map[string]string) (string, error) {
	for _, v := range t.vars {
		if _, ok := vars[v]; !ok {
			return "", fmt.Errorf("prompttpl: missing variable %q", v)
		}
	}
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, "{{"+k+"}}", v)
	}
	return strings.NewReplacer(pairs...).Replace(t.raw), nil
}

// MustRender is like Render but panics if any variable is missing.
func (t *Template) MustRender(vars map[string]string) string {
	out, err := t.Render(vars)
	if err != nil {
		panic(err)
	}
	return out
}

// Variables returns the list of placeholder names in the template,
// in order of first appearance.
func (t *Template) Variables() []string {
	out := make([]string, len(t.vars))
	copy(out, t.vars)
	return out
}

// String returns the raw (un-rendered) template string.
func (t *Template) String() string { return t.raw }

// extractVars parses {{name}} placeholders and returns them deduplicated,
// in order of first appearance.
func extractVars(s string) []string {
	var vars []string
	seen := make(map[string]bool)
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			break
		}
		name := s[start+2 : start+end]
		s = s[start+end+2:]
		if name != "" && !seen[name] {
			seen[name] = true
			vars = append(vars, name)
		}
	}
	return vars
}
