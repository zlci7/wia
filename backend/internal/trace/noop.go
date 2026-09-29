package trace

import "context"

type NoopRecorder struct{}

func (NoopRecorder) Record(event Event) {}

func (NoopRecorder) Close(ctx context.Context) error {
	return nil
}
