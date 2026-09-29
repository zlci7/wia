package storyapp

import (
	"encoding/json"
	"fmt"
	"strings"

	"gameagent/backend/internal/story"
	"gameagent/backend/internal/turn"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
)

func composeIntent(snapshot turn.Snapshot, run wiaworld.Run) turn.Material {
	participants := sceneCharacters(snapshot.Characters)
	var characters strings.Builder
	for _, character := range participants {
		fmt.Fprintf(&characters, "- %s：%s（%s）\n", character.EntityID, character.Name, character.Role)
	}
	input := fmt.Sprintf("当前地点：%s\n当前时间：%s\n在场人物：\n%s玩家输入：%s\n显式目标（若有）：%s\n请判断玩家本轮是 speak、observe 还是 act；如果玩家明确向某个在场人物说话，只返回该人物的 entity_id；没有明确对象时 addressee_id 返回空字符串或 null。visibility 只能是 public 或 private。只输出 JSON：{\"intent_type\":\"speak\",\"addressee_id\":\"npc:...\",\"visibility\":\"public\"}。人物名出现在谈话内容里不等于玩家正在对该人物说话。", turn.SceneFor(snapshot, "player"), snapshot.Summary.Clock, characters.String(), run.Input, wire.Clean(run.AddresseeID))
	input = turn.IntentVisibilityRule + "\n" + input
	input += "\naddressee_id 只从上方在场重要人物列表的 entity_id 选择。向船夫、搬运工等背景人物说话时返回空字符串，由场景协调组织其回应；不为背景人物自造 npc:... ID。没有在场重要人物时只能为空。"
	input += "\n世界：" + snapshot.Summary.GameID + "\n公开世界背景：" + snapshot.Definition.Background
	input += "\n另输出整数 wait_minutes：玩家明确选择等待时，解析其期望经过的分钟数，最多120；等待某事件但未指定时长时取120作为本轮等待上限。单纯沉默、观察或普通交谈取0。这里只解析意愿，实际经过时间由协调与剧情边界确定。玩家不参与也允许时间和世界事件继续发展。等待属于 act，intent_type 始终只从 speak、observe、act 中选择，时长另放 wait_minutes。"

	return turn.Material{System: "你负责把玩家本轮输入解析成结构化回合意图。根据当前输入、在场名单与已提交对话判断目标、可见范围与意图类型，不替玩家执行行动。", RequiredSources: append([]string{run.RunID + ":input"}, turn.SceneViewSources(snapshot, "player")...), Required: input, Optional: turn.DialogueSections(snapshot)}
}

func composeCoordination(snapshot turn.Snapshot, run wiaworld.Run, intent turn.TurnIntent, decisions map[string]turn.NPCDecision, events []wiaworld.Event, publicReplies string) turn.Material {
	policy, revision := turn.BehaviorPolicy(snapshot.Narrative, "coordination")
	var actionCandidates []wiaworld.Event
	for _, event := range events {
		if event.EventType == "npc_action_intent" || event.EventType == "player_action_intent" {
			actionCandidates = append(actionCandidates, event)
		}
	}
	actionJSON, _ := json.Marshal(actionCandidates)
	publicCharacters := turn.PublicCharacterContext(snapshot.Characters, wiaworld.CharacterIDs(sceneCharacters(snapshot.Characters)))
	input := fmt.Sprintf("世界：%s\n当前地点与情境：%s\n当前时间：%s\n当前公开人物(JSON)：%s\n当前背景人群：%s\n玩家本轮输入：%s\n结构化意图：type=%s；target=%s；visibility=%s\n主角共创边界：当前模式为%s。你只协调玩家实际输入已经表达的尝试和 NPC 已提交的行动；等价的简短台词、日常动作和表现性衔接由正文阶段处理，不在此新增玩家身份、秘密、目标、接受或拒绝、承诺、关系、关键资源处置、危险行动或移动目的地。\nNPC 已确定的公开对白：%s\nNPC 协调提案（只包含公开对白、行动尝试与沉默状态，不含个人记忆）：\n%s\n待裁定行动(JSON)：%s\n所有可用重要人物：%s\n当前在场人物 entity_id：%s\n请协调本轮事实。每个待裁定行动必须且只能产生一个 outcome，并用 action_id 精确引用；status 只能是 succeeded、failed、partial、not_executed；not_executed 表示重复、已覆盖或前置条件未成立而未另行执行，不代表动作成功；每项仍保留自己的 action_id 和结果；content 写已确定结果而不是尝试；recipients 只列实际感知结果的 player 或人物 entity_id，行动者本人可省略。scene 必须保留未被本轮事件改变的地点、在场人物和背景人群，不得凭空让人物离开；叙述人物时优先使用姓名，不根据姓名猜测代词。scene_characters 只给出回合结束后实际在场的重要 NPC entity_id，不要包含 player；人物进入或离开只影响之后的阶段，不回填此前信息。输出 JSON：time_minutes、scene、scene_characters、outcomes、scene_updates。", snapshot.Summary.GameID, turn.CoordinationScene(snapshot), snapshot.Summary.Clock, publicCharacters, turn.FormatBystanders(snapshot.Definition.BystanderRefs, snapshot.Bystanders), run.Input, intent.IntentType, intent.AddresseeID, intent.Visibility, turn.PlayerElaborationLabel(snapshot.Narrative), publicReplies, turn.CoordinationDecisionContext(decisions, snapshot.Characters), actionJSON, turn.AvailableCharacterIDs(snapshot.Characters), strings.Join(wiaworld.CharacterIDs(sceneCharacters(snapshot.Characters)), ","))

	input += "\n本轮场景协调策略：\n" + policy + "\n"
	input += "\nscene_characters 专指与玩家在回合结束时处于同一现场、之后能接收玩家普通交谈的重要NPC。分接收者场景包含场外人物资料，不代表他们与玩家在一起。玩家离开原地点而人物留在原地点时，从这个名单移出相应人物；人物依自己的已确认行动跟随抵达时才保留。只更新名单不自动移动人物，各人的 scene_updates 仍分别保留其实际位置。"
	input += "\n作者层世界规则：" + snapshot.Definition.Rules + "\n作者事实（不是全场共享知识）：" + snapshot.Definition.Secret + "\n地点资料（不是人物已经到达的结果）：" + wire.MarshalJSON(snapshot.Definition.Locations)
	input += "\n行动衔接合同：待裁定清单内所有 action_intent 都是未执行提案，不因措辞使用过去时而成为事实。按阶段及依赖关系协调；后一提案依赖前一项时，先确定前项结果。重叠、重复且没有新的需要时只完成一次，对其他项返回 not_executed 并说明已被哪项覆盖或为何无须另行执行，不补造消耗、时间经过或障碍来使重复合理。重要NPC的对白仅取本人已确定的公开对白，不从行动提案中补造其问话、承诺或回应。\n"
	input += sceneSourcePrompt(snapshot, run, intent, events)
	input += turn.PlotContext(snapshot)
	if intent.WaitMinutes > 0 {
		input += fmt.Sprintf("\n本轮已表达的等待目标：%d分钟；当前允许执行到%d分钟。通常令 time_minutes 等于后者，日常添水、闲聊、保持观察等可在等待期间发生，不独立缩短玩家的等待。只有需要玩家即时作关键选择、直接危险或玩家明确指定的中止事件才提前停下。另输出 interrupt_source_ids 数组：提前停止时引用本轮确实造成中止的事件ID，否则为[]。输出实际等候后的情境，不提前解决未来节点。", intent.WaitMinutes, min(intent.WaitMinutes, turn.PlotTimeLimit(snapshot)))
		input += "\n中断依据也可引用提供的已提交历史事件，但只有其影响仍在当前情境中成立时才用于中断；事件发生过不等于危险一直持续。引用真实 event_id，不使用人物ID、view别名或未来 definition 节点作为已经发生的证据。"
	}
	input += "\n清单内 player_action_intent 是玩家已经选择的尝试，也必须裁定实际结果，不能仅因玩家说已成功就确认成功；受已知条件、人物决定和本轮时间边界约束。等待仅执行到实际时点。清单外的玩家表达不新增 outcome。"
	input += "\n没有明确重要NPC对象的玩家交谈也列为互动尝试：若是向实际可接触的背景人物说话，可在该互动的outcome中组织背景人物基于处境的可见回应，不自动让玩家接受其请求。重要NPC名单内的人物仍只使用本人已确定的对白与行动，不能借背景互动重新决定。单纯面向大家的表达自然承接已有回应，没有新回应也可据实说明，不为了填充结果创造角色。背景人物未出现或尚未回应时，明确这是无人回应或等待确认，不推定玩家已与其达成约定。"
	input += "\n完整字段类型：time_minutes 为整数，scene 为自然语言字符串（不是场景视图数组或对象）；scene_characters 为字符串数组；outcomes 为对象数组，每项仅含 action_id 字符串、status 字符串、content 字符串、recipients 字符串数组、bystanders 字符串数组；scene_updates 为对象数组，每项仅含 content 字符串、source_ids 字符串数组、recipients 字符串数组。没有更新或行动时使用空数组，不使用 null。只输出合同列出的字段。"
	input += "\nbystanders 只列出本轮结果里真正参与的已定义路人 id，取自上方背景人群列表中的 id；他们据此获得本人经历。路人仅在场景中出现、被正文提到或玩家知道某事，都不算参与，不要据此填写。"
	input += turn.EventOpportunityContract(snapshot)
	input += "\n历史 generated_event_plan 是作者未来计划，只有 plot_result 或行动结果才表示实际发生。"
	return turn.Material{PolicyRevision: revision, System: turn.BehaviorContract + "\n你是场景协调 Agent。你可以读取本轮协调资料来裁定行动结果、时间和场景，但不要写玩家正文，也不要把 NPC 的行动尝试直接当成成功事实。\ntime_minutes 是本轮新增的游戏内分钟数，取 0 至 120 的整数，不是时钟读数或当天累计分钟。例如 19:02 经过一分钟，time_minutes 为 1，而非 1142 或 1143。" + "\n输出合同：只输出单个 JSON 对象，不带 Markdown 围栏。outcomes 与待裁定行动(JSON)一一对应，action_id 原样使用该列表中的 event_id。列表为空时 outcomes 必须为 []。清单外的输入、公开对白和此前已提交结果不另建 outcome，不编造行动 ID。", RequiredSources: append(turn.EventIDs(events), turn.SceneViewSources(snapshot, "")...), Required: input, Optional: turn.CoordinationSections(snapshot.Events)}
}

func composeNarration(snapshot turn.Snapshot, run wiaworld.Run, def story.Definition, recipient, intentType string, visibleEvents []wiaworld.Event, clock string, sceneCharacters []string) (turn.Material, int, error) {
	playerInput := run.Input
	projectedEvents, err := json.Marshal(narrativeEvents(visibleEvents, snapshot.Characters, snapshot.PlayerName, snapshot.Narrative))
	if err != nil {
		return turn.Material{}, 0, err
	}
	publicCharacters := turn.PublicCharacterContext(snapshot.Characters, sceneCharacters)
	perspectiveRule := turn.PerspectiveInstruction(snapshot.Narrative, snapshot.PlayerName)
	lengthRule, maxOutputTokens := turn.LengthInstruction(snapshot.Narrative)
	detailRule := turn.DetailInstruction(snapshot.Narrative)
	elaborationRule := turn.PlayerElaborationInstruction(snapshot.Narrative)
	customInstruction, revision := turn.BehaviorPolicy(snapshot.Narrative, "narration")
	if snapshot.Plot != nil {
		customInstruction += "\n多阶段叙事：事件清单是可用依据，不是必须逐条复述的稿件。保留关键选择与结果，相同立场的连续对白可以合并、间接转述，省去重复环境与动作；在现有回复长度预算内完整收尾。玩家原文中的等待是请求，实际经过的时间以 time_advanced 事件为准；未处理的剩余时段尚未发生。按事件阶段先后承接，不把较晚节点的刺激写到较早行动之前。"
	}
	input := fmt.Sprintf("剧本：%s\n当前地点与情境：%s\n时间：%s\n主角：%s\n主角简介：%s\n叙事人称规则：%s\n正文篇幅规则：%s\n描写密度规则：%s\n主角补写规则：%s\n正文表达策略（在明确选项与固定合同内生效）：%s\n玩家可见历史正文（只作剧情连贯参考，不得写成本轮再次发生；历史中不一致的人称不得继续沿用）：%s\n玩家本轮自己的完整表达：%s\n玩家意图类型：%s\n明确交谈对象：%s\n当前公开人物：%s\n当前背景人群：%s\n本轮玩家可见且已经确定的对白与结果：\n本轮玩家可见事件(JSON)：%s\n只根据以上玩家可见事件组织一段自然正文。事件的 actor_id、actor_name、narrative_reference 和 event_type 是事实边界；正文旁白必须使用 narrative_reference 指代相应行动者，对白必须保持原说话人、含义和可听范围；speech_scope=public_current_scene 的玩家表达或 NPC 对白已按公开范围分发，应呈现为在场者可听见，不改成仅特定人物听见；private_recipient 的玩家表达保持私聊范围。玩家试探语气不能被正文补写成耳语，NPC 公开对白不改成私密回复，NPC 的新对白和可见行动必须来自事件，不得由正文自行添加。细节是否越界取决于含义而非动作大小；例如在邀请后推碗、点头可能表示接受，不能替未作决定的人补出这类回应。outcome_status=not_executed 表示没有另行执行，不将该提案写成发生，也不逐项播报未执行清单。玩家输入中的“我”按叙事人称规则转述，NPC 台词中的“我”仍属于该 NPC。只呈现主角能够感知、已经知道或有明确来源获知的信息；不得断言其他人物未表露的心理，也不得使用“没有任何人注意到”等主角无法确认的全知判断。当前人物和背景人群继续留在场景状态中，但正文只提与本轮有关的少量人物；没有写到不表示离场，禁止为了证明仍在场而逐个点名或逐项汇报未变化状态。可以自由补充临时、低影响、符合场景的感官、天气、日常陈设和氛围；不得把补充陈设写成线索、障碍或可改变进程的资源。按照主角补写规则补全玩家表达，保留玩家已经说出的原意、态度和重要信息；可以直接承接而不逐字复述，语气轻缓不等于只有特定人听见；不得通过压低声音或空间描述缩小已确定的公开可听范围。不能把一句陈述改写成多次询问，不把疑问改成承诺、把拒绝改成接受，也不增加会成为后续依据的新事实。所有正文补写都只改善本轮呈现；删除这些补写后，不得改变下一轮的地点、物品持有、资源、关系、知识、任务、剧情条件、NPC 立场或可选行动。遇到会明显改变主角目标、关系、重要资源或剧情走向的选择，在选择发生前自然停下，把决定留给玩家；不得把玩家会影响进程的尝试直接写成成功。", snapshot.Summary.GameID, turn.SceneFor(snapshot, "player"), clock, snapshot.PlayerName, snapshot.PlayerProfile, perspectiveRule, lengthRule, detailRule, elaborationRule, customInstruction, "", playerInput, intentType, describeRecipient(def, recipient), publicCharacters, turn.FormatBystanders(snapshot.Definition.BystanderRefs, snapshot.Bystanders), projectedEvents)

	input += "\n公开世界背景：" + snapshot.Definition.Background + "\n世界规则：" + snapshot.Definition.Rules
	material := turn.Material{PolicyRevision: revision, System: turn.BehaviorContract + "\n你是玩家正文 Agent。你的职责是转述和润色已经确认的玩家可见事件，不继续替玩家或 NPC 作决定。叙事人称、玩家有限视角、事件来源和玩家控制权是不可覆盖的系统规则；创作者补充偏好只在这些边界内生效。只输出故事正文，不要输出 JSON、代码块、标题或解释。", RequiredSources: append(turn.EventIDs(visibleEvents), turn.SceneViewSources(snapshot, "player")...), Required: input, Optional: turn.NarrativeSections(snapshot.Messages)}
	material = withLongMemory(material, snapshot, "player", run.Input)
	return material, maxOutputTokens, nil
}

func composeNPC(snapshot turn.Snapshot, def story.Definition, character wiaworld.Character, recipient, intentType string, stageInput turn.StageInput, priorTurn string, stage int) turn.Material {
	_, revision := turn.BehaviorPolicy(snapshot.Narrative, "npc")
	base := snapshot
	base.Perceptions = nil
	base.Memories = nil
	material := turn.Material{PolicyRevision: revision, System: turn.BehaviorContract + "\n你是一个重要 NPC。只根据自己的角色资料、个人记忆和本阶段感知作决定。你可以沉默；speech 是你愿意让在场者听见的公开对白；即使玩家耳语，你也只能选择公开回应或沉默，不在 speech 中声明只有玩家听见；私密信息可以不说，所有对外说出的内容必须放入 speech；action_intent 只表达非言语行动，不夹带问话、台词或转述式发言。action_intent 不作为私密对白的备用通道，不以行动安排额外耳语或口令回复；非言语行动仍可产生仅部分人物感知的结果。action_intent 只是尝试，不是已经发生的事实。memory 只写本次真正获知的简短经历。\n输出合同：只输出单个 JSON 对象，不带 Markdown 围栏；speech、action_intent、memory 均为字符串，无内容用空字符串；silent 是布尔值。多个动作合写在 action_intent 的字符串里，不使用数组或对象。四个字段都要提供。", RequiredSources: append(append([]string{}, stageInput.SourceEventIDs...), turn.SceneViewSources(snapshot, character.EntityID)...), Required: buildNPCPrompt(base, def, character, recipient, intentType, stageInput, priorTurn, stage), Optional: turn.PersonalSections(snapshot, character.EntityID)}
	material = withLongMemory(material, snapshot, character.EntityID, stageInput.PlayerPerception+"\n"+stageInput.NewStimulus)
	if _, ok := snapshot.LongMemory[character.EntityID]; ok {
		committed := map[string]bool{}
		for _, s := range snapshot.LongMemory[character.EntityID].Archive {
			committed[s.EventID] = true
		}
		for _, p := range snapshot.Perceptions[character.EntityID] {
			if !committed[p.SourceEventID] {
				material.Required += "\n本轮前阶段已确认的个人经历（不是新的待执行行动）：" + turn.JoinPerceptions(snapshot, []wiaworld.Perception{p})
				material.RequiredSources = append(material.RequiredSources, p.SourceEventID)
			}
		}
		material.System += "\n需要查找较早经历时，可额外输出 recall_query 字符串（最多256字），其他字段保留空值与silent=true；程序仅检索你自己的获准经历，每次最多五条，最多两次。完成决定时省略recall_query或置为空字符串。"
	}
	return material
}

func buildNPCPrompt(snapshot turn.Snapshot, def story.Definition, character wiaworld.Character, recipient, intentType string, stageInput turn.StageInput, priorTurn string, stage int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "公开世界背景：%s\n世界规则：%s\n", snapshot.Definition.Background, snapshot.Definition.Rules)
	policy, _ := turn.BehaviorPolicy(snapshot.Narrative, "npc")
	fmt.Fprintf(&builder, "本轮 NPC 共用决策策略：\n%s\n", policy)
	fmt.Fprintf(&builder, "世界：%s；地点：%s；时间：%s；阶段：%d；玩家意图类型：%s\n", snapshot.Summary.GameID, turn.SceneFor(snapshot, character.EntityID), snapshot.Summary.Clock, stage, intentType)
	fmt.Fprintf(&builder, "你的身份：%s（%s）\n角色资料：%s\n你知道的初始背景：%s\n", character.Name, character.Role, character.Profile, character.Knowledge)
	fmt.Fprintf(&builder, "你的初始关切（故事开始时的动机，不是固定动作脚本）：%s\n", character.InitialConcerns)
	if len(character.SpeakingExamples) > 0 {
		// Style reference only: a sample shows how the person talks, it is not something
		// that happened and must not be treated as memory.
		fmt.Fprintf(&builder, "你的说话风格示例（只作语气与用词参考，不是已经发生的事，也不要照抄）：\n- %s\n", strings.Join(character.SpeakingExamples, "\n- "))
	}
	fmt.Fprintf(&builder, "本存档的 NPC 主动性：%s\n", turn.NPCInitiativeInstruction(snapshot.Narrative))
	if recipient == "" {
		builder.WriteString("玩家本轮没有明确指定具体对象。你获得了这次感知，请按照本存档的 NPC 主动性和自己的角色关切决定是否回应；可以沉默，也可以在规则允许时主动介入。\n")
	} else if recipient == character.EntityID {
		fmt.Fprintf(&builder, "玩家本轮明确对你说话，目标是%s。你是直接回应者，请优先决定你对玩家的自然回应。\n", describeRecipient(def, recipient))
	} else {
		fmt.Fprintf(&builder, "玩家本轮明确对%s说话。你不是直接回应者，不要代替目标人物回答；只有在有自然理由时才公开反应，是否说话与是否处理自己的事务分别判断。\n", describeRecipient(def, recipient))
	}
	if priorTurn != "" {
		fmt.Fprintf(&builder, "本轮此前你自己的表达与待执行提案：\n%s\n", priorTurn)
	}
	fmt.Fprintf(&builder, "本轮玩家输入中你实际获知的部分：\n%s\n", stageInput.PlayerPerception)
	if stage < 5 {
		builder.WriteString("玩家本轮表达中的行动与等待尚待场景协调。以当前游戏内时间和已确认结果理解进度；请求等待一小时不等于已经过了一小时，也不代表玩家已到达目标地点或完成行动。\n")
	}
	if stageInput.NewStimulus == "" {
		builder.WriteString("本阶段新增外部刺激：\n（暂无）\n")
		builder.WriteString("本阶段任务：依据本轮处境作出首次决定，按有效行为策略处理。\n")
	} else if stage >= 5 {
		fmt.Fprintf(&builder, "本阶段新增外部刺激：\n%s\n本轮前段已协调完成，请依据获准结果及个人经历决定对此新世界事件的反应。这里没有转发玩家本轮未向你表达的原话。只提交新决定；已完成行动保持完成。\n", stageInput.NewStimulus)
	} else {
		fmt.Fprintf(&builder, "本阶段新增外部刺激：\n%s\n", stageInput.NewStimulus)
		builder.WriteString("本阶段任务：玩家输入已经在前一阶段处理过。只判断是否需要对新增外部刺激追加反应，不要重新回答玩家，也不要把自己此前的决定当成新消息。此前行动尚待协调，即使措辞像已完成也不是执行结果；保留原计划时 action_intent 留空，只有新刺激确实带来新需要才提出增量行动，不重复填写、续做或假定前项已成功。\n")
	}
	builder.WriteString("action_intent 只填写会改变外部可观察状态、需要场景协调结果的行动尝试；“继续观察”“保持警惕”“维持原位”和重复已有姿态不属于 action_intent，可以只在 memory 中简短记录。输出 JSON，提供 speech、action_intent、silent、memory；其余只使用本次系统输出合同明确允许的可选字段。")
	return builder.String()
}
