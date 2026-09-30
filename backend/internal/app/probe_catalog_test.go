package app

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// The review's concurrency counter-example: publishing into a running process while
// another reader lists the directory. Run with -race.
func TestProbeCatalogConcurrentPublishAndRead(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, &scriptedGenerator{})
	project, draft := publishableDraft(t, a, "harbor-concurrent")

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = a.Games()
				_, _ = a.Game("harbor-concurrent")
				_, _, _ = a.GameCover("harbor-concurrent", "r-2026-09-28-00000000")
				_ = a.PackIssues()
			}
		}()
	}

	version := draft.Version
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("concurrent-publish-%d", i)
		payload := draft.Payload
		payload.Description = fmt.Sprintf("第 %d 次发布的说明。", i+1)
		saved, err := a.SaveContentDraft(ctx, draft.DraftID, payload, version)
		if err != nil {
			close(stop)
			readers.Wait()
			t.Fatal(err)
		}
		version = saved.Version
		projectVersion, _, err := a.ReadContentProject(ctx, project.ProjectID)
		if err != nil {
			close(stop)
			readers.Wait()
			t.Fatal(err)
		}
		if _, err = a.PublishContentDraft(ctx, PublishRequest{RequestKey: key, DraftID: draft.DraftID, ExpectedDraftVersion: version, ExpectedProjectVersion: projectVersion.Version}); err != nil {
			close(stop)
			readers.Wait()
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
	if len(a.Games()) == 0 {
		t.Fatal("the catalog lost the published story")
	}
}
