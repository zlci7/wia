package tool

import (
	"fmt"
	"gameagent/backend/internal/idgen"
	"gameagent/backend/internal/model"
	protocolv1alpha2 "gameagent/protocol/gen/go/gameagent/protocol/v1alpha2"

	"google.golang.org/protobuf/types/known/structpb"
)

// BuildActionRequest 把模型 ToolCall 转成 Adapter 能执行的 ActionRequest。
//
// 这里不直接访问 gRPC stream，也不接触具体游戏 API；它只维护
// Runtime 内部 tool 语义到 protocol action 语义的转换边界。
type ActionRequestInput struct {
	WorldID       string
	EntityID      string
	SourceEventID string
	SourceTurnID  string
	ToolCall      model.ToolCall
}

func BuildActionRequest(input ActionRequestInput) (*protocolv1alpha2.ActionRequest, error) {
	if input.WorldID == "" {
		return nil, fmt.Errorf("world is empty")
	}

	if input.EntityID == "" {
		return nil, fmt.Errorf("entity is empty")
	}

	if input.ToolCall.Arguments == nil {
		return nil, fmt.Errorf("tool arguments are missing")
	}

	arguments, err := structpb.NewStruct(input.ToolCall.Arguments)
	if err != nil {
		return nil, fmt.Errorf("convert tool arguments: %w", err)
	}

	return &protocolv1alpha2.ActionRequest{
		ActionId:      newActionID(),
		EntityId:      input.EntityID,
		Capability:    input.ToolCall.Name,
		Arguments:     arguments,
		WorldId:       input.WorldID,
		SourceEventId: input.SourceEventID,
		SourceTurnId:  input.SourceTurnID,
	}, nil
}

// newActionID 创建游戏动作 ID。
//
// action_id 标识一次 Adapter 需要执行的业务动作，不等同于 RuntimeMessage.message_id。
func newActionID() string {
	return idgen.New("act")
}
