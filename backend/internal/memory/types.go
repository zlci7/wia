// Package memory owns what one character has experienced and what the story may
// offer them later: the committed sources a memory is built from, the digest that
// stands in for the part already summarised, and the subjective states a character
// holds about it.
//
// It does not decide whether an event happened, where anyone is, or what the world
// clock says. Storage owns the database primitives; this package translates those
// records into memory and correction semantics.
package memory

import "errors"

var (
	ErrRebuilding           = errors.New("memory rebuilding")
	ErrStorageUnavailable   = errors.New("storage unavailable")
	ErrContextSourceMissing = errors.New("context source is missing")
)

// MemorySource is one committed experience a character may draw on. It is a
// projection: it names the event it came from and only carries content that
// character was already allowed to perceive.
type MemorySource struct {
	Scope     string `json:"scope"`
	Seq       int64  `json:"seq"`
	ID        string `json:"id"`
	EventID   string `json:"event_id"`
	RunID     string `json:"run_id"`
	Actor     string `json:"actor"`
	Kind      string `json:"kind"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// SubjectiveState is one thing a character currently holds to be true: a belief, a
// relationship, a concern or a commitment, with the sources it was drawn from.
type SubjectiveState struct {
	Kind    string   `json:"kind"`
	Content string   `json:"content"`
	Sources []string `json:"source_ids"`
}

// MemoryDigest is the standing summary of one scope's earlier experience. Through
// marks the sequence it covers; Head records how far the source stream had reached
// when it was written, so a reader can tell what is still summarised out.
type MemoryDigest struct {
	Scope    string            `json:"scope"`
	Revision int64             `json:"revision"`
	Epoch    int64             `json:"epoch"`
	Through  int64             `json:"through_seq"`
	Head     int64             `json:"source_head"`
	Content  string            `json:"content"`
	States   []SubjectiveState `json:"states"`
	Sources  []string          `json:"source_ids"`
}
