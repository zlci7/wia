package memory

import (
	"context"
	"encoding/json"

	"gameagent/backend/internal/storage"
)

// The adapters below translate the storage package's rows into the memory and
// correction types.
//
// They live here on purpose. Storage answers what is written down and knows nothing
// about corrections or memory scopes; the memory package holds the rules and does no
// I/O. Something in between has to name both, and this is it.

func correctionFromRecord(record storage.CorrectionRecord) Correction {
	return Correction{
		Epoch:        record.Epoch,
		Kind:         record.Kind,
		Scope:        record.Scope,
		TargetID:     record.TargetID,
		Original:     record.Original,
		Replacement:  record.Replacement,
		CreatedAt:    record.CreatedAt,
		SceneVersion: record.SceneVersion,
	}
}

func memorySourceFromRecord(record storage.MemorySourceRecord) MemorySource {
	return MemorySource{
		Scope:     record.Scope,
		Seq:       record.Seq,
		ID:        record.ID,
		EventID:   record.EventID,
		RunID:     record.RunID,
		Actor:     record.Actor,
		Kind:      record.Kind,
		Content:   record.Content,
		CreatedAt: record.CreatedAt,
	}
}

// ReadCorrections reads every correction and resolves what each one invalidated.
//
// The dependency walk is a separate query rather than part of the row read, because
// only an event correction has dependents and asking for them for every row would be
// work wasted on the common case.
func ReadCorrections(ctx context.Context, store *storage.WorldStore) ([]Correction, error) {
	records, err := store.LoadCorrections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Correction, 0, len(records))
	for _, record := range records {
		c := correctionFromRecord(record)
		if c.Kind == "event" {
			dependents, err := store.LoadCorrectionDependents(ctx, c.TargetID)
			if err != nil {
				return nil, err
			}
			c.Dependents = dependents
		}
		out = append(out, c)
	}
	return out, nil
}

// CorrectionEventRuns reports which run each corrected event belongs to.
func CorrectionEventRuns(ctx context.Context, store *storage.WorldStore, list []Correction) (map[string]string, error) {
	var eventIDs []string
	for _, c := range list {
		if c.Kind == "event" {
			eventIDs = append(eventIDs, c.TargetID)
		}
	}
	if len(eventIDs) == 0 {
		return map[string]string{}, nil
	}
	return store.LoadEventRunIDs(ctx, eventIDs)
}

// Ready reports whether a rebuild is outstanding, which is a reason not to
// start a new turn. Counting the rows is storage's job; saying what the count means
// is this package's.
func Ready(ctx context.Context, store *storage.WorldStore) error {
	count, err := store.CountUnfinishedMemoryJobs(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrRebuilding
	}
	return nil
}

// ReadDigest reads one scope's standing summary.
func ReadDigest(ctx context.Context, store *storage.WorldStore, scope string) (MemoryDigest, error) {
	d, found, err := readDigestRecord(ctx, store, scope)
	if err != nil || !found {
		return d, err
	}
	identities, err := store.LoadMemorySourceIdentities(ctx, scope, d.Through)
	if err != nil {
		return d, err
	}
	sources := make([]MemorySource, 0, len(identities))
	for _, identity := range identities {
		sources = append(sources, MemorySource{Scope: identity.Scope, Seq: identity.Seq, ID: identity.ID})
	}
	return validateDigestCoverage(d, sources)
}

func ReadDigestAgainst(ctx context.Context, store *storage.WorldStore, scope string, sources []MemorySource) (MemoryDigest, error) {
	d, found, err := readDigestRecord(ctx, store, scope)
	if err != nil || !found {
		return d, err
	}
	return validateDigestCoverage(d, sources)
}

func readDigestRecord(ctx context.Context, store *storage.WorldStore, scope string) (MemoryDigest, bool, error) {
	d := MemoryDigest{Scope: scope, States: []SubjectiveState{}, Sources: []string{}}
	record, found, err := store.LatestMemoryDigest(ctx, scope)
	if err != nil {
		return d, false, err
	}
	if !found {
		return d, false, nil
	}
	d.Revision, d.Epoch, d.Through, d.Head, d.Content = record.Revision, record.Epoch, record.Through, record.Head, record.Content
	if err := json.Unmarshal([]byte(record.States), &d.States); err != nil {
		return d, false, err
	}
	if err := json.Unmarshal([]byte(record.Sources), &d.Sources); err != nil {
		return d, false, err
	}
	return d, true, nil
}

func validateDigestCoverage(d MemoryDigest, sources []MemorySource) (MemoryDigest, error) {
	if !DigestCoverageMatches(d, sources) {
		return d, ErrStorageUnavailable
	}
	return d, nil
}

// ReadSources reads one scope's committed experiences with every correction
// that concerns them already applied.
func ReadSources(ctx context.Context, store *storage.WorldStore, scope string, after int64) ([]MemorySource, error) {
	records, err := store.LoadMemorySources(ctx, scope, after)
	if err != nil {
		return nil, err
	}
	items := make([]MemorySource, 0, len(records))
	for _, record := range records {
		items = append(items, memorySourceFromRecord(record))
	}
	corrections, err := ReadCorrections(ctx, store)
	if err != nil {
		return nil, err
	}
	runs, err := CorrectionEventRuns(ctx, store, corrections)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i] = ReplaceSource(items[i], corrections, runs)
	}
	return items, nil
}

// ExpandCorrection resolves what an event correction invalidates.
func ExpandCorrection(ctx context.Context, store *storage.WorldStore, c *Correction) error {
	if c.Kind != "event" {
		return nil
	}
	dependents, err := store.LoadCorrectionDependents(ctx, c.TargetID)
	if err != nil {
		return err
	}
	c.Dependents = dependents
	return nil
}
