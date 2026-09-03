// Package profiles describes how canonical entities are delivered to a target.
//
// A profile controls delivery, never truth: it can move an entity to another
// file, change how it is activated, disable it for one target, or accept a
// known lossy mapping. Changing the *meaning* of a rule requires an explicit
// content override, which always produces a diagnostic.
package profiles

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/globs"
	"github.com/alexvinola/stemma-cli/internal/provenance"
	"github.com/alexvinola/stemma-cli/internal/version"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// MaxProfileBytes bounds a profile document.
const MaxProfileBytes = 4 << 20

// Override adjusts how one canonical entity is projected to one target.
type Override struct {
	// Include forces the entity in (true) or out (false) of the target.
	// nil means "use the default projection rules".
	Include *bool `json:"include,omitempty"`
	// Activation replaces the canonical activation for this target only.
	Activation *canonical.Activation `json:"activation,omitempty"`
	// Directory pins the destination directory (repository-relative).
	Directory string `json:"directory,omitempty"`
	// Filename pins the destination file name inside Directory.
	Filename string `json:"filename,omitempty"`
	// AcceptLossy records that a lossy mapping has been reviewed and accepted.
	AcceptLossy bool `json:"acceptLossy,omitempty"`
	// ContentOverride replaces the agent-facing text for this target. Using it
	// always produces STEMMA3601 so the divergence stays visible.
	ContentOverride string `json:"contentOverride,omitempty"`
	// Options carries provider-specific, non-semantic switches.
	Options map[string]any `json:"options,omitempty"`
}

// Profile is a target projection profile.
type Profile struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Target        canonical.TargetFormat `json:"target"`
	// Overrides is keyed by canonical entity ID.
	Overrides map[string]Override `json:"overrides"`
	// AcceptedDiagnostics lists diagnostic fingerprints that have been
	// explicitly reviewed; they are downgraded to info and stop blocking.
	AcceptedDiagnostics []string `json:"acceptedDiagnostics"`
	// Options carries target-wide switches.
	Options map[string]any `json:"options,omitempty"`
}

// Default returns an empty profile for a target.
func Default(target canonical.TargetFormat) Profile {
	return Profile{
		SchemaVersion:       version.ProfileSchemaVersion,
		Target:              target,
		Overrides:           map[string]Override{},
		AcceptedDiagnostics: []string{},
	}
}

// For returns the override for an entity, if any.
func (p Profile) For(entityID string) (Override, bool) {
	o, ok := p.Overrides[entityID]
	return o, ok
}

// Marshal renders a profile as deterministic JSON with a trailing newline.
func Marshal(p Profile) ([]byte, error) {
	p.SchemaVersion = version.ProfileSchemaVersion
	if p.Overrides == nil {
		p.Overrides = map[string]Override{}
	}
	if p.AcceptedDiagnostics == nil {
		p.AcceptedDiagnostics = []string{}
	}
	sorted := append([]string{}, p.AcceptedDiagnostics...)
	sort.Strings(sorted)
	p.AcceptedDiagnostics = sorted
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, fmt.Errorf("encode profile: %w", err)
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes a profile, rejecting unknown fields.
func Unmarshal(data []byte) (Profile, error) {
	if len(data) > MaxProfileBytes {
		return Profile{}, fmt.Errorf("profile exceeds %d bytes", MaxProfileBytes)
	}
	var p Profile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Profile{}, fmt.Errorf("decode profile: %w", err)
	}
	if dec.More() {
		return Profile{}, fmt.Errorf("decode profile: trailing content after JSON document")
	}
	if p.SchemaVersion != version.ProfileSchemaVersion {
		return Profile{}, fmt.Errorf("unsupported profile schema version %d (this build supports %d)",
			p.SchemaVersion, version.ProfileSchemaVersion)
	}
	if p.Overrides == nil {
		p.Overrides = map[string]Override{}
	}
	if p.AcceptedDiagnostics == nil {
		p.AcceptedDiagnostics = []string{}
	}
	return p, nil
}

// Hash returns the digest of the canonical serialization of the profile.
func Hash(p Profile) (string, error) {
	b, err := Marshal(p)
	if err != nil {
		return "", err
	}
	return provenance.HashBytes(b), nil
}

// Validate checks a profile against a project and returns diagnostics.
func Validate(p Profile, project canonical.Project, path string) []diagnostics.Diagnostic {
	var bag diagnostics.Bag
	if !canonical.KnownTarget(p.Target) {
		bag.Add(diagnostics.New(diagnostics.ProfileInvalid, diagnostics.SeverityError,
			fmt.Sprintf("profile declares unknown target %q", p.Target)).WithPath(path))
	}
	known := map[string]struct{}{}
	for _, e := range project.Entities() {
		known[e.ID] = struct{}{}
	}
	ids := make([]string, 0, len(p.Overrides))
	for id := range p.Overrides {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		o := p.Overrides[id]
		if _, ok := known[id]; !ok {
			bag.Add(diagnostics.New(diagnostics.ProfileUnknownID, diagnostics.SeverityWarning,
				fmt.Sprintf("profile overrides unknown entity %q", id)).
				WithPath(path).WithEntity(id).WithTarget(string(p.Target)).
				WithSuggestion("Remove the override, or check the entity id in .stemma/project.json."))
			continue
		}
		if _, _, err := canonical.ParseID(id); err != nil {
			bag.Add(diagnostics.New(diagnostics.ProfileInvalid, diagnostics.SeverityError,
				fmt.Sprintf("profile override key %q is not a valid entity id", id)).
				WithPath(path).WithTarget(string(p.Target)))
		}
		if o.Activation != nil {
			if err := o.Activation.Validate(); err != nil {
				bag.Add(diagnostics.New(diagnostics.ProfileInvalid, diagnostics.SeverityError,
					fmt.Sprintf("override for %q has an invalid activation: %v", id, err)).
					WithPath(path).WithEntity(id).WithTarget(string(p.Target)))
			}
			for _, g := range append(append([]string{}, o.Activation.Include...), o.Activation.Exclude...) {
				if err := globs.Validate(g); err != nil {
					bag.Add(diagnostics.New(diagnostics.InvalidGlob, diagnostics.SeverityError,
						fmt.Sprintf("override for %q has an invalid pattern: %v", id, err)).
						WithPath(path).WithEntity(id).WithTarget(string(p.Target)))
				}
			}
		}
		if o.Directory != "" {
			if _, err := workspace.NormalizeRel(o.Directory); err != nil {
				bag.Add(diagnostics.New(diagnostics.PathEscape, diagnostics.SeverityError,
					fmt.Sprintf("override for %q has an unsafe directory %q: %v", id, o.Directory, err)).
					WithPath(path).WithEntity(id).WithTarget(string(p.Target)))
			}
		}
		if o.Filename != "" {
			if strings.ContainsAny(o.Filename, "/\\") || o.Filename == "." || o.Filename == ".." {
				bag.Add(diagnostics.New(diagnostics.PathEscape, diagnostics.SeverityError,
					fmt.Sprintf("override for %q has an unsafe filename %q", id, o.Filename)).
					WithPath(path).WithEntity(id).WithTarget(string(p.Target)).
					WithSuggestion("Use directory for the folder and filename for the file name only."))
			}
		}
	}
	for _, fp := range p.AcceptedDiagnostics {
		if !strings.HasPrefix(fp, "dg_") {
			bag.Add(diagnostics.New(diagnostics.ProfileInvalid, diagnostics.SeverityWarning,
				fmt.Sprintf("accepted diagnostic fingerprint %q does not look like a Stemma fingerprint", fp)).
				WithPath(path).WithTarget(string(p.Target)).
				WithSuggestion("Fingerprints are printed by `stemma plan --json` as diagnostics[].fingerprint."))
		}
	}
	return bag.Items()
}
