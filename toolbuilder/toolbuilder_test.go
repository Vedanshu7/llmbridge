package toolbuilder

import (
	"testing"
)

func TestBasicBuild(t *testing.T) {
	tool := New("get_weather").
		Desc("Get current weather.").
		Param("location", "string", "City name").
		Required("location").
		Build()

	if tool.Name != "get_weather" {
		t.Fatalf("name: %s", tool.Name)
	}
	if tool.Description != "Get current weather." {
		t.Fatalf("desc: %s", tool.Description)
	}
	if tool.Parameters.Type != "object" {
		t.Fatalf("schema type: %s", tool.Parameters.Type)
	}
	if _, ok := tool.Parameters.Properties["location"]; !ok {
		t.Fatal("missing location param")
	}
	if len(tool.Parameters.Required) != 1 || tool.Parameters.Required[0] != "location" {
		t.Fatalf("required: %v", tool.Parameters.Required)
	}
}

func TestEnumParam(t *testing.T) {
	tool := New("set_unit").
		Param("unit", "string", "Temperature unit").
		Enum("unit", "celsius", "fahrenheit").
		Build()

	p := tool.Parameters.Properties["unit"]
	if len(p.Enum) != 2 || p.Enum[0] != "celsius" {
		t.Fatalf("enum: %v", p.Enum)
	}
}

func TestMultipleParams(t *testing.T) {
	tool := New("fn").
		Param("a", "string", "first").
		Param("b", "integer", "second").
		Param("c", "boolean", "third").
		Required("a", "b").
		Build()

	if len(tool.Parameters.Properties) != 3 {
		t.Fatalf("expected 3 params, got %d", len(tool.Parameters.Properties))
	}
	if len(tool.Parameters.Required) != 2 {
		t.Fatalf("expected 2 required, got %d", len(tool.Parameters.Required))
	}
}

func TestBuildIsImmutable(t *testing.T) {
	b := New("fn").Param("x", "string", "x")
	t1 := b.Build()
	b.Param("y", "string", "y") // mutate builder after first Build
	t2 := b.Build()

	// t1 should only have x; t2 should have x and y.
	if _, ok := t1.Parameters.Properties["y"]; ok {
		t.Fatal("t1 should not have y after subsequent Param call")
	}
	if _, ok := t2.Parameters.Properties["y"]; !ok {
		t.Fatal("t2 should have y")
	}
}

func TestEnumOnUnknownParamIsNoop(t *testing.T) {
	// Should not panic.
	tool := New("fn").Enum("missing", "a", "b").Build()
	if len(tool.Parameters.Properties) != 0 {
		t.Fatal("expected no properties")
	}
}
