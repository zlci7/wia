package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gameagent/backend/internal/agent"
	protocolv1alpha2 "gameagent/protocol/gen/go/gameagent/protocol/v1alpha2"
	"google.golang.org/grpc/metadata"
)

func TestStreamEnvironmentSubmitActionRejectsNilRequest(t *testing.T) {
	env := newStreamEnvironment(nil)

	_, err := env.SubmitAction(context.Background(), nil)

	if err == nil {
		t.Fatal("expected error for nil action request")
	}
	if !strings.Contains(err.Error(), "action request is nil") {
		t.Fatalf("expected nil action request error, got %q", err.Error())
	}
}

func TestStreamEnvironmentSubmitActionRejectsEmptyActionID(t *testing.T) {
	env := newStreamEnvironment(nil)

	_, err := env.SubmitAction(context.Background(), &protocolv1alpha2.ActionRequest{})

	if err == nil {
		t.Fatal("expected error for empty action id")
	}
	if !strings.Contains(err.Error(), "action id is empty") {
		t.Fatalf("expected empty action id error, got %q", err.Error())
	}
}

func TestStreamEnvironmentSubmitActionDoesNotSendExpiredAction(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 2)}
	env := newStreamEnvironment(stream)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := env.SubmitAction(ctx, &protocolv1alpha2.ActionRequest{
		ActionId:   "act_expired",
		EntityId:   "npc:Linus",
		Capability: "speak",
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}

	select {
	case msg := <-stream.sent:
		t.Fatalf("expected no runtime message for expired action, got %T", msg.Payload)
	default:
	}

	env.pendingMu.Lock()
	defer env.pendingMu.Unlock()
	if len(env.pendingActions) != 0 {
		t.Fatalf("expected no pending actions for expired action, got %d", len(env.pendingActions))
	}
}

func TestStreamEnvironmentSubmitActionSendsCancelOnContextDone(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 2)}
	env := newStreamEnvironment(stream)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := env.SubmitAction(ctx, &protocolv1alpha2.ActionRequest{
			ActionId:   "act_1",
			EntityId:   "npc:Linus",
			Capability: "speak",
		})
		errCh <- err
	}()

	actionMsg := stream.recvSent(t)
	if actionMsg.GetAction() == nil {
		t.Fatalf("expected ActionRequest, got %T", actionMsg.Payload)
	}

	cancelMsg := stream.recvSent(t)
	cancelReq := cancelMsg.GetCancelAction()
	if cancelReq == nil {
		t.Fatalf("expected CancelActionRequest, got %T", cancelMsg.Payload)
	}
	if cancelReq.ActionId != "act_1" {
		t.Fatalf("expected cancel action_id act_1, got %q", cancelReq.ActionId)
	}
	if cancelReq.Reason != "action_timeout" {
		t.Fatalf("expected cancel reason action_timeout, got %q", cancelReq.Reason)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SubmitAction did not return after context deadline")
	}
}

func TestStreamEnvironmentLateActionResultAfterTimeoutIsIgnored(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 2)}
	env := newStreamEnvironment(stream)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := env.SubmitAction(ctx, &protocolv1alpha2.ActionRequest{
			ActionId:   "act_late",
			EntityId:   "npc:Linus",
			Capability: "speak",
		})
		errCh <- err
	}()

	actionMsg := stream.recvSent(t)
	if actionMsg.GetAction() == nil {
		t.Fatalf("expected ActionRequest, got %T", actionMsg.Payload)
	}
	cancelMsg := stream.recvSent(t)
	if cancelMsg.GetCancelAction() == nil {
		t.Fatalf("expected CancelActionRequest, got %T", cancelMsg.Payload)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SubmitAction did not return after context deadline")
	}

	env.resolveActionResult("act_late", &protocolv1alpha2.ActionResult{
		ActionId: "act_late",
		Status:   protocolv1alpha2.ActionStatus_ACTION_STATUS_CANCELLED,
	})

	env.pendingMu.Lock()
	defer env.pendingMu.Unlock()
	if len(env.pendingActions) != 0 {
		t.Fatalf("expected no pending actions after timeout, got %d", len(env.pendingActions))
	}
}

func TestStreamEnvironmentFailObservationUnblocksObserve(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 1)}
	env := newStreamEnvironment(stream)
	wantErr := fmt.Errorf("adapter observe failed")

	resultCh := make(chan error, 1)
	go func() {
		_, err := env.Observe(context.Background(), "world:test", "npc:Linus")
		resultCh <- err
	}()

	sent := stream.recvSent(t)
	observe := sent.GetObserve()
	if observe == nil {
		t.Fatalf("expected ObserveRequest, got %T", sent.Payload)
	}
	if observe.WorldId != "world:test" {
		t.Fatalf("observe world id = %q, want %q", observe.WorldId, "world:test")
	}
	if observe.EntityId != "npc:Linus" {
		t.Fatalf("observe entity id = %q, want %q", observe.EntityId, "npc:Linus")
	}

	env.failObservation(sent.MessageId, wantErr)

	select {
	case err := <-resultCh:
		if err == nil {
			t.Fatal("expected observe error")
		}
		if !strings.Contains(err.Error(), wantErr.Error()) {
			t.Fatalf("expected %q, got %q", wantErr.Error(), err.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("observe was not unblocked")
	}
}

func TestAdapterErrorExposesFailureReason(t *testing.T) {
	err := adapterError{
		code:    "world_mismatch",
		message: "current world changed",
	}

	if err.FailureReason() != "world_mismatch" {
		t.Fatalf("failure reason = %q, want world_mismatch", err.FailureReason())
	}
	if !strings.Contains(err.Error(), "world_mismatch") {
		t.Fatalf("error string = %q, want code included", err.Error())
	}
}

func TestStreamEnvironmentSendTurnCompletion(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 1)}
	env := newStreamEnvironment(stream)

	err := env.SendTurnCompletion(context.Background(), &protocolv1alpha2.TurnCompletion{
		TurnId:   "turn_1",
		EventId:  "event_1",
		WorldId:  "world:test",
		EntityId: "npc:Linus",
		Status:   protocolv1alpha2.TurnCompletionStatus_TURN_COMPLETION_STATUS_COMPLETED,
	})
	if err != nil {
		t.Fatalf("SendTurnCompletion returned error: %v", err)
	}

	msg := stream.recvSent(t)
	completion := msg.GetTurnCompletion()
	if completion == nil {
		t.Fatalf("expected TurnCompletion, got %T", msg.Payload)
	}
	if completion.TurnId != "turn_1" || completion.EventId != "event_1" {
		t.Fatalf("completion = %+v, want turn_1/event_1", completion)
	}
}

func TestStreamEnvironmentStartActionReceivesAcceptedStatus(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 2)}
	env := newStreamEnvironment(stream)

	startCh := make(chan actionStartResult, 1)
	go func() {
		start, err := env.StartAction(context.Background(), &protocolv1alpha2.ActionRequest{
			ActionId:   "act_async",
			EntityId:   "npc:Linus",
			Capability: "move_to",
		})
		startCh <- actionStartResult{start: start, err: err}
	}()

	actionMsg := stream.recvSent(t)
	if action := actionMsg.GetAction(); action == nil || action.ActionId != "act_async" {
		t.Fatalf("expected async ActionRequest, got %+v", actionMsg.Payload)
	}
	env.resolveActionStatusUpdate("act_async", &protocolv1alpha2.ActionStatusUpdate{
		ActionId: "act_async",
		Status:   protocolv1alpha2.ActionStatus_ACTION_STATUS_ACCEPTED,
	})

	started := recvActionStartResult(t, startCh)
	if started.err != nil {
		t.Fatalf("StartAction returned error: %v", started.err)
	}
	if update := started.start.Update; update == nil || update.Status != protocolv1alpha2.ActionStatus_ACTION_STATUS_ACCEPTED {
		t.Fatalf("start update = %+v, want ACCEPTED", update)
	}
	if started.start.Result != nil {
		t.Fatalf("unexpected fast terminal result: %+v", started.start.Result)
	}

	env.resolveActionResult("act_async", &protocolv1alpha2.ActionResult{
		ActionId: "act_async",
		Status:   protocolv1alpha2.ActionStatus_ACTION_STATUS_SUCCEEDED,
	})
	result, err := env.WaitActionResult(context.Background(), "act_async")
	if err != nil {
		t.Fatalf("WaitActionResult returned error: %v", err)
	}
	if result.GetStatus() != protocolv1alpha2.ActionStatus_ACTION_STATUS_SUCCEEDED {
		t.Fatalf("wait result status = %s, want SUCCEEDED", result.GetStatus())
	}
}

func TestStreamEnvironmentStartActionReturnsFastTerminalResult(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 1)}
	env := newStreamEnvironment(stream)

	startCh := make(chan actionStartResult, 1)
	go func() {
		start, err := env.StartAction(context.Background(), &protocolv1alpha2.ActionRequest{
			ActionId:   "act_fast",
			EntityId:   "npc:Linus",
			Capability: "move_to",
		})
		startCh <- actionStartResult{start: start, err: err}
	}()

	_ = stream.recvSent(t)
	env.resolveActionResult("act_fast", &protocolv1alpha2.ActionResult{
		ActionId: "act_fast",
		Status:   protocolv1alpha2.ActionStatus_ACTION_STATUS_SUCCEEDED,
	})

	started := recvActionStartResult(t, startCh)
	if started.err != nil {
		t.Fatalf("StartAction returned error: %v", started.err)
	}
	if started.start.Result == nil {
		t.Fatal("expected fast terminal result")
	}
	if started.start.Update != nil {
		t.Fatalf("unexpected status update: %+v", started.start.Update)
	}
	assertNoPendingActions(t, env)
}

func TestStreamEnvironmentWaitActionResultReceivesTerminalResult(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 1)}
	env := newStreamEnvironment(stream)

	startCh := make(chan actionStartResult, 1)
	go func() {
		start, err := env.StartAction(context.Background(), &protocolv1alpha2.ActionRequest{
			ActionId:   "act_wait",
			EntityId:   "npc:Linus",
			Capability: "move_to",
		})
		startCh <- actionStartResult{start: start, err: err}
	}()
	_ = stream.recvSent(t)
	env.resolveActionStatusUpdate("act_wait", &protocolv1alpha2.ActionStatusUpdate{
		ActionId: "act_wait",
		Status:   protocolv1alpha2.ActionStatus_ACTION_STATUS_RUNNING,
	})
	started := recvActionStartResult(t, startCh)
	if started.err != nil {
		t.Fatalf("StartAction returned error: %v", started.err)
	}

	waitCh := make(chan actionResult, 1)
	go func() {
		result, err := env.WaitActionResult(context.Background(), "act_wait")
		waitCh <- actionResult{result: result, err: err}
	}()
	env.resolveActionResult("act_wait", &protocolv1alpha2.ActionResult{
		ActionId: "act_wait",
		Status:   protocolv1alpha2.ActionStatus_ACTION_STATUS_SUCCEEDED,
	})

	result := recvActionResult(t, waitCh)
	if result.err != nil {
		t.Fatalf("WaitActionResult returned error: %v", result.err)
	}
	if result.result.GetActionId() != "act_wait" {
		t.Fatalf("wait action_id = %q, want act_wait", result.result.GetActionId())
	}
	assertNoPendingActions(t, env)
}

func TestStreamEnvironmentStartActionTimeoutSendsCancelAction(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 2)}
	env := newStreamEnvironment(stream)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	startCh := make(chan actionStartResult, 1)
	go func() {
		start, err := env.StartAction(ctx, &protocolv1alpha2.ActionRequest{
			ActionId:   "act_start_timeout",
			EntityId:   "npc:Linus",
			Capability: "move_to",
		})
		startCh <- actionStartResult{start: start, err: err}
	}()

	_ = stream.recvSent(t)
	cancelMsg := stream.recvSent(t)
	cancelReq := cancelMsg.GetCancelAction()
	if cancelReq == nil {
		t.Fatalf("expected CancelActionRequest, got %T", cancelMsg.Payload)
	}
	if cancelReq.ActionId != "act_start_timeout" || cancelReq.Reason != "action_start_timeout" {
		t.Fatalf("cancel = %+v, want act_start_timeout/action_start_timeout", cancelReq)
	}

	started := recvActionStartResult(t, startCh)
	if !errors.Is(started.err, context.DeadlineExceeded) {
		t.Fatalf("StartAction error = %v, want deadline exceeded", started.err)
	}
	assertNoPendingActions(t, env)
}

func TestStreamEnvironmentWaitActionResultTimeoutSendsCancelAction(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 2)}
	env := newStreamEnvironment(stream)

	startCh := make(chan actionStartResult, 1)
	go func() {
		start, err := env.StartAction(context.Background(), &protocolv1alpha2.ActionRequest{
			ActionId:   "act_wait_timeout",
			EntityId:   "npc:Linus",
			Capability: "move_to",
		})
		startCh <- actionStartResult{start: start, err: err}
	}()
	_ = stream.recvSent(t)
	env.resolveActionStatusUpdate("act_wait_timeout", &protocolv1alpha2.ActionStatusUpdate{
		ActionId: "act_wait_timeout",
		Status:   protocolv1alpha2.ActionStatus_ACTION_STATUS_ACCEPTED,
	})
	if started := recvActionStartResult(t, startCh); started.err != nil {
		t.Fatalf("StartAction returned error: %v", started.err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := env.WaitActionResult(ctx, "act_wait_timeout")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitActionResult error = %v, want deadline exceeded", err)
	}
	cancelMsg := stream.recvSent(t)
	cancelReq := cancelMsg.GetCancelAction()
	if cancelReq == nil {
		t.Fatalf("expected CancelActionRequest, got %T", cancelMsg.Payload)
	}
	if cancelReq.ActionId != "act_wait_timeout" || cancelReq.Reason != "async_action_timeout" {
		t.Fatalf("cancel = %+v, want act_wait_timeout/async_action_timeout", cancelReq)
	}
	assertNoPendingActions(t, env)
}

func TestStreamEnvironmentLateAsyncResultAfterTimeoutIsIgnored(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 2)}
	env := newStreamEnvironment(stream)

	startCh := make(chan actionStartResult, 1)
	go func() {
		start, err := env.StartAction(context.Background(), &protocolv1alpha2.ActionRequest{
			ActionId:   "act_late_async",
			EntityId:   "npc:Linus",
			Capability: "move_to",
		})
		startCh <- actionStartResult{start: start, err: err}
	}()
	_ = stream.recvSent(t)
	env.resolveActionStatusUpdate("act_late_async", &protocolv1alpha2.ActionStatusUpdate{
		ActionId: "act_late_async",
		Status:   protocolv1alpha2.ActionStatus_ACTION_STATUS_RUNNING,
	})
	if started := recvActionStartResult(t, startCh); started.err != nil {
		t.Fatalf("StartAction returned error: %v", started.err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := env.WaitActionResult(ctx, "act_late_async")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitActionResult error = %v, want deadline exceeded", err)
	}
	_ = stream.recvSent(t)
	env.resolveActionResult("act_late_async", &protocolv1alpha2.ActionResult{
		ActionId: "act_late_async",
		Status:   protocolv1alpha2.ActionStatus_ACTION_STATUS_SUCCEEDED,
	})
	assertNoPendingActions(t, env)
}

func TestStreamEnvironmentFailAllPendingUnblocksAsyncWaiters(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 1)}
	env := newStreamEnvironment(stream)
	wantErr := fmt.Errorf("adapter disconnected")

	startCh := make(chan actionStartResult, 1)
	go func() {
		start, err := env.StartAction(context.Background(), &protocolv1alpha2.ActionRequest{
			ActionId:   "act_disconnect_start",
			EntityId:   "npc:Linus",
			Capability: "move_to",
		})
		startCh <- actionStartResult{start: start, err: err}
	}()
	_ = stream.recvSent(t)
	env.failAllPending(wantErr)
	if started := recvActionStartResult(t, startCh); !errors.Is(started.err, wantErr) {
		t.Fatalf("StartAction error = %v, want %v", started.err, wantErr)
	}

	waitPending := newPendingAction()
	env.setPendingAction("act_disconnect_wait", waitPending)
	env.failAllPending(wantErr)
	if result := recvActionResult(t, waitPending.results); !errors.Is(result.err, wantErr) {
		t.Fatalf("WaitActionResult error = %v, want %v", result.err, wantErr)
	}
}

func TestStreamEnvironmentRejectsObservationScopeMismatch(t *testing.T) {
	stream := &captureStream{sent: make(chan *protocolv1alpha2.RuntimeMessage, 1)}
	env := newStreamEnvironment(stream)

	resultCh := make(chan error, 1)
	go func() {
		_, err := env.Observe(context.Background(), "world:test", "npc:Linus")
		resultCh <- err
	}()

	sent := stream.recvSent(t)
	env.resolveObservation(sent.MessageId, &protocolv1alpha2.Observation{
		EntityId: "npc:Linus",
		WorldId:  "world:other",
	})

	select {
	case err := <-resultCh:
		if err == nil {
			t.Fatal("expected observation scope mismatch error")
		}
		if !strings.Contains(err.Error(), "observation_scope_mismatch") {
			t.Fatalf("expected observation_scope_mismatch, got %q", err.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("observe was not unblocked")
	}
}

type actionStartResult struct {
	start agent.ActionStart
	err   error
}

type captureStream struct {
	sent chan *protocolv1alpha2.RuntimeMessage
}

func (s *captureStream) Send(msg *protocolv1alpha2.RuntimeMessage) error {
	if s.sent == nil {
		s.sent = make(chan *protocolv1alpha2.RuntimeMessage, 1)
	}
	s.sent <- msg
	return nil
}

func (s *captureStream) Recv() (*protocolv1alpha2.AdapterMessage, error) {
	return nil, fmt.Errorf("recv not implemented")
}

func (s *captureStream) recvSent(t *testing.T) *protocolv1alpha2.RuntimeMessage {
	t.Helper()

	select {
	case msg := <-s.sent:
		return msg
	case <-time.After(time.Second):
		t.Fatal("runtime message was not sent")
		return nil
	}
}

func recvActionStartResult(t *testing.T, ch <-chan actionStartResult) actionStartResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(time.Second):
		t.Fatal("action start did not return")
		return actionStartResult{}
	}
}

func recvActionResult(t *testing.T, ch <-chan actionResult) actionResult {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(time.Second):
		t.Fatal("action result wait did not return")
		return actionResult{}
	}
}

func assertNoPendingActions(t *testing.T, env *streamEnvironment) {
	t.Helper()
	env.pendingMu.Lock()
	defer env.pendingMu.Unlock()
	if len(env.pendingActions) != 0 {
		t.Fatalf("expected no pending actions, got %d", len(env.pendingActions))
	}
}

func (s *captureStream) SetHeader(metadata.MD) error {
	return nil
}

func (s *captureStream) SendHeader(metadata.MD) error {
	return nil
}

func (s *captureStream) SetTrailer(metadata.MD) {}

func (s *captureStream) Context() context.Context {
	return context.Background()
}

func (s *captureStream) SendMsg(any) error {
	return nil
}

func (s *captureStream) RecvMsg(any) error {
	return nil
}
