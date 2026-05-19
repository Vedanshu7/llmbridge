// Package toolbuilder provides a fluent API for constructing types.Tool values
// without manually assembling nested structs.
//
// Usage:
//
//	tool := toolbuilder.New("get_weather").
//	    Desc("Fetch current weather for a city.").
//	    Param("location", "string", "City name, e.g. 'London'").
//	    Param("unit", "string", "Temperature unit: 'celsius' or 'fahrenheit'").
//	    Enum("unit", "celsius", "fahrenheit").
//	    Required("location").
//	    Build()
package toolbuilder

import "github.com/Vedanshu7/llmbridge/types"

// Builder constructs a types.Tool incrementally.
type Builder struct {
	name     string
	desc     string
	props    map[string]types.Property
	required []string
	order    []string // insertion order for deterministic output
}

// New returns a Builder for a tool with the given name.
func New(name string) *Builder {
	return &Builder{
		name:  name,
		props: make(map[string]types.Property),
	}
}

// Desc sets the tool description.
func (b *Builder) Desc(description string) *Builder {
	b.desc = description
	return b
}

// Param adds a parameter with the given name, JSON type, and description.
// Common types: "string", "number", "integer", "boolean", "array", "object".
func (b *Builder) Param(name, typ, description string) *Builder {
	if _, exists := b.props[name]; !exists {
		b.order = append(b.order, name)
	}
	b.props[name] = types.Property{Type: typ, Description: description}
	return b
}

// Enum restricts the valid values for an existing parameter.
// The parameter must have been added with Param first.
func (b *Builder) Enum(name string, values ...string) *Builder {
	if p, ok := b.props[name]; ok {
		p.Enum = values
		b.props[name] = p
	}
	return b
}

// Required marks parameters as required. Multiple calls accumulate.
func (b *Builder) Required(names ...string) *Builder {
	b.required = append(b.required, names...)
	return b
}

// Build returns the finished types.Tool. The Builder may be reused after
// Build — subsequent Param/Desc calls won't affect the returned Tool.
func (b *Builder) Build() types.Tool {
	props := make(map[string]types.Property, len(b.props))
	for _, k := range b.order {
		props[k] = b.props[k]
	}
	req := make([]string, len(b.required))
	copy(req, b.required)
	return types.Tool{
		Name:        b.name,
		Description: b.desc,
		Parameters: types.Schema{
			Type:       "object",
			Properties: props,
			Required:   req,
		},
	}
}
