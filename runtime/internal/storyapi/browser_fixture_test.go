package storyapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gameagent/runtime/internal/model"
	"gameagent/runtime/internal/storyapp"
)

type browserGenerator struct{ apiGenerator }

func (g browserGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	select {
	case <-ctx.Done():
		return model.TextResponse{}, ctx.Err()
	case <-time.After(450 * time.Millisecond):
	}
	// The suggestion call has its own role and response contract; without this the
	// generic stub would answer it with an NPC payload.
	if strings.Contains(request.System, "玩家行动建议助手") {
		return model.TextResponse{Text: `{"items":["我向沈岚问候。","我看看窗外的河面。","我在一旁稍作停留。"]}`, Diagnostic: model.TextDiagnostic{Provider: "fixture", Model: "controlled-fixture", InputKnown: true, OutputKnown: true, ReasoningKnown: true, CacheKnown: true, InputTokens: 100, OutputTokens: 30, ReasoningTokens: 10, CacheHitTokens: 60, CacheMissTokens: 40}}, nil
	}
	return g.apiGenerator.GenerateText(ctx, request)
}

// Opt-in fixture serves disposable data and the real API/client for browser verification.
func TestBrowserFixture(t *testing.T) {
	if os.Getenv("WIA_BROWSER_FIXTURE") != "1" {
		t.Skip("interactive browser fixture")
	}
	root := t.TempDir()
	app, err := storyapp.Open(context.Background(), storyapp.Options{DataRoot: root, Generator: browserGenerator{}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for index, name := range []string{"雨夜的第一晚", "另一段旅程"} {
		world, err := app.CreateWorld(context.Background(), name, "guided", "旅人", "寻找答案", index == 0)
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", filepath.Join(root, "story-app", "worlds", storyapp.LocalUserID, storyapp.GameID, world.WorldID, "world.db"))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		for seq := 2; seq <= 251; seq++ {
			kind, content := "narrative", fmt.Sprintf("第 %d 段。雨声轻敲着旧渡口客栈的窗沿，沈岚把灯拨亮了一些。你望向窗外，河面上的船影缓缓远去。\n\n铁杉放下酒杯，等着你继续说话。", seq)
			if seq%2 == 0 {
				kind = "player"
				content = fmt.Sprintf("第 %d 次：我向老板问起渡口的消息。", seq)
			}
			// One completed run per turn keeps the seeded history shaped like real
			// play, so personal memory indexes it as per-turn groups.
			run := fmt.Sprintf("fixture-run-%d-%d", index, seq/2)
			if _, err = tx.Exec(`INSERT OR IGNORE INTO runs(run_id,request_key,request_hash,input,addressee_id,attempt,status,created_at,updated_at) VALUES(?,?,?,'','',1,'completed',?,?)`, run, run, run, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(`INSERT INTO messages(seq,message_id,kind,content,run_id,created_at) VALUES(?,?,?,?,?,?)`, seq, fmt.Sprintf("fixture-%d-%d", index, seq), kind, content, run, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err = tx.Exec(`UPDATE meta SET value='251' WHERE key='message_head'`); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	assets := os.DirFS(filepath.Clean("../../../console/dist"))
	server, err := New(Options{Addr: "127.0.0.1:0", App: app, Assets: assets, Version: "browser-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown()
	go server.Serve()
	bootstrap, _ := url.Parse(server.BrowserURL())
	response, err := http.Post(server.URL()+"/api/session", "application/json", strings.NewReader(fmt.Sprintf(`{"token":%q}`, strings.TrimPrefix(bootstrap.Fragment, "token="))))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cookies := response.Cookies()
	target, _ := url.Parse(server.URL())
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		if r.Header.Get("Origin") != "" {
			r.Header.Set("Origin", server.URL())
		}
		for _, c := range cookies {
			r.AddCookie(c)
		}
	}
	done := make(chan struct{})
	var finish sync.Once
	var controls sync.Mutex
	ready, failPath, delayPath := true, "", ""
	delayMS := 1800
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture/finish" && r.Method == http.MethodPost {
			w.WriteHeader(204)
			finish.Do(func() { close(done) })
			return
		}
		if r.URL.Path == "/fixture/control" && r.Method == http.MethodPost {
			var request struct {
				Ready     *bool  `json:"ready"`
				FailPath  string `json:"fail_path"`
				DelayPath string `json:"delay_path"`
				DelayMS   int    `json:"delay_ms"`
				Append    int    `json:"append"`
				WorldID   string `json:"world_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				writeError(w, 400, "fixture", err.Error())
				return
			}
			controls.Lock()
			if request.Ready != nil {
				ready = *request.Ready
			}
			failPath, delayPath = request.FailPath, request.DelayPath
			delayMS = 1800
			if request.DelayMS > 0 && request.DelayMS <= 30000 {
				delayMS = request.DelayMS
			}
			controls.Unlock()
			if request.Append > 0 && request.Append <= 400 {
				list, err := app.ListWorlds(r.Context())
				if err != nil {
					writeError(w, 500, "fixture", err.Error())
					return
				}
				found := false
				for _, world := range list {
					if world.WorldID == request.WorldID {
						found = true
					}
				}
				if !found {
					writeError(w, 404, "fixture", "unknown fixture world")
					return
				}
				db, err := sql.Open("sqlite", filepath.Join(root, "story-app", "worlds", storyapp.LocalUserID, storyapp.GameID, request.WorldID, "world.db"))
				if err != nil {
					writeError(w, 500, "fixture", err.Error())
					return
				}
				defer db.Close()
				tx, err := db.Begin()
				if err != nil {
					writeError(w, 500, "fixture", err.Error())
					return
				}
				defer tx.Rollback()
				var head int
				if err = tx.QueryRow(`SELECT MAX(seq) FROM messages`).Scan(&head); err != nil {
					writeError(w, 500, "fixture", err.Error())
					return
				}
				for i := 1; i <= request.Append; i++ {
					seq := head + i
					_, err = tx.Exec(`INSERT INTO messages(seq,message_id,kind,content,run_id,created_at) VALUES(?,?,'narrative',?,'',?)`, seq, fmt.Sprintf("append-%d", seq), fmt.Sprintf("新到的故事 %d：河面上传来船笛声。", seq), time.Now().UTC().Format(time.RFC3339Nano))
					if err != nil {
						writeError(w, 500, "fixture", err.Error())
						return
					}
				}
				if _, err = tx.Exec(`UPDATE meta SET value=? WHERE key='message_head'`, fmt.Sprint(head+request.Append)); err != nil {
					writeError(w, 500, "fixture", err.Error())
					return
				}
				if err = tx.Commit(); err != nil {
					writeError(w, 500, "fixture", err.Error())
					return
				}
			}
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
		controls.Lock()
		fail, delay := r.Method+" "+r.URL.Path == failPath, r.Method+" "+r.URL.Path == delayPath
		if fail {
			failPath = ""
		}
		if delay {
			delayPath = ""
		}
		connected := ready
		delayDuration := time.Duration(delayMS) * time.Millisecond
		controls.Unlock()
		if delay {
			time.Sleep(delayDuration)
		}
		if fail {
			writeError(w, 503, "fixture_failure", "测试连接失败，请重试。")
			return
		}
		if r.URL.Path == "/api/v1/status" && r.Method == http.MethodGet {
			state, err := app.Status(r.Context())
			if err != nil {
				writeAppError(w, err)
				return
			}
			state.Ready = connected
			state.Model.Configured = connected
			writeJSON(w, 200, map[string]any{"status": state})
			return
		}
		if r.URL.Path == "/api/v1/model-profiles" && r.Method == http.MethodPost {
			controls.Lock()
			ready = true
			controls.Unlock()
			state, err := app.Status(r.Context())
			if err != nil {
				writeAppError(w, err)
				return
			}
			writeJSON(w, 200, state)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	local := httptest.NewUnstartedServer(handler)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	local.Listener = listener
	local.Start()
	defer local.Close()
	fmt.Println("UI_FIXTURE_URL=" + local.URL)
	select {
	case <-done:
	case <-time.After(20 * time.Minute):
	}
}
