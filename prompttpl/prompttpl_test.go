package prompttpl

import (
	"strings"
	"testing"
)

func TestRenderBasic(t *testing.T) {
	tpl := New("Hello {{name}}, you are a {{role}}.")
	out, err := tpl.Render(map[string]string{"name": "Alice", "role": "engineer"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "Hello Alice, you are a engineer." {
		t.Fatalf("got: %q", out)
	}
}

func TestRenderMissingVar(t *testing.T) {
	tpl := New("Hello {{name}}.")
	_, err := tpl.Render(map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing variable")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Fatalf("error should mention variable name: %v", err)
	}
}

func TestRenderExtraVarsIgnored(t *testing.T) {
	tpl := New("Hello {{name}}.")
	out, err := tpl.Render(map[string]string{"name": "Bob", "extra": "ignored"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "Hello Bob." {
		t.Fatalf("got: %q", out)
	}
}

func TestVariables(t *testing.T) {
	tpl := New("{{a}} and {{b}} and {{a}} again.")
	vars := tpl.Variables()
	if len(vars) != 2 {
		t.Fatalf("expected 2 unique vars, got %d: %v", len(vars), vars)
	}
	if vars[0] != "a" || vars[1] != "b" {
		t.Fatalf("unexpected order: %v", vars)
	}
}

func TestNoPlaceholders(t *testing.T) {
	tpl := New("No variables here.")
	out, err := tpl.Render(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "No variables here." {
		t.Fatalf("got: %q", out)
	}
}

func TestMustRenderPanicsOnMissing(t *testing.T) {
	tpl := New("{{x}}")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on missing variable")
		}
	}()
	tpl.MustRender(map[string]string{})
}

func TestString(t *testing.T) {
	raw := "{{a}} test"
	if New(raw).String() != raw {
		t.Fatal("String() should return raw template")
	}
}

func TestEmptyPlaceholderIgnored(t *testing.T) {
	// {{}} should not be treated as a variable.
	tpl := New("test {{}} end")
	if len(tpl.Variables()) != 0 {
		t.Fatalf("expected 0 vars for empty placeholder, got %v", tpl.Variables())
	}
}
