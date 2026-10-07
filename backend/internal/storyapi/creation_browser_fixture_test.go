package storyapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gameagent/backend/internal/app"
	"gameagent/backend/internal/llm"
	"gameagent/backend/internal/model"
)

type creationBrowserGenerator struct {
	inner     model.TextGenerator
	positions map[string]string
	calls     atomic.Int32
	delay     atomic.Bool
	invalid   atomic.Bool
	dir       string
	maxCalls  int32
}

func (g *creationBrowserGenerator) SupportsTextStreaming() bool {
	if g.inner == nil {
		return true
	}
	p, ok := g.inner.(model.TextStreamingProvider)
	return ok && p.SupportsTextStreaming()
}
func (g *creationBrowserGenerator) ModelWindow() model.WindowLimits {
	if p, ok := g.inner.(model.WindowProvider); ok {
		return p.ModelWindow()
	}
	return model.WindowLimits{}
}
func (g *creationBrowserGenerator) GenerateText(ctx context.Context, request model.TextRequest) (model.TextResponse, error) {
	index := g.calls.Add(1)
	if g.inner != nil && index > g.maxCalls {
		return model.TextResponse{}, errors.New("browser verification request budget exhausted")
	}
	started := time.Now()
	first := int64(0)
	observer := request.OnDelta
	request.OnDelta = func(delta model.TextDelta) {
		if first == 0 && delta.Text != "" {
			first = time.Since(started).Milliseconds()
		}
		if observer != nil {
			observer(delta)
		}
	}
	var response model.TextResponse
	var err error
	if g.inner != nil {
		response, err = g.inner.GenerateText(ctx, request)
	} else {
		var text string
		if strings.Contains(request.System, "玩家行动建议助手") {
			text = `{"items":["我请马丁说明最后一次见面时发生了什么，尤其是他停顿的那一刻，先听完他的说法再决定下一步。","我仔细核对桌上的照片和地址纸条，记下能够确认的细节，再向马丁核实其中不清楚的地方。","我请书记员谈谈刚才注意到的反应，把她的观察与马丁的说法对照，寻找值得继续追问的问题。"]}`
		} else {
			data, _ := json.Marshal(map[string]any{"scene_changes": map[string]any{"elapsed_minutes": 2, "positions": g.positions}, "narrative": "马丁把帽子放在膝上。\n\n“她说想自己接一单活计。”他说，“我担心陌生顾客，却没有仔细听她解释。”\n\n书记员抬起头，没有插话。你能接着核对那天的订单与地址。"})
			text = string(data)
			if g.invalid.Load() {
				text = `{"narrative":"候选正文","scene_changes":{"positions":{"player":"invalid"}}}`
			}
		}
		if request.Streams() && request.OnDelta != nil {
			for _, r := range text {
				request.OnDelta(model.TextDelta{Text: string(r)})
				select {
				case <-ctx.Done():
					err = ctx.Err()
				case <-time.After(2 * time.Millisecond):
				}
				if err != nil {
					break
				}
			}
		}
		if err == nil && g.delay.Load() {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(60 * time.Second):
			}
		}
		response.Text = text
	}
	if g.dir != "" {
		record := struct {
			Index                  int                 `json:"index"`
			Streaming              bool                `json:"streaming"`
			Reasoning              model.ReasoningMode `json:"reasoning"`
			ElapsedMS, FirstTextMS int64
			Response               string               `json:"response"`
			Diagnostic             model.TextDiagnostic `json:"diagnostic"`
			ErrorCode              string               `json:"error_code"`
		}{int(index), request.Streams(), request.Reasoning, time.Since(started).Milliseconds(), first, response.Text, response.Diagnostic, model.TextErrorCode(err)}
		data, _ := json.MarshalIndent(record, "", "  ")
		if writeErr := os.WriteFile(filepath.Join(g.dir, fmt.Sprintf("call-%02d.json", index)), data, 0644); writeErr != nil {
			return response, writeErr
		}
	}
	return response, err
}

// This fixture keeps the real API and built UI, with a disposable application root.
// Its proxy supplies only its own local bootstrap cookie; credentials are never emitted.
func TestCreationBrowserFixture(t *testing.T) {
	if os.Getenv("WIA_CREATION_BROWSER") != "1" {
		t.Skip("interactive browser verification")
	}
	g := &creationBrowserGenerator{}
	g.maxCalls = 16
	if os.Getenv("WIA_CREATION_LIVE") == "1" {
		if value := os.Getenv("WIA_CREATION_MAX_CALLS"); value != "" {
			limit, err := strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 16 {
				t.Fatal("invalid browser request budget")
			}
			g.maxCalls = int32(limit)
		}
		provider, config, err := llm.NewProviderFromConfigFile(os.Getenv("WIA_LIVE_MODEL_CONFIG"))
		if err != nil {
			t.Fatal("real model configuration could not be loaded")
		}
		if config.Provider == "fake" {
			t.Fatal("real provider required")
		}
		var ok bool
		g.inner, ok = provider.(model.TextGenerator)
		if !ok {
			t.Fatal("text model required")
		}
		g.dir = os.Getenv("WIA_LIVE_EVIDENCE")
		if g.dir == "" {
			t.Fatal("evidence directory required")
		}
		if err := os.MkdirAll(g.dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	a, err := app.Open(t.Context(), app.Options{DataRoot: t.TempDir(), StoryPacksPath: apiStoryPacks(t), Generator: g})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	pack, _ := a.Pack("mist-embers")
	g.positions = pack.Definition.InitialLocations
	s, err := New(Options{Addr: "127.0.0.1:0", App: a, Assets: os.DirFS("../webdist/dist")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	go s.Serve()
	u, _ := url.Parse(s.BrowserURL())
	response, err := http.Post(s.URL()+"/api/session", "application/json", strings.NewReader(fmt.Sprintf(`{"token":%q}`, strings.TrimPrefix(u.Fragment, "token="))))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cookies := response.Cookies()
	target, _ := url.Parse(s.URL())
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = target.Host
		if r.Header.Get("Origin") != "" {
			r.Header.Set("Origin", s.URL())
		}
		for _, c := range cookies {
			r.AddCookie(c)
		}
	}
	done := make(chan struct{})
	var finish sync.Once
	local := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/fixture/finish" {
			finish.Do(func() { close(done) })
			w.WriteHeader(204)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/fixture/control" && g.inner == nil {
			var request struct {
				Delay   bool `json:"delay"`
				Invalid bool `json:"invalid"`
			}
			if !decodeJSON(w, r, &request) {
				return
			}
			g.delay.Store(request.Delay)
			g.invalid.Store(request.Invalid)
			w.WriteHeader(204)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	local.Listener = listener
	local.Start()
	defer local.Close()
	fmt.Println("CREATION_BROWSER_URL=" + local.URL)
	select {
	case <-done:
	case <-time.After(45 * time.Minute):
	}
}
