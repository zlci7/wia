package storyapp

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// DiagnosticLog rotates only its own files; directories inherit normal user access.
type DiagnosticLog struct {
	mu    sync.Mutex
	file  *os.File
	path  string
	size  int64
	limit int64
}

func OpenDiagnosticLog(dataRoot string) (*DiagnosticLog, error) {
	dir := filepath.Join(dataRoot, "story-app", "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	w := &DiagnosticLog{path: filepath.Join(dir, "runtime.log"), limit: 5 << 20}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}
func (w *DiagnosticLog) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.file, w.size = f, info.Size()
	return nil
}
func (w *DiagnosticLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.limit {
		if err := w.file.Close(); err != nil {
			return 0, err
		}
		w.file = nil
		// Keep the current log and two bounded backups.
		if err := os.Remove(w.path + ".2"); err != nil && !os.IsNotExist(err) {
			_ = w.open()
			return 0, err
		}
		if err := os.Rename(w.path+".1", w.path+".2"); err != nil && !os.IsNotExist(err) {
			_ = w.open()
			return 0, err
		}
		if err := os.Rename(w.path, w.path+".1"); err != nil {
			_ = w.open()
			return 0, err
		}
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}
func (w *DiagnosticLog) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// File failures remain visible on stderr without dropping the console diagnostic.
func (w *DiagnosticLog) Logger(console io.Writer) *log.Logger {
	return log.New(&diagnosticTee{file: w, console: console}, "", log.LstdFlags|log.LUTC)
}

type diagnosticTee struct {
	file    io.Writer
	console io.Writer
}

func (w *diagnosticTee) Write(p []byte) (int, error) {
	n, err := w.console.Write(p)
	if _, fileErr := w.file.Write(p); fileErr != nil {
		_, _ = io.WriteString(w.console, "story diagnostic file unavailable\n")
	}
	return n, err
}
