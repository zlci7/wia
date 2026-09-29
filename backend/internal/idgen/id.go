package idgen

import (
	"fmt"
	"sync/atomic"
	"time"
)

var counter atomic.Uint64

// New 创建带进程内递增后缀的 Runtime 本地 ID。
func New(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), counter.Add(1))
}
