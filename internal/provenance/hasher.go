package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

// Hasher streams a canonical digest for large files.
type Hasher struct{ h hash.Hash }

// NewHasher returns a streaming hasher.
func NewHasher() *Hasher { return &Hasher{h: sha256.New()} }

// Write implements io.Writer.
func (h *Hasher) Write(p []byte) (int, error) { return h.h.Write(p) }

// Sum returns the canonical "sha256:<hex>" digest.
func (h *Hasher) Sum() string { return "sha256:" + hex.EncodeToString(h.h.Sum(nil)) }
