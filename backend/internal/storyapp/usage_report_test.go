package storyapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/storage"
)

// Real-model acceptance keeps its metadata after disposable story data is removed.
func keepRealUsageReport(t *testing.T, a *App) {
	keepUsageReport(t, a, filepath.Join("..", "..", "..", ".gstack", "usage-reports"))
}

func keepUsageReport(t *testing.T, a *App, dir string) {
	t.Helper()
	t.Cleanup(func() {
		_ = a.Close()
		db, err := storage.OpenAppDB(filepath.Join(a.root, "app.db"), appSchema+usageSchema+contentSchema)
		if err != nil {
			t.Error("usage report unavailable", err)
			return
		}
		defer db.Close()
		reader := &App{appDB: db, userID: a.userID}
		page, err := reader.ReadUsage(context.Background(), "", 0)
		if err != nil {
			t.Error(err)
			return
		}
		all := page
		for page.NextBeforeID > 0 {
			page, err = reader.ReadUsage(context.Background(), "", page.NextBeforeID)
			if err != nil {
				t.Error(err)
				return
			}
			all.Calls = append(all.Calls, page.Calls...)
		}
		all.NextBeforeID = 0
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Error(err)
			return
		}
		name := strings.ReplaceAll(t.Name(), "/", "-") + "-" + time.Now().UTC().Format("20060102T150405.000000000") + ".json"
		body, err := json.MarshalIndent(all, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0644); err != nil {
			t.Error(err)
			return
		}
		t.Logf("usage_report=%s calls=%d input=%d output=%d cache_known=%d", path, all.Totals.Calls, all.Totals.InputTokens, all.Totals.OutputTokens, all.Totals.CacheKnown)
	})
}

func TestUsageReportSurvivesDisposableApp(t *testing.T) {
	dir := t.TempDir()
	t.Run("export", func(t *testing.T) {
		a := newTestApp(t, &scriptedGenerator{})
		keepUsageReport(t, a, dir)
		for i := 0; i < 102; i++ {
			if _, err := a.beginUsage(context.Background(), ContextScope{Purpose: "report-test"}, ContextBuildReport{}); err != nil {
				t.Fatal(err)
			}
		}
	})
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	body, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var page UsagePage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	if page.Totals.Calls != 102 || len(page.Calls) != 102 || page.NextBeforeID != 0 || page.Totals.Unconfirmed != 102 {
		t.Fatalf("incomplete report: %+v", page.Totals)
	}
}
