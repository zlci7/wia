package gateway

import "gameagent/backend/internal/idgen"

// newMessageID 创建 Runtime 本地的协议消息 ID。
//
// 这些 ID 用于 RuntimeMessage.message_id 和 AdapterMessage.correlation_id，
// 不代表游戏动作身份；游戏动作身份由 ActionRequest.action_id 表示。
func newMessageID(prefix string) string {
	return idgen.New(prefix)
}
