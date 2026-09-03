// Package version exposes build and compatibility identifiers.
//
// Every value here is a compile-time constant. Nothing in Stemma may depend on
// the wall clock, the network or the environment to determine these values,
// because they participate in manifests and plans.
package version

// Version is the Stemma CLI version.
//
// This is a var, not a const, so a release build can override it with
// -ldflags "-X .../internal/version.Version=...". An ordinary `go build` or
// `go install` leaves it at this default. Overriding it only changes what
// `stemma version` reports; it never affects compilation, hashing or any
// other behaviour, which is why version.go documents everything else here as
// a compile-time constant.
var Version = "0.1.0"

// CanonicalSchemaVersion is the version of the canonical project layout.
//
// Version 2 stores entities as Markdown files under .stemma/, with project
// metadata in .stemma/project.json and machine bookkeeping in
// .stemma/provenance.json. Version 1 held everything in one JSON document.
const CanonicalSchemaVersion = 2

// ProfileSchemaVersion is the version of target projection profiles.
const ProfileSchemaVersion = 1

// ManifestSchemaVersion is the version of .stemma/manifest.json.
const ManifestSchemaVersion = 1

// ReportSchemaVersion is the version of the JSON output contract.
const ReportSchemaVersion = 1

// PlanSchemaVersion is the version of a serialized compilation plan.
const PlanSchemaVersion = 1

// ImporterVersion identifies the importer generation recorded in provenance.
// Bump it when importer output changes in a way that affects canonical output.
const ImporterVersion = "1"

// CompatibilityBaseline identifies the provider-behaviour baseline that the
// adapters were written against. See docs/provider-compatibility.md.
const CompatibilityBaseline = "2026-09-provider-baseline-1"
