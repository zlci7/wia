package storyapp

import (
	"context"
	"encoding/json"

	"gameagent/backend/internal/memorymodel"
	"gameagent/backend/internal/storage"
)

// The adapters below translate the storage package's rows into the memory and
// correction types.
//
// They live here on purpose. Storage answers what is written down and knows nothing
// about corrections or memory scopes; the memory package holds the rules and does no
// I/O. Something in between has to name both, and this is it.

func correctionFromRecord(record storage.CorrectionRecord) memorymodel.Correction {
	return memorymodel.Correction{
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

func memorySourceFromRecord(record storage.MemorySourceRecord) memorymodel.MemorySource {
	return memorymodel.MemorySource{
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

// readCorrections reads every correction and resolves what each one invalidated.
//
// The dependency walk is a separate query rather than part of the row read, because
// only an event correction has dependents and asking for them for every row would be
// work wasted on the common case.
func readCorrections(ctx context.Context, store *storage.WorldStore) ([]memorymodel.Correction, error) {
	records, err := store.LoadCorrections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]memorymodel.Correction, 0, len(records))
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

// correctionEventRuns reports which run each corrected event belongs to.
func correctionEventRuns(ctx context.Context, store *storage.WorldStore, list []memorymodel.Correction) (map[string]string, error) {
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

// memoryReady reports whether a rebuild is outstanding, which is a reason not to
// start a new turn. Counting the rows is storage's job; saying what the count means
// is this package's.
func memoryReady(ctx context.Context, store *storage.WorldStore) error {
	count, err := store.CountUnfinishedMemoryJobs(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrMemoryRebuilding
	}
	return nil
}

// readDigest reads one scope's standing summary.
func readDigest(ctx context.Context, store *storage.WorldStore, scope string) (memorymodel.MemoryDigest, error) {
	d, found, err := readDigestRecord(ctx, store, scope)
	if err != nil || !found {
		return d, err
	}
	identities, err := store.LoadMemorySourceIdentities(ctx, scope, d.Through)
	if err != nil {
		return d, err
	}
	sources := make([]memorymodel.MemorySource, 0, len(identities))
	for _, identity := range identities {
		sources = append(sources, memorymodel.MemorySource{Scope: identity.Scope, Seq: identity.Seq, ID: identity.ID})
	}
	return validateDigestCoverage(d, sources)
}

func readDigestAgainst(ctx context.Context, store *storage.WorldStore, scope string, sources []memorymodel.MemorySource) (memorymodel.MemoryDigest, error) {
	d, found, err := readDigestRecord(ctx, store, scope)
	if err != nil || !found {
		return d, err
	}
	return validateDigestCoverage(d, sources)
}

func readDigestRecord(ctx context.Context, store *storage.WorldStore, scope string) (memorymodel.MemoryDigest, bool, error) {
	d := memorymodel.MemoryDigest{Scope: scope, States: []memorymodel.SubjectiveState{}, Sources: []string{}}
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

func validateDigestCoverage(d memorymodel.MemoryDigest, sources []memorymodel.MemorySource) (memorymodel.MemoryDigest, error) {
	if !memorymodel.DigestCoverageMatches(d, sources) {
		return d, ErrStorageUnavailable
	}
	return d, nil
}

// readMemorySources reads one scope's committed experiences with every correction
// that concerns them already applied.
func readMemorySources(ctx context.Context, store *storage.WorldStore, scope string, after int64) ([]memorymodel.MemorySource, error) {
	records, err := store.LoadMemorySources(ctx, scope, after)
	if err != nil {
		return nil, err
	}
	items := make([]memorymodel.MemorySource, 0, len(records))
	for _, record := range records {
		items = append(items, memorySourceFromRecord(record))
	}
	corrections, err := readCorrections(ctx, store)
	if err != nil {
		return nil, err
	}
	runs, err := correctionEventRuns(ctx, store, corrections)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i] = memorymodel.ReplaceSource(items[i], corrections, runs)
	}
	return items, nil
}

// expandCorrection resolves what an event correction invalidates.
func expandCorrection(ctx context.Context, store *storage.WorldStore, c *memorymodel.Correction) error {
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
