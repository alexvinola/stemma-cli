// Package store reads and writes the repository-local .stemma directory.
//
// It is the only place that knows the on-disk layout of Stemma's own state.
package store

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/alexvinola/stemma-cli/internal/canonical"
	"github.com/alexvinola/stemma-cli/internal/manifest"
	"github.com/alexvinola/stemma-cli/internal/profiles"
	"github.com/alexvinola/stemma-cli/internal/workspace"
)

// Layout constants.
const (
	Dir          = ".stemma"
	ProjectFile  = ".stemma/project.json"
	ManifestFile = ".stemma/manifest.json"
	ProfilesDir  = ".stemma/profiles"
	RecoveryDir  = ".stemma/recovery"
)

// ErrNoProject reports that the workspace has no canonical project.
var ErrNoProject = errors.New("no canonical project found")

// ProfilePath returns the profile path for a target.
func ProfilePath(target canonical.TargetFormat) string {
	return path.Join(ProfilesDir, string(target)+".json")
}

// HasProject reports whether a canonical project exists.
func HasProject(ws *workspace.Workspace) (bool, error) {
	return ws.Exists(ProjectFile)
}

// LoadProfile reads a target profile, returning a default when absent.
func LoadProfile(
	ctx context.Context, ws *workspace.Workspace, target canonical.TargetFormat, override string,
) (profiles.Profile, string, error) {
	p := override
	if p == "" {
		p = ProfilePath(target)
	}
	exists, err := ws.Exists(p)
	if err != nil {
		return profiles.Profile{}, p, err
	}
	if !exists {
		if override != "" {
			return profiles.Profile{}, p, fmt.Errorf("profile %s does not exist", p)
		}
		return profiles.Default(target), p, nil
	}
	f, err := ws.ReadFile(ctx, p)
	if err != nil {
		return profiles.Profile{}, p, fmt.Errorf("read %s: %w", p, err)
	}
	prof, err := profiles.Unmarshal(f.Data)
	if err != nil {
		return profiles.Profile{}, p, fmt.Errorf("%s: %w", p, err)
	}
	if prof.Target != target {
		return profiles.Profile{}, p, fmt.Errorf("%s declares target %q but %q was requested",
			p, prof.Target, target)
	}
	return prof, p, nil
}

// SaveProfile writes a target profile atomically.
func SaveProfile(ws *workspace.Workspace, prof profiles.Profile) error {
	data, err := profiles.Marshal(prof)
	if err != nil {
		return err
	}
	return writeFile(ws, ProfilePath(prof.Target), data)
}

// LoadManifest reads the manifest, returning an empty one when absent.
func LoadManifest(ctx context.Context, ws *workspace.Workspace) (manifest.Manifest, error) {
	exists, err := ws.Exists(ManifestFile)
	if err != nil {
		return manifest.Manifest{}, err
	}
	if !exists {
		return manifest.New(), nil
	}
	f, err := ws.ReadFile(ctx, ManifestFile)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("read %s: %w", ManifestFile, err)
	}
	m, err := manifest.Unmarshal(f.Data)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	return m, nil
}

// SaveManifest writes the manifest atomically.
func SaveManifest(ws *workspace.Workspace, m manifest.Manifest) error {
	data, err := manifest.Marshal(m)
	if err != nil {
		return err
	}
	return writeFile(ws, ManifestFile, data)
}

func writeFile(ws *workspace.Workspace, rel string, data []byte) error {
	tx := ws.Begin()
	if err := tx.Add(workspace.WriteOp{Path: rel, Content: data, Mode: 0o644}); err != nil {
		return err
	}
	return tx.Commit()
}
