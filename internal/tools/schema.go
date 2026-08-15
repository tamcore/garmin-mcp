package tools

import (
	"maps"
	"slices"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

// JSON Schema type names this package uses.
const (
	typeString  = "string"
	typeInteger = "integer"
	typeNumber  = "number"
	typeBoolean = "boolean"
	typeArray   = "array"
	typeObject  = "object"
)

// JSON Schema keywords this package emits. They are named once so a nested item
// schema, which is written as plain data, cannot misspell what the property renderer
// emits.
const (
	keyType                 = "type"
	keyProperties           = "properties"
	keyRequired             = "required"
	keyAdditionalProperties = "additionalProperties"
	keyDescription          = "description"
	keyFormat               = "format"
	keyPattern              = "pattern"
	keyMinimum              = "minimum"
	keyMaximum              = "maximum"
	keyMaxLength            = "maxLength"
	keyMinItems             = "minItems"
	keyMaxItems             = "maxItems"
	keyItems                = "items"
	keyEnum                 = "enum"
	keyDefault              = "default"
)

// JSON Schema formats and patterns this package declares.
const (
	formatDate          = "date"
	formatDateTime      = "date-time"
	patternCalendarDate = `^\d{4}-\d{2}-\d{2}$`
)

// A Property is one strict input property of a tool.
//
// It is the declared contract: the ranges, formats and defaults a caller may rely on,
// and the shape the contract test compares against compat/tools.json. The handler
// enforces the same bounds from the same constants, so the declaration and the
// behaviour cannot drift apart silently.
type Property struct {
	// Name is the wire argument name, which must be the manifest's name.
	Name string

	// Types are the accepted JSON types. More than one renders as anyOf, which is
	// how the manifest describes an identifier that arrives as a number or a string.
	Types []string

	// Description is the model-facing description and is required.
	Description string

	// Format is an optional JSON Schema format, for example "date".
	Format string

	// Pattern is an optional anchored regular expression.
	Pattern string

	// Minimum and Maximum bound a numeric argument. Nil means unbounded.
	Minimum *float64
	Maximum *float64

	// MaxLength bounds a string argument. Nil means unbounded.
	MaxLength *int

	// MinItems and MaxItems bound an array argument. Nil means unbounded.
	MinItems *int
	MaxItems *int

	// Items is the declared element schema of an array argument. Nil declares no
	// element schema, which is what the manifest states for the batch tools.
	Items map[string]any

	// Enum is the closed set of accepted values. Nil means the value is open.
	Enum []any

	// Default is the value the tool applies when the argument is absent. Nil means
	// the argument has no default.
	Default any

	// Required reports whether the caller must supply the argument.
	Required bool
}

// A Schema is the strict input schema of one tool.
//
// It is immutable data: JSON returns a fresh document every time, so no caller can
// reach into the declaration another caller holds.
type Schema struct {
	properties []Property
}

// NewSchema returns the schema described by properties, copying the slice.
func NewSchema(properties ...Property) Schema {
	return Schema{properties: slices.Clone(properties)}
}

// Properties returns a copy of the declared properties.
func (s Schema) Properties() []Property { return slices.Clone(s.properties) }

// Required returns the required argument names, sorted.
func (s Schema) Required() []string {
	out := make([]string, 0, len(s.properties))
	for _, property := range s.properties {
		if property.Required {
			out = append(out, property.Name)
		}
	}
	slices.Sort(out)
	return out
}

// JSON renders the normalized JSON Schema document: a strict object that accepts no
// property it does not declare.
func (s Schema) JSON() map[string]any {
	properties := make(map[string]any, len(s.properties))
	for _, property := range s.properties {
		properties[property.Name] = property.json()
	}
	return map[string]any{
		keyType:                 typeObject,
		keyProperties:           properties,
		keyRequired:             s.Required(),
		keyAdditionalProperties: false,
	}
}

// json renders one property. A single type renders as "type"; several render as
// "anyOf", matching the manifest.
func (p Property) json() map[string]any {
	out := map[string]any{keyDescription: p.Description}
	switch {
	case len(p.Types) == 1:
		out[keyType] = p.Types[0]
	case len(p.Types) > 1:
		variants := make([]any, 0, len(p.Types))
		for _, name := range slices.Sorted(slices.Values(p.Types)) {
			variants = append(variants, map[string]any{keyType: name})
		}
		out["anyOf"] = variants
	}
	p.addConstraints(out)
	return out
}

// addConstraints adds every declared bound that is set.
func (p Property) addConstraints(out map[string]any) {
	if p.Format != "" {
		out[keyFormat] = p.Format
	}
	if p.Pattern != "" {
		out[keyPattern] = p.Pattern
	}
	if p.Minimum != nil {
		out[keyMinimum] = *p.Minimum
	}
	if p.Maximum != nil {
		out[keyMaximum] = *p.Maximum
	}
	if p.MaxLength != nil {
		out[keyMaxLength] = *p.MaxLength
	}
	if p.MinItems != nil {
		out[keyMinItems] = *p.MinItems
	}
	if p.MaxItems != nil {
		out[keyMaxItems] = *p.MaxItems
	}
	if p.Items != nil {
		out[keyItems] = maps.Clone(p.Items)
	}
	if len(p.Enum) > 0 {
		out[keyEnum] = slices.Clone(p.Enum)
	}
	if p.Default != nil {
		out[keyDefault] = p.Default
	}
}

// A Contract is the declared contract of one registered tool: what the server tells a
// client about it, plus the strict input schema this package guarantees.
type Contract struct {
	// Spec is the registration spec, including the tier, the log category and all
	// four annotation hints.
	Spec mcpserver.ToolSpec

	// Schema is the strict input schema.
	Schema Schema
}

// Registration returns the spec that is registered with the server, carrying the
// declared schema so the strict bounds reach the wire.
//
// Without this, the SDK infers a schema from the handler's input type: it gets
// the types and the descriptions but none of the ranges, formats, defaults or
// anyOf, so a bound such as "limit is 1 to 100, default 20" would be enforced in
// the handler and invisible to the caller. Registering the declared schema keeps
// the published contract and the enforced contract the same object.
// Publishing it matters: the schema the SDK infers from a handler's Go input type
// carries types and descriptions but no ranges, formats, defaults or unions, so a
// bound such as "limit is 1 to 100, default 20" would be enforced in the handler
// and invisible to the caller. A client builds its call from the published
// schema, so a thin contract produces avoidable invalid calls.
func (c Contract) Registration() mcpserver.ToolSpec {
	spec := c.Spec
	spec.InputSchema = c.Schema.JSON()
	return spec
}

// cloneContracts returns a copy, so Contracts hands out no shared map.
func cloneContracts(contracts map[string]Contract) map[string]Contract {
	return maps.Clone(contracts)
}

// bound is a convenience for a numeric schema bound.
func bound(value float64) *float64 { return new(value) }
