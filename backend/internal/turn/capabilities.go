package turn

import (
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

// Both coordination purposes use these contracts; each model call is independent.
func coordinationCapabilityContext(snapshot Snapshot, decisions map[string]NPCDecision, provided []wiaworld.Event) string {
	input := "\n作者世界规则：" + snapshot.Definition.Rules + "\n作者事实（按实际感知分发）：" + snapshot.Definition.Secret
	input += "\nNPC 协调提案（本人对白及听众范围、行动、沉默与关系提案，不含个人记忆）：\n" + coordinationDecisionContext(decisions, snapshot.Characters, provided)
	input += "\nspeech_source_id 或 action_source_id 有值时，完整原文见本请求中对应事件或场景来源的 content；引用的对白不是沉默，引用的行动仍是待裁定提案。依据事件阶段处理本人的各项决定，保持 speech_visibility 与实际听众范围。"
	if snapshot.Definition.Capabilities["spatial"] == 1 {
		input += "\n地点与有向连接(JSON，records各行按columns字段顺序)：" + locationContext(snapshot.Definition.Locations) + "\n当前位置(JSON)：" + wire.MarshalJSON(snapshot.Positions)
		input += "\n已定义背景实体(JSON)：" + wire.MarshalJSON(snapshot.Definition.BystanderRefs)
		input += "\nmovements 必须为数组；每项包含 entity_id、from、to、route、action_id。route 是 from 到 to 的有向地点 ID 序列。行动来源属于移动者本人且结果为 succeeded 或 partial；每位移动者有引用 action_id 且交给本人的 scene_updates。位置决定结束时的同场名单；失败或未执行的行动不产生移动。无移动返回 []。"
	} else {
		input += "\n地点资料（records各行按columns字段顺序，地点资料不表示已经到达）：" + locationContext(snapshot.Definition.Locations)
	}
	if snapshot.Definition.Capabilities["state"] == 1 || snapshot.Definition.Capabilities["relations"] == 1 || snapshot.Definition.Capabilities["items"] == 1 {
		input += "\n权威人物状态、关系与物品(JSON，records各行按columns字段顺序，states值类型由state_definitions.type确定)：" + HostMechanicsContext(snapshot)
		input += "\n关系实例只列非默认或已变化的值，其他已定义人物组合采用关系定义的 default。你根据世界资料、本人决定和结果理解变化，不把未执行的计划当事实；能力数组无变化时返回 []。"
	}
	if snapshot.Definition.Capabilities["state"] == 1 {
		input += stateCoordinationContract()
	}
	if snapshot.Definition.Capabilities["relations"] == 1 {
		input += relationshipCoordinationContract()
	}
	if snapshot.Definition.Capabilities["items"] == 1 {
		input += itemCoordinationContract()
	}
	return input
}

func coordinationCapabilityFields(snapshot Snapshot) []string {
	fields := []string{}
	for _, module := range []struct{ capability, field string }{{"spatial", "movements"}, {"state", "state_effects"}, {"relations", "relationship_effects"}, {"items", "item_transfers"}} {
		if snapshot.Definition.Capabilities[module.capability] == 1 {
			fields = append(fields, module.field)
		}
	}
	return fields
}

func validateMechanicCapabilities(snapshot Snapshot, effects mechanicEffects) error {
	for _, module := range []struct {
		capability, field string
		count             int
	}{{"spatial", "movements", len(effects.Movements)}, {"state", "state_effects", len(effects.StateEffects)}, {"relations", "relationship_effects", len(effects.RelationshipEffects)}, {"items", "item_transfers", len(effects.ItemTransfers)}} {
		if snapshot.Definition.Capabilities[module.capability] != 1 && module.count > 0 {
			return coordinationInvalid(module.field+"_unsupported", module.field, "empty-or-omitted")
		}
	}
	return nil
}
