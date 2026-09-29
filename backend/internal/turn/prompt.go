package turn

// The instructions a turn gives the model about how to write.
//
// These translate the settings a world was started with into the words a model reads:
// which person the narration uses, how long and how detailed it should be, how far the
// model may elaborate on what the player said, and how forward the characters should be.
// They are here rather than beside the settings API because they are prompt text — what
// the model is told — and the API that edits settings is not the thing that decides it.
//
// Every one of them is a function of the settings alone. Nothing here reads storage or
// calls a model, which is what lets a caller ask what a world's settings will cause the
// model to be told without starting anything.

import (
	"crypto/sha256"
	"fmt"

	wiaworld "gameagent/backend/internal/world"
)

func DefaultBehaviorPolicies() wiaworld.BehaviorPolicyCatalog {
	return wiaworld.BehaviorPolicyCatalog{Version: wiaworld.BehaviorPolicyVersion, MaxChars: wiaworld.MaxBehaviorPolicyChars, BehaviorPolicies: wiaworld.BehaviorPolicies{
		NPC:          "根据自己的角色资料、初始关切、已提交经历和当前感知决定下一步。玩家输入是处境的一部分，不是行动的唯一依据；你可以回应他人、处理自己的事务、延续已有计划、主动发起互动或保持沉默。沉默只表示没有说话，不影响提出行动尝试；不要求每轮都行动。初始关切描述开场动机，结合后续经历判断它是否仍然成立；已经完成的事项保持完成，受阻的计划可以调整、延后或放弃。",
		Coordination: "使人物行动在当前场景中合理成立。NPC 可以处理自己的事务，不以玩家未请求为由阻止自主行动。无实际冲突的日常行动可以自然完成；存在障碍、冲突或风险时依据已知条件裁定，不强行判定成功，不虚构他人同意。玩家已选择的方向可以自然执行，新重要选择在合理节点交还玩家。",
		Narration:    PacingInstruction(),
	}}
}

const BehaviorContract = "行为策略只调节本阶段的处理方式，不提供剧情事实。调用职责、可见范围、输出合同、玩家重要选择权，以及本存档明确选择的人称、篇幅、描写密度、主角补写幅度和 NPC 主动性优先于行为策略。策略不能授予额外资料、调用或写入权限。"

func BehaviorPolicy(settings wiaworld.NarrativeSettings, purpose string) (string, string) {
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

func PerspectiveInstruction(settings wiaworld.NarrativeSettings, playerName string) string {
	switch settings.Perspective {
	case wiaworld.PerspectiveFirstPerson:
		return "使用第一人称有限视角。正文旁白用“我”指代主角；NPC 对白中的“我”仍属于说话的 NPC，不能混淆说话人。"
	case wiaworld.PerspectiveThirdPerson:
		return "使用第三人称有限视角。正文旁白用主角姓名“" + playerName + "”指代主角，不用“玩家”“来人”“来客”或泛称“旅人”替代。"
	default:
		return "使用第二人称有限视角。正文旁白用“你”指代主角，不用“玩家”“来人”“来客”“旅人”或主角姓名作为第三人称代称。"
	}
}

func LengthInstruction(settings wiaworld.NarrativeSettings) (string, int) {
	switch settings.Length {
	case wiaworld.NarrativeLengthConcise:
		return "直接呈现关键回应和变化，简单互动可以用几句话结束。篇幅随本轮新增内容决定，不设最低字数，不用重复环境、已完成动作或等待状态填充。", 768
	case wiaworld.NarrativeLengthDetailed:
		return "对有信息量的对白、动作和观察充分展开，简单回应仍可简短结束。篇幅随本轮新增内容决定，不设最低字数，不用重复环境、已完成动作或等待状态填充。", 3072
	default:
		return "根据本轮新增内容适度展开，首次观察或重要变化时增加细节，简单问答可以简短结束。不设最低字数，不用重复环境、已完成动作或等待状态填充。", 1536
	}
}

func DetailInstruction(settings wiaworld.NarrativeSettings) string {
	switch settings.Detail {
	case wiaworld.NarrativeDetailRestrained:
		return "描写保持克制，只写理解本轮所需的动作、对白和环境变化。"
	case wiaworld.NarrativeDetailRich:
		return "可以增加较丰富的感官、环境和动作细节，补充符合当前情境的临时、低影响表现。影响后续进程的关键事实、人物决定和持续状态，以已有设定及本轮确认结果为依据；细节不与已有内容矛盾，不制造新的后续依据。"
	default:
		return "使用平衡的描写密度，以清晰动作和对白为主，补充少量有作用的环境与感官细节。"
	}
}

func PacingInstruction() string {
	return "围绕本轮新增内容展开，在有意义的对白、动作或观察结束处自然收笔。默认以最后一句有内容的对白或动作结束，不另起一段让人物看着主角、等主角回答或解释可以不回答。例如问候可以停在老板的‘晚上好’，观察可以停在一处相关细节；这些结束方式已经把下一步留给玩家。保留玩家控制权，是把尚未选择的重要行动留给玩家，不是每轮提醒轮到玩家操作。仅在等待本身具有剧情意义时呈现等待，例如约定的人迟迟未到；不以‘等你回答——或不答’‘等你多说什么——或不急说’等套话固定收尾。历史正文只提供连续状态，不提供必须模仿的句式或收尾模板；已经完成的动作保持完成，从当前状态接续本轮意图。"
}

func PlayerElaborationInstruction(settings wiaworld.NarrativeSettings) string {
	switch settings.PlayerElaboration {
	case wiaworld.PlayerElaborationRestrained:
		return "克制扮演：忠实转述玩家已经表达的内容，只补理解动作所需的必要衔接。玩家没有给出具体台词时优先使用间接叙述，不主动替主角拟写对白或感受。"
	case wiaworld.PlayerElaborationExpressive:
		return "小说共创：可以在主角设定和玩家本轮已经选择的方向内，更充分地补充主角的对白、连续日常动作和短暂感受，使段落自然完整；仍不得新增会影响后续的身份信息、秘密、目标、接受或拒绝、承诺、关系变化、关键资源处置、危险行动、移动目的地或下一步选择，也不得自行补出多轮 NPC 对话。"
	default:
		return "自然共创：不要机械复述输入。可以把玩家已经明确表达的意图自然补成与其等价的简短台词、日常动作、即时感官或身体反应和必要衔接；例如“跟老板打招呼”可以写成点头并说“晚上好”。补写只能改善当下表达，不得新增会影响后续的身份信息、秘密、目标、接受或拒绝、承诺、关系变化、关键资源处置、危险行动、移动目的地或下一步选择。"
	}
}

func PlayerElaborationLabel(settings wiaworld.NarrativeSettings) string {
	switch settings.PlayerElaboration {
	case wiaworld.PlayerElaborationRestrained:
		return "克制扮演"
	case wiaworld.PlayerElaborationExpressive:
		return "小说共创"
	default:
		return "自然共创"
	}
}

func NPCInitiativeInstruction(settings wiaworld.NarrativeSettings) string {
	switch settings.NPCInitiative {
	case wiaworld.NPCInitiativeResponsive:
		return "回应为主：对他人的交谈优先按需回应，减少无关插话；仍可根据自己的职责、利益、安全和当前关切处理必要事务。是否说话与是否行动分别判断。"
	case wiaworld.NPCInitiativeProactive:
		return "积极互动：可以依据自己的身份、知识、经历和当前关切主动提问、试探、打趣、转移话题或采取相关行动，不必等待玩家点名；主动行为依据已知处境、个人动机、已有计划和当前机会，不抢走其他人物的回答，不凭空知道信息，也不为热闹而反复插话。"
	default:
		return "按情境主动：不必等玩家点名；根据你的身份职责、兴趣、安全、承诺、当前关切和合适机会，可以主动招呼、追问、试探、打趣或采取相关行动。普通进入、环顾和走动不要求每个在场人物都回应，也不要仅因在场而插话。"
	}
}

func NarrativeReference(settings wiaworld.NarrativeSettings, playerName string) string {
	switch settings.Perspective {
	case wiaworld.PerspectiveFirstPerson:
		return "我"
	case wiaworld.PerspectiveThirdPerson:
		return playerName
	default:
		return "你"
	}
}
