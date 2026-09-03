package canonical

import (
	"encoding/json"
	"fmt"

	"github.com/alexvinola/stemma-cli/internal/globs"
)

// ActivationType is the tag of the exhaustive activation union.
type ActivationType string

const (
	// ActivationAlways: loaded into every request.
	ActivationAlways ActivationType = "always"
	// ActivationPathScoped: loaded when matching files are in scope.
	ActivationPathScoped ActivationType = "path-scoped"
	// ActivationOnDemand: loaded only when explicitly invoked.
	ActivationOnDemand ActivationType = "on-demand"
	// ActivationDocumentationOnly: never loaded into agent context.
	ActivationDocumentationOnly ActivationType = "documentation-only"
)

// Activation is a tagged union describing when content reaches an agent.
//
// The zero value is deliberately invalid so that a forgotten assignment is a
// validation error rather than a silent "always-on" default.
type Activation struct {
	Type ActivationType `json:"type"`

	// Include and Exclude are only meaningful for ActivationPathScoped.
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`

	// Trigger and InvocationName are only meaningful for ActivationOnDemand.
	Trigger        string `json:"trigger,omitempty"`
	InvocationName string `json:"invocationName,omitempty"`
}

// Always returns an always-on activation.
func Always() Activation { return Activation{Type: ActivationAlways} }

// PathScoped returns a path-scoped activation.
func PathScoped(include, exclude []string) Activation {
	return Activation{
		Type:    ActivationPathScoped,
		Include: globs.Normalize(include),
		Exclude: globs.Normalize(exclude),
	}
}

// OnDemand returns an on-demand activation.
func OnDemand(trigger, invocation string) Activation {
	return Activation{Type: ActivationOnDemand, Trigger: trigger, InvocationName: invocation}
}

// DocumentationOnly returns a documentation-only activation.
func DocumentationOnly() Activation { return Activation{Type: ActivationDocumentationOnly} }

// KnownActivationType reports whether t is one of the four union tags.
func KnownActivationType(t ActivationType) bool {
	switch t {
	case ActivationAlways, ActivationPathScoped, ActivationOnDemand, ActivationDocumentationOnly:
		return true
	default:
		return false
	}
}

// Validate checks the union invariants: fields that do not belong to the tag
// must be empty, and path-scoped activations must carry at least one include.
func (a Activation) Validate() error {
	if !KnownActivationType(a.Type) {
		return fmt.Errorf("unknown activation type %q", a.Type)
	}
	switch a.Type {
	case ActivationPathScoped:
		if len(a.Include) == 0 {
			return fmt.Errorf("path-scoped activation requires at least one include pattern")
		}
		if a.Trigger != "" || a.InvocationName != "" {
			return fmt.Errorf("path-scoped activation must not carry on-demand fields")
		}
		for _, p := range append(append([]string{}, a.Include...), a.Exclude...) {
			if err := globs.Validate(p); err != nil {
				return err
			}
		}
	case ActivationOnDemand:
		if len(a.Include) > 0 || len(a.Exclude) > 0 {
			return fmt.Errorf("on-demand activation must not carry path patterns")
		}
	case ActivationAlways, ActivationDocumentationOnly:
		if len(a.Include) > 0 || len(a.Exclude) > 0 {
			return fmt.Errorf("%s activation must not carry path patterns", a.Type)
		}
		if a.Trigger != "" || a.InvocationName != "" {
			return fmt.Errorf("%s activation must not carry on-demand fields", a.Type)
		}
	}
	return nil
}

// UnmarshalJSON rejects unknown activation tags instead of defaulting.
func (a *Activation) UnmarshalJSON(b []byte) error {
	type raw Activation
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	if !KnownActivationType(r.Type) {
		return fmt.Errorf("unknown activation type %q", r.Type)
	}
	*a = Activation(r)
	return nil
}

// AgentFacing reports whether content with this activation can ever be loaded
// into an agent request.
func (a Activation) AgentFacing() bool {
	return a.Type != ActivationDocumentationOnly
}
