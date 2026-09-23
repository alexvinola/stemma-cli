package adapters

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/capabilities"
	"github.com/alexvinola/stemma-cli/internal/diagnostics"
	"github.com/alexvinola/stemma-cli/internal/provenance"
)

// ExtensionLoss is one provider extension field that an export did not write.
type ExtensionLoss struct {
	// Provider is the extension namespace, as stored in the canonical entity.
	Provider string
	// Key is the field name within that namespace.
	Key string
	// Kind is the field's classification, or the conservative default.
	Kind capabilities.ExtensionKind
	// Classified is false when the key is not in the capability table and
	// Kind is capabilities.UnclassifiedExtensionKind.
	Classified bool
	// Meaning describes the field when it is classified.
	Meaning string
}

// Field is the canonical path of the lost field, used to anchor diagnostics.
func (l ExtensionLoss) Field() string {
	return "extensions." + l.Provider + "." + l.Key
}

// Reported reports whether losing the field needs a diagnostic. Only
// presentation fields may disappear without one.
func (l ExtensionLoss) Reported() bool {
	return l.Kind != capabilities.ExtensionPresentation
}

func (l ExtensionLoss) label() string {
	if !l.Classified {
		return fmt.Sprintf("%s.%s (unclassified, treated as %s)", l.Provider, l.Key, l.Kind)
	}
	return fmt.Sprintf("%s.%s (%s)", l.Provider, l.Key, l.Kind)
}

// UnprojectedExtensions lists every extension field of an entity that
// projected does not report as written, classified and sorted by provider
// then key. Keys in Stemma's reserved namespace are bookkeeping, not user
// data, and are never listed.
func UnprojectedExtensions(ext canonical.Extensions, projected func(provider, key string) bool) []ExtensionLoss {
	providers := make([]string, 0, len(ext))
	for p := range ext {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	var out []ExtensionLoss
	for _, p := range providers {
		for _, k := range sortedKeys(ext[p]) {
			if strings.HasPrefix(k, InternalExtensionPrefix) {
				continue
			}
			if projected != nil && projected(p, k) {
				continue
			}
			loss := ExtensionLoss{Provider: p, Key: k, Kind: capabilities.UnclassifiedExtensionKind}
			if f, ok := capabilities.ClassifyExtension(canonical.TargetFormat(p), k); ok {
				loss.Kind, loss.Classified, loss.Meaning = f.Kind, true, f.Meaning
			}
			out = append(out, loss)
		}
	}
	return out
}

// ExtensionLossOutcome returns the projection outcome after losing the given
// fields. Losing anything other than presentation makes an exact or adapted
// mapping lossy; lossy, blocked and skipped outcomes are unchanged.
func ExtensionLossOutcome(outcome Outcome, losses []ExtensionLoss) Outcome {
	if outcome != OutcomeExact && outcome != OutcomeAdapted {
		return outcome
	}
	for _, l := range losses {
		if l.Reported() {
			return OutcomeLossy
		}
	}
	return outcome
}

// ExtensionLossDiagnostic builds the diagnostic for one lost field. It
// returns false for presentation fields, which are dropped silently.
//
// Security fields produce a blocking error: a permission policy or a tool
// allowlist must not vanish until someone accepts its fingerprint in the
// target profile. Every other reported field produces a warning. The field is
// part of the fingerprint, so accepting one field never accepts another.
func ExtensionLossDiagnostic(
	entityID string, target canonical.TargetFormat, source provenance.Provenance, l ExtensionLoss,
) (diagnostics.Diagnostic, bool) {
	if !l.Reported() {
		return diagnostics.Diagnostic{}, false
	}
	what := fmt.Sprintf("%s: %s", l.Kind, l.Meaning)
	if !l.Classified {
		what = fmt.Sprintf("unclassified; treated as %s because Stemma cannot tell whether it changes what the agent does",
			l.Kind)
	}
	origin := ""
	if source.SourcePath != "" {
		origin = fmt.Sprintf(" It was imported from %s.", source.SourcePath)
	}
	detail := fmt.Sprintf("%s declares %s (%s).%s The %s output has no place for it, so the field stays in "+
		"the canonical project but does not reach the generated files.",
		entityID, l.Field(), what, origin, target)
	var d diagnostics.Diagnostic
	if l.Kind == capabilities.ExtensionSecurity {
		d = diagnostics.New(diagnostics.SecurityExtensionNotProjected, diagnostics.SeverityError,
			fmt.Sprintf("%s security field %q is not written for %s", l.Provider, l.Key, target)).
			WithDetail("%s Losing it could widen or remove a restriction on what the agent may do, so it "+
				"blocks apply until it is reviewed.", detail).
			WithSuggestion("Configure an equivalent policy for %s by hand if one exists, then add this "+
				"fingerprint to acceptedDiagnostics in the %s profile.", target, target)
	} else {
		d = diagnostics.New(diagnostics.ExtensionNotProjected, diagnostics.SeverityWarning,
			fmt.Sprintf("%s field %q is not written for %s", l.Provider, l.Key, target)).
			WithDetail("%s", detail).
			WithSuggestion("Check whether %s needs an equivalent setting; once reviewed, accept this "+
				"diagnostic in the %s profile.", target, target)
	}
	return d.WithEntity(entityID).WithTarget(string(target)).WithField(l.Field()), true
}

// extensionLossNote explains the lost fields in a projection mapping.
func extensionLossNote(target canonical.TargetFormat, losses []ExtensionLoss) string {
	var labels []string
	for _, l := range losses {
		if l.Reported() {
			labels = append(labels, l.label())
		}
	}
	if len(labels) == 0 {
		return ""
	}
	return fmt.Sprintf("Provider-specific fields not written for %s: %s.", target, strings.Join(labels, ", "))
}
