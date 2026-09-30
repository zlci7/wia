package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// These packages and registered hashes predate structured bystander identities.
// The fixture comes from 605a8de, so it cannot drift with the current serializer.
func TestOriginalSchemaV1CatalogCanCreateWorldsAfterUpgrade(t *testing.T) {
	var fixtures map[string]struct {
		Digest string                     `json:"digest"`
		Files  map[string]json.RawMessage `json:"files"`
	}
	body, err := os.ReadFile("testdata/pack-digests-schema-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"unchanged", "changed story", "changed NPC", "structured bystander", "wrong digest version"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a := newTestApp(t, &scriptedGenerator{})
			packRoot := t.TempDir()
			for id, fixture := range fixtures {
				for name, original := range fixture.Files {
					data := original
					var value map[string]any
					if err := json.Unmarshal(data, &value); err != nil {
						t.Fatal(err)
					}
					if name == "story.json" {
						switch scenario {
						case "changed story":
							value["background"] = "Changed content under the same revision"
						case "structured bystander":
							names := value["bystanders"].([]any)
							for i, name := range names {
								names[i] = map[string]any{"name": name}
							}
						}
					} else if scenario == "changed NPC" {
						value["profile"] = "Changed character profile"
					}
					data, err = json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(packRoot, id, filepath.FromSlash(name))
					if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, data, 0644); err != nil {
						t.Fatal(err)
					}
				}
				version := 1
				if scenario == "wrong digest version" {
					version = 2
				}
				if _, err := a.appDB.ExecContext(ctx, `INSERT INTO pack_revisions(game_id,revision,digest,digest_version) VALUES(?,?,?,?)`, id, id+".pack.v1", fixture.Digest, version); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			restarted, err := Open(ctx, Options{DataRoot: a.dataRoot, StoryPacksPath: packRoot, Generator: &scriptedGenerator{}})
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if scenario != "unchanged" {
				if len(restarted.Games()) != 0 || len(restarted.PackIssues()) != 2 {
					t.Fatalf("changed or unrecognized packages accepted: games=%v issues=%v", restarted.Games(), restarted.PackIssues())
				}
				for id, fixture := range fixtures {
					var digest string
					if err := restarted.appDB.QueryRowContext(ctx, `SELECT digest FROM pack_revisions WHERE game_id=? AND revision=?`, id, id+".pack.v1").Scan(&digest); err != nil {
						t.Fatal(err)
					}
					if digest != fixture.Digest {
						t.Fatal("rejection changed revision registration")
					}
				}
				return
			}
			if len(restarted.Games()) != 2 || len(restarted.PackIssues()) != 0 {
				t.Fatalf("unchanged original catalog rejected: %v", restarted.PackIssues())
			}
			for id := range fixtures {
				pack, _ := restarted.Pack(id)
				var digest string
				var version int
				if err := restarted.appDB.QueryRowContext(ctx, `SELECT digest,digest_version FROM pack_revisions WHERE game_id=? AND revision=?`, id, id+".pack.v1").Scan(&digest, &version); err != nil {
					t.Fatal(err)
				}
				if digest != pack.Digest || version != packDigestVersion {
					t.Fatalf("unmigrated registration: %s v%d", digest, version)
				}
				if _, err := restarted.CreateStoryWorld(ctx, CreateWorldRequest{GameID: id, ExpectedRevision: id + ".pack.v1", RequestKey: "upgraded-" + id, Activate: true}); err != nil {
					t.Fatal(err)
				}
			}
			if err := restarted.Close(); err != nil {
				t.Fatal(err)
			}
			again, err := Open(ctx, Options{DataRoot: a.dataRoot, StoryPacksPath: packRoot, Generator: &scriptedGenerator{}})
			if err != nil {
				t.Fatal(err)
			}
			defer again.Close()
			if len(again.Games()) != 2 || len(again.PackIssues()) != 0 {
				t.Fatal("migrated catalog changed on restart")
			}
		})
	}
}

// C01: a database written by the previous release holds first-algorithm digests. An
// unchanged story must still be usable after the upgrade, and the record must be
// migrated instead of reported as changed content.
func TestLegacyPackDigestIsMigratedNotReportedAsChanged(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	for _, gameID := range []string{"lantern-dusk", "orbital-repair"} {
		pack, ok := a.Pack(gameID)
		if !ok {
			t.Fatalf("%s not loaded", gameID)
		}
		if pack.LegacyDigest == "" || pack.LegacyDigest == pack.Digest {
			t.Fatalf("%s: the two algorithms must differ for this probe to mean anything", gameID)
		}
		// Rewrite the registry the way the previous release left it.
		if _, err := a.appDB.ExecContext(ctx, `UPDATE pack_revisions SET digest=?,digest_version=1 WHERE game_id=? AND revision=?`, pack.LegacyDigest, gameID, pack.Definition.Revision); err != nil {
			t.Fatal(err)
		}
		var check string
		if err := a.appDB.QueryRowContext(ctx, `SELECT digest FROM pack_revisions WHERE game_id=? AND revision=?`, gameID, pack.Definition.Revision).Scan(&check); err != nil {
			t.Fatal(err)
		}
		if check != pack.LegacyDigest {
			t.Fatalf("%s: the probe could not write the legacy digest", gameID)
		}
	}
	// Restart the application against the same data root.
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(ctx, Options{DataRoot: a.dataRoot, Generator: &scriptedGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if issues := restarted.PackIssues(); len(issues) != 0 {
		t.Fatalf("an unchanged story was reported as changed after an upgrade: %+v", issues)
	}
	for _, gameID := range []string{"lantern-dusk", "orbital-repair"} {
		pack, ok := restarted.Pack(gameID)
		if !ok {
			t.Fatalf("%s is missing from the catalog after the upgrade", gameID)
		}
		var digest string
		var version int
		if err := restarted.appDB.QueryRowContext(ctx, `SELECT digest,digest_version FROM pack_revisions WHERE game_id=? AND revision=?`, gameID, pack.Definition.Revision).Scan(&digest, &version); err != nil {
			t.Fatal(err)
		}
		if digest != pack.Digest || version != packDigestVersion {
			t.Fatalf("%s digest was not migrated: %q v%d", gameID, digest, version)
		}
		// And a new world can be started from it.
		if _, err := restarted.CreateStoryWorld(ctx, CreateWorldRequest{GameID: gameID, ExpectedRevision: pack.Definition.Revision, RequestKey: "after-upgrade-" + gameID, Activate: true}); err != nil {
			t.Fatalf("%s could not start a world after the upgrade: %v", gameID, err)
		}
	}
	// Genuinely edited content is still refused, so the migration is not a blanket pass.
	pack, _ := restarted.Pack("lantern-dusk")
	if _, err := restarted.appDB.ExecContext(ctx, `UPDATE pack_revisions SET digest='not-the-real-digest',digest_version=? WHERE game_id=? AND revision=?`, packDigestVersion, "lantern-dusk", pack.Definition.Revision); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(ctx, Options{DataRoot: a.dataRoot, Generator: &scriptedGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if _, ok := again.Pack("lantern-dusk"); ok {
		t.Fatal("a genuinely changed story was accepted")
	}
	if len(again.PackIssues()) == 0 {
		t.Fatal("a genuinely changed story was not reported")
	}
}

// The two algorithms must agree on what they exclude: the legacy digest is exactly the
// old canonical form, so a database written before the change is recognised.
func TestLegacyDigestMatchesTheOldCanonicalForm(t *testing.T) {
	a := newTestApp(t, &scriptedGenerator{})
	pack, ok := a.Pack("lantern-dusk")
	if !ok {
		t.Fatal("pack missing")
	}
	// Recompute the first algorithm from the same inputs the loader used.
	raw, err := loadPack(filepath.Join(a.PackRoot(), "lantern-dusk"))
	if err != nil {
		t.Fatal(err)
	}
	npcBodies := make([]json.RawMessage, 0, len(raw.Story.NPCs))
	for _, name := range raw.Story.NPCs {
		npcBodies = append(npcBodies, raw.NPCFiles[name])
	}
	canonical, _ := json.Marshal(struct {
		Story StoryPack
		NPCs  []json.RawMessage
		Cover []byte
	}{raw.Story, npcBodies, raw.Cover})
	sum := sha256.Sum256(canonical)
	if got := hex.EncodeToString(sum[:]); got != pack.LegacyDigest {
		t.Fatalf("legacy digest does not reproduce the previous algorithm: %s vs %s", got, pack.LegacyDigest)
	}
}
