// Package workspace owns every filesystem effect in Stemma.
//
// Nothing outside this package opens, reads or writes files. Repository paths
// are always slash-separated and always relative to the workspace root;
// platform-native paths only exist at the syscall boundary.
package workspace

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// ErrPathEscape is returned when a repository path leaves the workspace.
var ErrPathEscape = errors.New("path escapes the workspace root")

// MaxPathLength bounds a repository-relative path.
const MaxPathLength = 1024

// NormalizeRel converts a candidate repository path into its canonical
// slash-separated relative form, or fails.
//
// It rejects absolute paths, Windows drive letters, UNC prefixes, backslashes,
// NUL bytes, "." / ".." traversal and empty results. The check is purely
// lexical so it can be applied to generated paths before touching the disk.
func NormalizeRel(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("%w: empty path", ErrPathEscape)
	}
	if len(p) > MaxPathLength {
		return "", fmt.Errorf("%w: path longer than %d bytes", ErrPathEscape, MaxPathLength)
	}
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: path contains a NUL byte", ErrPathEscape)
	}
	if strings.ContainsRune(p, '\\') {
		return "", fmt.Errorf("%w: path contains a backslash separator (%q)", ErrPathEscape, p)
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: path is absolute (%q)", ErrPathEscape, p)
	}
	if hasDriveLetter(p) {
		return "", fmt.Errorf("%w: path carries a drive letter (%q)", ErrPathEscape, p)
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == "" {
		return "", fmt.Errorf("%w: path resolves to the workspace root itself", ErrPathEscape)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: path traverses above the root (%q)", ErrPathEscape, p)
	}
	if strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("%w: path is absolute after cleaning (%q)", ErrPathEscape, p)
	}
	// A leading '~' is rejected after cleaning so that normalization stays
	// idempotent: "./~" and "~" must be treated the same way.
	if strings.HasPrefix(cleaned, "~") {
		return "", fmt.Errorf("%w: path starts with '~' (%q)", ErrPathEscape, p)
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" {
			return "", fmt.Errorf("%w: path contains an empty segment (%q)", ErrPathEscape, p)
		}
		if seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: path contains %q (%q)", ErrPathEscape, seg, p)
		}
	}
	return cleaned, nil
}

func hasDriveLetter(p string) bool {
	if len(p) < 2 || p[1] != ':' {
		return false
	}
	c := p[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// JoinRel joins repository-relative segments and normalizes the result.
func JoinRel(parts ...string) (string, error) {
	nonEmpty := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return NormalizeRel(path.Join(nonEmpty...))
}

// Dir returns the parent of a repository-relative path, or "" at the root.
func Dir(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}
