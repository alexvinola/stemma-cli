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
	// A colon can never appear in a repository path. It carries a Windows drive
	// letter ("C:/x"), and on NTFS it also opens an alternate data stream
	// ("notes.md:hidden"), so refusing it outright is both simpler and safer
	// than pattern-matching drive letters. The check is repeated after cleaning
	// below, because "./A:" hides a drive letter from a prefix test.
	if strings.ContainsRune(p, ':') {
		return "", fmt.Errorf("%w: path contains ':' (%q)", ErrPathEscape, p)
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
	// These are checked after cleaning so that normalization stays idempotent:
	// "./~" and "~", or "./A:" and "A:", must be treated the same way.
	if strings.HasPrefix(cleaned, "~") {
		return "", fmt.Errorf("%w: path starts with '~' (%q)", ErrPathEscape, p)
	}
	if strings.ContainsRune(cleaned, ':') {
		return "", fmt.Errorf("%w: path contains ':' after cleaning (%q)", ErrPathEscape, p)
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
