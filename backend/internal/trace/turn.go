package trace

import (
	"gameagent/backend/internal/idgen"
	"sync"
	"time"
)

// Emit      发普通阶段事件
// Complete  发 turn_completed
// Fail      发 turn_failed
type TurnTracer interface {
	Emit(name EventName, data EventData)
	Complete(data EventData)
	Fail(stage string, reason string, err error, data EventData)
}

// mu       保护 seq / closed
// recorder 真正消费 Event 的对象
// ctx      game_id/world_id/session_id/event_id/entity_id 等固定上下文
// turnID   本轮 turn ID
// traceID  Phase2 先等于 turnID
// start    计算 elapsed_ms 的起点
// seq      本轮事件序号
// closed   terminal 发出后关闭
type turnTracer struct {
	mu       sync.Mutex
	recorder Recorder

	ctx     TurnContext
	turnID  string
	traceID string

	start  time.Time
	seq    uint32
	closed bool
}

// NewTurnTracer 创建一条新的 Turn trace。
// 默认由 tracer 自己生成 turnID，适合不需要和其他子系统共享 ID 的场景。
func NewTurnTracer(recorder Recorder, ctx TurnContext) TurnTracer {
	return NewTurnTracerWithID(recorder, ctx, newTurnID())
}

// NewTurnTracerWithID 创建使用调用方 turnID 的 Turn trace。
// Phase4 让 Trace 和 Memory 共享同一个上游 turnID，避免 Memory 反向依赖 Trace。
func NewTurnTracerWithID(recorder Recorder, ctx TurnContext, turnID string) TurnTracer {
	if turnID == "" {
		turnID = newTurnID()
	}
	return &turnTracer{
		recorder: recorder,
		ctx:      ctx,
		turnID:   turnID,
		traceID:  turnID,
		start:    time.Now(),
	}
}

// newTurnID 生成默认 Turn ID。
func newTurnID() string {
	return idgen.New("turn")
}

// Emit 记录普通阶段事件。
// terminal 事件发出后，后续 Emit 会被忽略。
func (t *turnTracer) Emit(name EventName, data EventData) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}

	t.recordLocked(name, "", "", nil, data)
}

// Complete 记录 turn_completed，并关闭本轮 TurnTracer。
// 关闭后再调用 Complete / Fail / Emit 都不会产生新事件。
func (t *turnTracer) Complete(data EventData) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}

	t.recordLocked(EventTurnCompleted, "", "", nil, data)
	t.closed = true
}

// Fail 记录 turn_failed，并关闭本轮 TurnTracer。
// reason 用于表达稳定失败原因，err 只在存在底层技术错误时记录。
func (t *turnTracer) Fail(stage string, reason string, err error, data EventData) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}

	t.recordLocked(EventTurnFailed, stage, reason, err, data)
	t.closed = true
}

// recorder.Record 必须是非阻塞 Observer；recordLocked 会在持有 turn 锁时调用它。
// 如果未来实现同步 IO recorder，应先改为锁外 Record。
func (t *turnTracer) recordLocked(
	name EventName,
	stage string,
	reason string,
	err error,
	data EventData,
) {
	t.seq++

	now := time.Now()
	event := Event{
		SchemaVersion: 1,
		TraceID:       t.traceID,
		TurnID:        t.turnID,
		Seq:           t.seq,
		Event:         name,
		Time:          now,
		ElapsedMS:     now.Sub(t.start).Milliseconds(),

		GameID:    t.ctx.GameID,
		WorldID:   t.ctx.WorldID,
		SessionID: t.ctx.SessionID,
		EventID:   t.ctx.EventID,
		EventType: t.ctx.EventType,
		EntityID:  t.ctx.EntityID,

		ActionID: data.ActionID,
		Tool:     data.Tool,

		Stage:  stage,
		Reason: reason,
		Fields: data.Fields,
	}

	if err != nil {
		event.ErrorMessage = err.Error()
	}

	if t.recorder != nil {
		t.recorder.Record(event)
	}
}
