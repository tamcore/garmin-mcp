// Package resources serves the constant documents this server publishes as MCP
// resources.
//
// Every document here is compiled in. Nothing in this package reads Garmin, reads
// the caller's account, or varies by principal: a resource that did any of those
// would be a tool, and the tier gate, the confirmation gate and the rate limiter
// all key off tools deliberately.
//
// The workout templates exist to be edited and handed to upload_workout, so they
// are written in the same document shape internal/tools builds — the shape the live
// write suite has actually uploaded to Garmin. A template this server's own upload
// path would reject is worse than no template, which is what
// TestEveryTemplateIsAValidWorkoutDocument exists to prevent.
package resources

import (
	"encoding/json"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

// mimeTypePlainText is the media type every document is served as. The manifest
// pins text/plain: the bodies are JSON, but upstream serves them as plain text and
// a client that switched on the media type would see a different resource if this
// changed.
const mimeTypePlainText = "text/plain"

// Registrar contributes this package's documents to a server.
type Registrar struct{}

// NewRegistrar returns the registrar the composition root wires in.
func NewRegistrar() Registrar { return Registrar{} }

// RegisterResources registers every constant document, in manifest order.
func (Registrar) RegisterResources(registry *mcpserver.Registry) error {
	for _, doc := range documents() {
		if err := mcpserver.AddResource(registry, doc.spec, doc.body); err != nil {
			return fmt.Errorf("registering %s: %w", doc.spec.URI, err)
		}
	}
	return nil
}

// A document pairs one resource's declared contract with the bytes it serves.
type document struct {
	spec mcpserver.ResourceSpec
	body func() string
}

// documents is the whole published set. The URIs, names, descriptions and media
// type are the pinned manifest's, verbatim.
func documents() []document {
	return []document{
		{
			spec: mcpserver.ResourceSpec{
				URI:   "workout://templates/simple-run",
				Name:  "get_simple_run_template",
				Title: "Simple run workout template",
				Description: "Simple run workout template (warmup, run, cooldown)\n\n" +
					"A basic running workout structure suitable for easy runs.\n" +
					"Modify the endConditionValue to adjust durations.",
				MIMEType: mimeTypePlainText,
			},
			body: func() string { return render(simpleRunTemplate()) },
		},
		{
			spec: mcpserver.ResourceSpec{
				URI:   "workout://templates/interval-running",
				Name:  "get_interval_template",
				Title: "Interval running workout template",
				Description: "Interval running workout template with repeat groups\n\n" +
					"Demonstrates RepeatGroupDTO for interval training.\n" +
					"Includes 6x400m intervals with 2min recovery.",
				MIMEType: mimeTypePlainText,
			},
			body: func() string { return render(intervalRunTemplate()) },
		},
		{
			spec: mcpserver.ResourceSpec{
				URI:   "workout://templates/tempo-run",
				Name:  "get_tempo_template",
				Title: "Tempo run workout template",
				Description: "Tempo run workout template with heart rate zone target\n\n" +
					"Demonstrates targeting a specific heart rate zone.\n" +
					"20min tempo block at HR zone 4.",
				MIMEType: mimeTypePlainText,
			},
			body: func() string { return render(tempoRunTemplate()) },
		},
		{
			spec: mcpserver.ResourceSpec{
				URI:   "workout://templates/strength-circuit",
				Name:  "get_strength_template",
				Title: "Strength training circuit template",
				Description: "Strength training circuit template\n\n" +
					"Circuit-style strength workout with repeat groups.\n" +
					"3 rounds of 10min work + 2min rest.",
				MIMEType: mimeTypePlainText,
			},
			body: func() string { return render(strengthCircuitTemplate()) },
		},
		{
			spec: mcpserver.ResourceSpec{
				URI:   "workout://reference/structure",
				Name:  "get_structure_reference",
				Title: "Workout structure reference",
				Description: "Reference guide for workout JSON structure\n\n" +
					"Documents valid values for step types, conditions, targets, and sports.\n" +
					"Use this to understand what values are valid in workout definitions.",
				MIMEType: mimeTypePlainText,
			},
			body: func() string { return render(structureReference()) },
		},
	}
}

// render serialises one document the way upstream does, with two-space indentation.
//
// The marshal cannot fail: every value here is compiled in, built from strings,
// numbers and slices, with no channel, function or NaN in reach. It is still
// checked, and a failure yields an empty document rather than a panic, because a
// resource read is not worth taking the process down for. TestEveryDocumentRenders
// proves no document takes that path.
func render(value any) string {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(body)
}
