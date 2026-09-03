package canonical

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

const maxSlugLength = 64

// Slug converts free text into a stable, lowercase, ASCII identifier segment.
//
// The transformation is deterministic and locale-independent: only ASCII
// letters and digits survive; every other rune becomes a separator. Non-ASCII
// text that reduces to nothing yields an empty slug, and callers must supply a
// deterministic fallback.
func Slug(s string) string {
	var b strings.Builder
	lastHyphen := true // suppresses a leading hyphen
	for _, r := range s {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(unicode.ToLower(r))
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
		if b.Len() >= maxSlugLength {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > maxSlugLength {
		out = strings.Trim(out[:maxSlugLength], "-")
	}
	return out
}

// SlugOrHash returns Slug(s), or a deterministic hash-derived slug built from
// fallback when s contains no usable ASCII characters.
func SlugOrHash(s, fallback string) string {
	if v := Slug(s); v != "" {
		return v
	}
	sum := sha256.Sum256([]byte(fallback + "\x00" + s))
	return "x" + hex.EncodeToString(sum[:])[:12]
}

// MakeID composes an entity identifier such as "rule.api-conventions".
func MakeID(t EntityType, slug string) string {
	return string(t) + "." + slug
}

// ParseID splits an entity identifier into its type and slug.
func ParseID(id string) (EntityType, string, error) {
	i := strings.IndexByte(id, '.')
	if i <= 0 || i == len(id)-1 {
		return "", "", fmt.Errorf("malformed entity id %q: expected \"<type>.<slug>\"", id)
	}
	t := EntityType(id[:i])
	for _, k := range AllEntityTypes() {
		if k == t {
			return t, id[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("malformed entity id %q: unknown entity type %q", id, t)
}

// ValidIDSlug reports whether a slug is well formed.
func ValidIDSlug(slug string) bool {
	if slug == "" || len(slug) > maxSlugLength {
		return false
	}
	if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		return false
	}
	prevHyphen := false
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			prevHyphen = false
		case r == '-':
			if prevHyphen {
				return false
			}
			prevHyphen = true
		default:
			return false
		}
	}
	return true
}

// Allocator hands out unique entity IDs deterministically. Collisions are
// resolved by appending "-2", "-3", ... in allocation order.
type Allocator struct {
	used map[string]int
}

// NewAllocator returns an empty allocator.
func NewAllocator() *Allocator { return &Allocator{used: map[string]int{}} }

// Allocate returns a unique ID for the entity type and desired slug.
func (a *Allocator) Allocate(t EntityType, slug, fallback string) string {
	if a.used == nil {
		a.used = map[string]int{}
	}
	slug = SlugOrHash(slug, fallback)
	base := MakeID(t, slug)
	n, seen := a.used[base]
	if !seen {
		a.used[base] = 1
		return base
	}
	for {
		n++
		candidate := fmt.Sprintf("%s-%d", base, n)
		if _, taken := a.used[candidate]; !taken {
			a.used[base] = n
			a.used[candidate] = 1
			return candidate
		}
	}
}

// Reserve records an existing ID so later allocations do not collide with it.
func (a *Allocator) Reserve(id string) {
	if a.used == nil {
		a.used = map[string]int{}
	}
	if _, ok := a.used[id]; !ok {
		a.used[id] = 1
	}
}
