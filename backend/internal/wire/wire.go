// Package wire holds the small, dependency-free primitives that the narrative
// packages share: text normalization, timestamps, JSON encoding and identity
// generation.
//
// They live here, rather than in one of the packages that happens to use them,
// because everything depends on them and they depend on nothing. A primitive
// that every file reaches for belongs to no single responsibility: keeping it
// inside one of them means every other package imports that package to get it,
// which is how a module ends up depending on an unrelated one.
//
// Domain vocabulary does not belong here. Character, Event, GameTime and the
// rules that read them belong to the world package; this package is only what
// remains once the domain concepts are named.
package wire

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Clean normalizes text that came from a model, a pack or a request: surrounding
// whitespace is dropped and embedded NUL bytes are removed so a value stays
// storable in SQLite text columns.
func Clean(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
}

// NowText is the timestamp format used for every persisted created_at and
// updated_at column.
func NowText() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// MarshalJSON encodes a value for a text column. The values stored this way are
// always types this package already validated, so an encoding failure cannot be
// reported to a caller meaningfully; an empty string is stored instead.
func MarshalJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

// BoolInt maps a boolean to the integer SQLite stores.
func BoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// NewID returns a new identifier with the given prefix. The random suffix makes
// identifiers unique across processes, unlike a counter.
func NewID(prefix string) string {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(data[:])
}
