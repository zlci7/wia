package trace

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

const defaultJSONLQueueSize = 1024

type JSONLRecorderOptions struct {
	QueueSize int
}

// JSONLRecorder 异步将 trace Event 写入 JSONL 文件。
//
// Record 必须保持非阻塞；文件 IO 只发生在后台 writer goroutine 中。
type JSONLRecorder struct {
	mu     sync.Mutex
	events chan Event
	done   chan struct{}
	closed bool

	file    *os.File
	writer  *bufio.Writer
	encoder *json.Encoder

	dropped atomic.Uint64
	err     error
}

// NewJSONLRecorder 创建一个基于文件的 JSONL recorder。
func NewJSONLRecorder(path string, options JSONLRecorderOptions) (*JSONLRecorder, error) {
	if options.QueueSize <= 0 {
		options.QueueSize = defaultJSONLQueueSize
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	recorder := &JSONLRecorder{
		events: make(chan Event, options.QueueSize),
		done:   make(chan struct{}),
		file:   file,
		writer: bufio.NewWriter(file),
	}
	recorder.encoder = json.NewEncoder(recorder.writer)

	go recorder.run()

	return recorder, nil
}

// Record 以非阻塞方式提交 event；队列满时直接丢弃。
func (r *JSONLRecorder) Record(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return
	}

	select {
	case r.events <- event:
	default:
		r.dropped.Add(1)
	}
}

// Close 停止接收新 event，并等待后台 writer 写完队列中剩余数据。
func (r *JSONLRecorder) Close(ctx context.Context) error {
	// MVP0 选择 drain 完成，暂不使用 ctx 提前中断关闭流程。
	_ = ctx

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return r.err
	}
	r.closed = true

	close(r.events)
	r.mu.Unlock()

	<-r.done

	if err := r.writer.Flush(); err != nil {
		r.setError(err)
	}
	if err := r.file.Close(); err != nil {
		r.setError(err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *JSONLRecorder) run() {
	defer close(r.done)

	for event := range r.events {
		if err := r.encoder.Encode(event); err != nil {
			r.setError(err)
			continue
		}
		if err := r.writer.Flush(); err != nil {
			r.setError(err)
		}
	}
}

func (r *JSONLRecorder) setError(err error) {
	if err == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.err == nil {
		r.err = err
	}
}

// Dropped 返回因队列满而丢弃的 event 数量。
func (r *JSONLRecorder) Dropped() uint64 {
	return r.dropped.Load()
}
