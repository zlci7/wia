package storyapp

import (
	"crypto/sha256"
	"fmt"

	wiaworld "gameagent/backend/internal/world"
)

func DefaultBehaviorPolicies() wiaworld.BehaviorPolicyCatalog {
	return wiaworld.BehaviorPolicyCatalog{Version: wiaworld.BehaviorPolicyVersion, MaxChars: wiaworld.MaxBehaviorPolicyChars, BehaviorPolicies: wiaworld.BehaviorPolicies{
		NPC:          "根据自己的角色资料、初始关切、已提交经历和当前感知决定下一步。玩家输入是处境的一部分，不是行动的唯一依据；你可以回应他人、处理自己的事务、延续已有计划、主动发起互动或保持沉默。沉默只表示没有说话，不影响提出行动尝试；不要求每轮都行动。初始关切描述开场动机，结合后续经历判断它是否仍然成立；已经完成的事项保持完成，受阻的计划可以调整、延后或放弃。",
		Coordination: "使人物行动在当前场景中合理成立。NPC 可以处理自己的事务，不以玩家未请求为由阻止自主行动。无实际冲突的日常行动可以自然完成；存在障碍、冲突或风险时依据已知条件裁定，不强行判定成功，不虚构他人同意。玩家已选择的方向可以自然执行，新重要选择在合理节点交还玩家。",
		Narration:    narrativePacingInstruction(),
	}}
}

const behaviorContract = "行为策略只调节本阶段的处理方式，不提供剧情事实。调用职责、可见范围、输出合同、玩家重要选择权，以及本存档明确选择的人称、篇幅、描写密度、主角补写幅度和 NPC 主动性优先于行为策略。策略不能授予额外资料、调用或写入权限。"

func behaviorPolicy(settings wiaworld.NarrativeSettings, purpose string) (string, string) {
	defaults := DefaultBehaviorPolicies()
	var custom, fallback string
	switch purpose {
	case "npc":
		custom, fallback = settings.Policies.NPC, defaults.NPC
	case "coordination":
		custom, fallback = settings.Policies.Coordination, defaults.Coordination
	case "narration":
		custom, fallback = settings.Policies.Narration, defaults.Narration
	default:
		return "", ""
	}
	if custom == "" {
		return fallback, wiaworld.BehaviorPolicyVersion + ":" + purpose
	}
	digest := sha256.Sum256([]byte(custom))
	return custom, fmt.Sprintf("custom:%x", digest[:8])
}

func migrateWritingPreference(settings wiaworld.NarrativeSettings) wiaworld.NarrativeSettings {
	if settings.CustomInstruction != "" && settings.Policies.Narration == "" {
		settings.Policies.Narration = narrativePacingInstruction() + "\n写作偏好：" + settings.CustomInstruction
	}
	settings.CustomInstruction = ""
	return settings
}
