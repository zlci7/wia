package gateway

import (
	"errors"
	"strings"
	"testing"
)

func TestLogSafeErrorReturnsSingleLineTruncatedMessage(t *testing.T) {
	message := logSafeError(errors.New("line one\nline two\r\n" + strings.Repeat("x", 300)))

	if strings.Contains(message, "\n") || strings.Contains(message, "\r") {
		t.Fatalf("message contains line break: %q", message)
	}
	if !strings.Contains(message, "line one line two") {
		t.Fatalf("message = %q, want compacted text", message)
	}
	if got := len([]rune(message)); got > 240 {
		t.Fatalf("message rune count = %d, want at most 240", got)
	}
}
