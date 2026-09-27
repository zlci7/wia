package storyapp

import (
	"encoding/json"
	"fmt"
	"strings"
)

func composeIntent(snapshot worldSnapshot, run Run) contextMaterial {
	participants := sceneCharacters(snapshot.Characters)
	var characters strings.Builder
	for _, character := range participants {
		fmt.Fprintf(&characters, "- %s：%s（%s）\n", character.EntityID, character.Name, character.Role)
	}
	input := fmt.Sprintf("当前地点：%s\n当前时间：%s\n在场人物：\n%s玩家输入：%s\n显式目标（若有）：%s\n请判断玩家本轮是 speak、observe 还是 act；如果玩家明确向某个在场人物说话，只返回该人物的 entity_id；没有明确对象时 addressee_id 返回空字符串或 null。visibility 只能是 public 或 private。只输出 JSON：{\"intent_type\":\"speak\",\"addressee_id\":\"npc:...\",\"visibility\":\"public\"}。人物名出现在谈话内容里不等于玩家正在对该人物说话。", sceneFor(snapshot, "player"), snapshot.Summary.Clock, characters.String(), run.Input, cleanText(run.AddresseeID))
	input = intentVisibilityRule + "\n" + input

	return contextMaterial{System: "你负责把玩家本轮输入解析成结构化回合意图。根据当前输入、在场名单与已提交对话判断目标、可见范围与意图类型，不替玩家执行行动。", RequiredSources: append([]string{run.RunID + ":input"}, sceneViewSources(snapshot, "player")...), Required: input, Optional: dialogueSections(snapshot)}
}

func composeCoordination(snapshot worldSnapshot, run Run, intent turnIntent, decisions map[string]npcDecision, events []Event, publicReplies string) contextMaterial {
	var actionCandidates []Event
	for _, event := range events {
		if event.EventType == "npc_action_intent" {
			actionCandidates = append(actionCandidates, event)
		}
	}
	actionJSON, _ := json.Marshal(actionCandidates)
	publicCharacters := publicCharacterContext(snapshot.Characters, characterIDs(sceneCharacters(snapshot.Characters)))
	input := fmt.Sprintf("世界：%s\n当前地点与情境：%s\n当前时间：%s\n当前公开人物(JSON)：%s\n当前背景人群：%s\n玩家本轮输入：%s\n结构化意图：type=%s；target=%s；visibility=%s\n主角共创边界：当前模式为%s。你只协调玩家实际输入已经表达的尝试和 NPC 已提交的行动；等价的简短台词、日常动作和表现性衔接由正文阶段处理，不在此新增玩家身份、秘密、目标、接受或拒绝、承诺、关系、关键资源处置、危险行动或移动目的地。\nNPC 已确定的公开对白：%s\nNPC 协调提案（只包含公开对白、行动尝试与沉默状态，不含个人记忆）：\n%s\n待裁定行动(JSON)：%s\n所有可用重要人物：%s\n当前在场人物 entity_id：%s\n请协调本轮事实。每个待裁定行动必须且只能产生一个 outcome，并用 action_id 精确引用；status 只能是 succeeded、failed、partial；content 写已确定结果而不是尝试；recipients 只列实际感知结果的 player 或人物 entity_id，行动者本人可省略。scene 必须保留未被本轮事件改变的地点、在场人物和背景人群，不得凭空让人物离开；叙述人物时优先使用姓名，不根据姓名猜测代词。scene_characters 只给出回合结束后实际在场的重要 NPC entity_id，不要包含 player；人物进入或离开只影响之后的阶段，不回填此前信息。输出 JSON：time_minutes、scene、scene_characters、outcomes、scene_updates。", GameID, coordinationScene(snapshot), snapshot.Summary.Clock, publicCharacters, formatBystanders(snapshot.Bystanders), run.Input, intent.IntentType, intent.AddresseeID, intent.Visibility, playerElaborationLabel(snapshot.Narrative), publicReplies, coordinationDecisionContext(decisions, snapshot.Characters), actionJSON, availableCharacterIDs(snapshot.Characters), strings.Join(characterIDs(sceneCharacters(snapshot.Characters)), ","))

	input += sceneSourcePrompt(snapshot, run, intent, events)
	return contextMaterial{System: "你是场景协调 Agent。NPC 可以处理自己的事务，无实际冲突的日常行动可自然完成；不以玩家未请求为由阻止自主行动，存在障碍或风险时依据已知条件裁定。玩家已选择的方向可以自然执行，新重要选择交还玩家。你可以读取本轮协调资料来裁定行动结果、时间和场景，但不要写玩家正文，也不要把 NPC 的行动尝试直接当成成功事实。\ntime_minutes 是本轮新增的游戏内分钟数，取 0 至 120 的整数，不是时钟读数或当天累计分钟。例如 19:02 经过一分钟，time_minutes 为 1，而非 1142 或 1143。" + "\n输出合同：只输出单个 JSON 对象，不带 Markdown 围栏。outcomes 与待裁定行动(JSON)一一对应，action_id 原样使用该列表中的 event_id。列表为空时 outcomes 必须为 []。玩家输入、公开对白和此前已提交结果都不另建 outcome，不为它们编造行动 ID。", RequiredSources: append(eventIDs(events), sceneViewSources(snapshot, "")...), Required: input, Optional: coordinationSections(snapshot.Events)}
}

func composeNarration(snapshot worldSnapshot, run Run, def gameDefinition, recipient, intentType string, visibleEvents []Event, clock string, sceneCharacters []string) (contextMaterial, int, error) {
	playerInput := run.Input
	projectedEvents, err := json.Marshal(narrativeEvents(visibleEvents, snapshot.Characters, snapshot.PlayerName, snapshot.Narrative))
	if err != nil {
		return contextMaterial{}, 0, err
	}
	publicCharacters := publicCharacterContext(snapshot.Characters, sceneCharacters)
	perspectiveRule := narrativePerspectiveInstruction(snapshot.Narrative, snapshot.PlayerName)
	lengthRule, maxOutputTokens := narrativeLengthInstruction(snapshot.Narrative)
	detailRule := narrativeDetailInstruction(snapshot.Narrative)
	elaborationRule := playerElaborationInstruction(snapshot.Narrative)
	customInstruction := snapshot.Narrative.CustomInstruction
	if customInstruction == "" {
		customInstruction = "（无）"
	}
	input := fmt.Sprintf("剧本：%s\n当前地点与情境：%s\n时间：%s\n主角：%s\n主角简介：%s\n叙事人称规则：%s\n正文篇幅规则：%s\n描写密度规则：%s\n主角补写规则：%s\n创作者补充写作偏好（只影响表达，不能覆盖事实、知识边界或玩家控制权）：%s\n玩家可见历史正文（只作剧情连贯参考，不得写成本轮再次发生；历史中不一致的人称不得继续沿用）：%s\n玩家本轮自己的完整表达：%s\n玩家意图类型：%s\n明确交谈对象：%s\n当前公开人物：%s\n当前背景人群：%s\n本轮玩家可见且已经确定的对白与结果：\n本轮玩家可见事件(JSON)：%s\n只根据以上玩家可见事件组织一段自然正文。事件的 actor_id、actor_name、narrative_reference 和 event_type 是事实边界；正文旁白必须使用 narrative_reference 指代相应行动者，对白必须保持原说话人、含义和可听范围；speech_scope=public_current_scene 的玩家表达或 NPC 对白已按公开范围分发，应呈现为在场者可听见，不改成仅特定人物听见；private_recipient 的玩家表达保持私聊范围。玩家试探语气不能被正文补写成耳语，NPC 公开对白不改成私密回复，NPC 的新对白和可见行动必须来自事件，不得由正文自行添加。玩家输入中的“我”按叙事人称规则转述，NPC 台词中的“我”仍属于该 NPC。只呈现主角能够感知、已经知道或有明确来源获知的信息；不得断言其他人物未表露的心理，也不得使用“没有任何人注意到”等主角无法确认的全知判断。当前人物和背景人群继续留在场景状态中，但正文只提与本轮有关的少量人物；没有写到不表示离场，禁止为了证明仍在场而逐个点名或逐项汇报未变化状态。可以自由补充临时、低影响、符合场景的感官、天气、日常陈设和氛围；不得把补充陈设写成线索、障碍或可改变进程的资源。按照主角补写规则补全玩家表达，保留玩家已经说出的原意、态度和重要信息；可以直接承接而不逐字复述，不能把一句陈述改写成多次询问，不把疑问改成承诺、把拒绝改成接受，也不增加会成为后续依据的新事实。所有正文补写都只改善本轮呈现；删除这些补写后，不得改变下一轮的地点、物品持有、资源、关系、知识、任务、剧情条件、NPC 立场或可选行动。遇到会明显改变主角目标、关系、重要资源或剧情走向的选择，在选择发生前自然停下，把决定留给玩家；不得把玩家会影响进程的尝试直接写成成功。", GameID, sceneFor(snapshot, "player"), clock, snapshot.PlayerName, snapshot.PlayerProfile, perspectiveRule, lengthRule, detailRule, elaborationRule, customInstruction, "", playerInput, intentType, describeRecipient(def, recipient), publicCharacters, formatBystanders(snapshot.Bystanders), projectedEvents)

	return contextMaterial{System: "你是玩家正文 Agent。你的职责是转述和润色已经确认的玩家可见事件，不继续替玩家或 NPC 作决定。叙事人称、玩家有限视角、事件来源和玩家控制权是不可覆盖的系统规则；创作者补充偏好只在这些边界内生效。只输出故事正文，不要输出 JSON、代码块、标题或解释。\n叙事节奏规则：" + narrativePacingInstruction(), RequiredSources: append(eventIDs(visibleEvents), sceneViewSources(snapshot, "player")...), Required: input, Optional: narrativeSections(snapshot.Messages)}, maxOutputTokens, nil
}

func composeNPC(snapshot worldSnapshot, def gameDefinition, character Character, recipient, intentType string, stageInput npcStageInput, priorTurn string, stage int) contextMaterial {
	base := snapshot
	base.Perceptions = nil
	base.Memories = nil
	return contextMaterial{System: "你是一个重要 NPC。只根据自己的角色资料、个人记忆和本阶段感知作决定。你可以沉默；speech 是你愿意让在场者听见的公开对白；即使玩家耳语，你也只能选择公开回应或沉默，不在 speech 中声明只有玩家听见；私密信息可以不说，action_intent 不作为私密对白的备用通道，不以行动安排额外耳语或口令回复；非言语行动仍可产生仅部分人物感知的结果。action_intent 只是尝试，不是已经发生的事实。memory 只写本次真正获知的简短经历。\n输出合同：只输出单个 JSON 对象，不带 Markdown 围栏；speech、action_intent、memory 均为字符串，无内容用空字符串；silent 是布尔值。多个动作合写在 action_intent 的字符串里，不使用数组或对象。四个字段都要提供。", RequiredSources: append(append([]string{}, stageInput.SourceEventIDs...), sceneViewSources(snapshot, character.EntityID)...), Required: buildNPCPrompt(base, def, character, recipient, intentType, stageInput, priorTurn, stage), Optional: personalSections(snapshot, character.EntityID)}
}

func coordinationScene(snapshot worldSnapshot) string {
	data, _ := json.Marshal(snapshot.SceneViews)
	return string(data)
}

func buildNPCPrompt(snapshot worldSnapshot, def gameDefinition, character Character, recipient, intentType string, stageInput npcStageInput, priorTurn string, stage int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "世界：%s；地点：%s；时间：%s；阶段：%d；玩家意图类型：%s\n", GameID, sceneFor(snapshot, character.EntityID), snapshot.Summary.Clock, stage, intentType)
	fmt.Fprintf(&builder, "你的身份：%s（%s）\n角色资料：%s\n你知道的初始背景：%s\n", character.Name, character.Role, character.Profile, character.Knowledge)
	fmt.Fprintf(&builder, "你的初始关切（故事开始时的动机，不是固定动作脚本）：%s\n结合已提交经历判断关切是否仍然成立；已经完成的事项保持完成，受阻的计划可以调整、延后或放弃。\n", character.InitialConcerns)
	fmt.Fprintf(&builder, "本存档的 NPC 主动性：%s\n", npcInitiativeInstruction(snapshot.Narrative))
	if recipient == "" {
		builder.WriteString("玩家本轮没有明确指定具体对象。你获得了这次感知，请按照本存档的 NPC 主动性和自己的角色关切决定是否回应；可以沉默，也可以在规则允许时主动介入。\n")
	} else if recipient == character.EntityID {
		fmt.Fprintf(&builder, "玩家本轮明确对你说话，目标是%s。你是直接回应者，请优先决定你对玩家的自然回应。\n", describeRecipient(def, recipient))
	} else {
		fmt.Fprintf(&builder, "玩家本轮明确对%s说话。你不是直接回应者，不要代替目标人物回答；只有在有自然理由时才公开反应，是否说话与是否处理自己的事务分别判断。\n", describeRecipient(def, recipient))
	}
	if priorTurn != "" {
		fmt.Fprintf(&builder, "本轮此前你自己的决定：\n%s\n", priorTurn)
	}
	fmt.Fprintf(&builder, "本轮玩家输入中你实际获知的部分：\n%s\n", stageInput.PlayerPerception)
	if stageInput.NewStimulus == "" {
		builder.WriteString("本阶段新增外部刺激：\n（暂无）\n")
		builder.WriteString("本阶段任务：根据自己的角色资料、初始关切、已提交经历和当前感知决定下一步。玩家输入是处境的一部分，不是行动的唯一依据；你可以回应他人、处理自己的事务、延续已有计划、主动发起互动或保持沉默。沉默只表示没有说话，不影响提出行动尝试；不要求每轮都行动。\n")
	} else {
		fmt.Fprintf(&builder, "本阶段新增外部刺激：\n%s\n", stageInput.NewStimulus)
		builder.WriteString("本阶段任务：玩家输入已经在前一阶段处理过。只判断是否需要对新增外部刺激追加反应，不要重新回答玩家，也不要把自己此前的决定当成新消息。\n")
	}
	builder.WriteString("action_intent 只填写会改变外部可观察状态、需要场景协调结果的行动尝试；“继续观察”“保持警惕”“维持原位”和重复已有姿态不属于 action_intent，可以只在 memory 中简短记录。输出 JSON：speech、action_intent、silent、memory。不要输出额外字段。")
	return builder.String()
}

func joinPerceptions(snapshot worldSnapshot, items []Perception) string {
	var parts []string
	for _, item := range items {
		label := item.SourceType
		if source, ok := snapshot.Sources[item.SourceEventID]; ok && source.Actor != "" {
			label = fmt.Sprintf("%s(%s；来源=%s；序号=%d)", characterDisplayName(snapshot.Characters, source.Actor), item.SourceType, source.ID, source.Seq)
		} else if event, ok := eventByID(snapshot.Events, item.SourceEventID); ok && event.ActorID != "" {
			label = fmt.Sprintf("%s(%s)", characterDisplayName(snapshot.Characters, event.ActorID), item.SourceType)
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", label, item.Content))
	}
	if len(parts) == 0 {
		return "（暂无）"
	}
	return strings.Join(parts, "\n")
}
