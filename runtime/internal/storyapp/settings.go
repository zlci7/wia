package storyapp

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

const (
	PerspectiveFirstPerson  = "first_person"
	PerspectiveSecondPerson = "second_person"
	PerspectiveThirdPerson  = "third_person"

	NarrativeLengthConcise  = "concise"
	NarrativeLengthStandard = "standard"
	NarrativeLengthDetailed = "detailed"

	NarrativeDetailRestrained = "restrained"
	NarrativeDetailBalanced   = "balanced"
	NarrativeDetailRich       = "rich"

	PlayerElaborationRestrained = "restrained"
	PlayerElaborationNatural    = "natural"
	PlayerElaborationExpressive = "expressive"

	NPCInitiativeResponsive = "responsive"
	NPCInitiativeContextual = "contextual"
	NPCInitiativeProactive  = "proactive"
)

type NarrativeSettings struct {
	Perspective       string `json:"perspective"`
	Length            string `json:"length"`
	Detail            string `json:"detail"`
	PlayerElaboration string `json:"player_elaboration"`
	NPCInitiative     string `json:"npc_initiative"`
	CustomInstruction string `json:"custom_instruction"`
}

type UpdateNarrativeSettingsRequest struct {
	Perspective          string `json:"perspective"`
	Length               string `json:"length"`
	Detail               string `json:"detail"`
	PlayerElaboration    string `json:"player_elaboration"`
	NPCInitiative        string `json:"npc_initiative"`
	CustomInstruction    string `json:"custom_instruction"`
	ExpectedContextEpoch int64  `json:"expected_context_epoch"`
}

func defaultNarrativeSettings() NarrativeSettings {
	return NarrativeSettings{
		Perspective:       PerspectiveSecondPerson,
		Length:            NarrativeLengthStandard,
		Detail:            NarrativeDetailBalanced,
		PlayerElaboration: PlayerElaborationNatural,
		NPCInitiative:     NPCInitiativeContextual,
	}
}

func validateNarrativeSettings(settings NarrativeSettings) (NarrativeSettings, error) {
	settings.Perspective = strings.TrimSpace(settings.Perspective)
	settings.Length = strings.TrimSpace(settings.Length)
	settings.Detail = strings.TrimSpace(settings.Detail)
	settings.PlayerElaboration = strings.TrimSpace(settings.PlayerElaboration)
	settings.NPCInitiative = strings.TrimSpace(settings.NPCInitiative)
	settings.CustomInstruction = cleanText(settings.CustomInstruction)
	if settings.Perspective != PerspectiveFirstPerson && settings.Perspective != PerspectiveSecondPerson && settings.Perspective != PerspectiveThirdPerson {
		return NarrativeSettings{}, ErrInvalidRequest
	}
	if settings.Length != NarrativeLengthConcise && settings.Length != NarrativeLengthStandard && settings.Length != NarrativeLengthDetailed {
		return NarrativeSettings{}, ErrInvalidRequest
	}
	if settings.Detail != NarrativeDetailRestrained && settings.Detail != NarrativeDetailBalanced && settings.Detail != NarrativeDetailRich {
		return NarrativeSettings{}, ErrInvalidRequest
	}
	if settings.PlayerElaboration != PlayerElaborationRestrained && settings.PlayerElaboration != PlayerElaborationNatural && settings.PlayerElaboration != PlayerElaborationExpressive {
		return NarrativeSettings{}, ErrInvalidRequest
	}
	if settings.NPCInitiative != NPCInitiativeResponsive && settings.NPCInitiative != NPCInitiativeContextual && settings.NPCInitiative != NPCInitiativeProactive {
		return NarrativeSettings{}, ErrInvalidRequest
	}
	if len([]rune(settings.CustomInstruction)) > 1000 {
		return NarrativeSettings{}, ErrInvalidRequest
	}
	return settings, nil
}

func loadNarrativeSettings(ctx context.Context, db *sql.DB) NarrativeSettings {
	settings := defaultNarrativeSettings()
	for key, target := range map[string]*string{
		"narrative_perspective":        &settings.Perspective,
		"narrative_length":             &settings.Length,
		"narrative_detail":             &settings.Detail,
		"player_elaboration":           &settings.PlayerElaboration,
		"npc_initiative":               &settings.NPCInitiative,
		"narrative_custom_instruction": &settings.CustomInstruction,
	} {
		if value, err := metaGet(ctx, db, key); err == nil {
			*target = value
		}
	}
	validated, err := validateNarrativeSettings(settings)
	if err != nil {
		return defaultNarrativeSettings()
	}
	return validated
}

func (a *App) UpdateNarrativeSettings(ctx context.Context, worldID string, request UpdateNarrativeSettingsRequest) (NarrativeSettings, WorldSummary, error) {
	settings, err := validateNarrativeSettings(NarrativeSettings{
		Perspective: request.Perspective, Length: request.Length, Detail: request.Detail,
		PlayerElaboration: request.PlayerElaboration, NPCInitiative: request.NPCInitiative,
		CustomInstruction: request.CustomInstruction,
	})
	if err != nil {
		return NarrativeSettings{}, WorldSummary{}, err
	}
	path, status, err := a.worldRecord(ctx, worldID)
	if err != nil {
		return NarrativeSettings{}, WorldSummary{}, err
	}
	if status != "ready" {
		return NarrativeSettings{}, WorldSummary{}, ErrWorldNotReady
	}
	world := a.worldRuntimeFor(worldID)
	world.mu.Lock()
	defer world.mu.Unlock()
	if world.savePending {
		return NarrativeSettings{}, WorldSummary{}, ErrWorldBusy
	}
	store, err := openWorldDB(path)
	if err != nil {
		return NarrativeSettings{}, WorldSummary{}, err
	}
	defer store.db.Close()
	if count, err := countActiveRuns(ctx, store.db); err != nil {
		return NarrativeSettings{}, WorldSummary{}, err
	} else if count > 0 {
		return NarrativeSettings{}, WorldSummary{}, ErrWorldBusy
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return NarrativeSettings{}, WorldSummary{}, err
	}
	defer tx.Rollback()
	currentEpochText, err := metaGetTx(ctx, tx, "context_epoch")
	if err != nil {
		return NarrativeSettings{}, WorldSummary{}, err
	}
	currentEpoch, err := strconv.ParseInt(currentEpochText, 10, 64)
	if err != nil {
		return NarrativeSettings{}, WorldSummary{}, err
	}
	if request.ExpectedContextEpoch > 0 && request.ExpectedContextEpoch != currentEpoch {
		return NarrativeSettings{}, WorldSummary{}, ErrVersionConflict
	}
	values := map[string]string{
		"narrative_perspective":        settings.Perspective,
		"narrative_length":             settings.Length,
		"narrative_detail":             settings.Detail,
		"player_elaboration":           settings.PlayerElaboration,
		"npc_initiative":               settings.NPCInitiative,
		"narrative_custom_instruction": settings.CustomInstruction,
		"context_epoch":                strconv.FormatInt(currentEpoch+1, 10),
		"updated_at":                   nowText(),
	}
	for key, value := range values {
		if err := metaSetTx(ctx, tx, key, value); err != nil {
			return NarrativeSettings{}, WorldSummary{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return NarrativeSettings{}, WorldSummary{}, err
	}
	if err := a.touchWorld(ctx, worldID); err != nil {
		return NarrativeSettings{}, WorldSummary{}, err
	}
	summary, err := a.worldSummary(ctx, worldID)
	return settings, summary, err
}

func narrativePerspectiveInstruction(settings NarrativeSettings, playerName string) string {
	switch settings.Perspective {
	case PerspectiveFirstPerson:
		return "使用第一人称有限视角。正文旁白用“我”指代主角；NPC 对白中的“我”仍属于说话的 NPC，不能混淆说话人。"
	case PerspectiveThirdPerson:
		return "使用第三人称有限视角。正文旁白用主角姓名“" + playerName + "”指代主角，不用“玩家”“来人”“来客”或泛称“旅人”替代。"
	default:
		return "使用第二人称有限视角。正文旁白用“你”指代主角，不用“玩家”“来人”“来客”“旅人”或主角姓名作为第三人称代称。"
	}
}

func narrativeLengthInstruction(settings NarrativeSettings) (string, int) {
	switch settings.Length {
	case NarrativeLengthConcise:
		return "直接呈现关键回应和变化，简单互动可以用几句话结束。篇幅随本轮新增内容决定，不设最低字数，不用重复环境、已完成动作或等待状态填充。", 768
	case NarrativeLengthDetailed:
		return "对有信息量的对白、动作和观察充分展开，简单回应仍可简短结束。篇幅随本轮新增内容决定，不设最低字数，不用重复环境、已完成动作或等待状态填充。", 3072
	default:
		return "根据本轮新增内容适度展开，首次观察或重要变化时增加细节，简单问答可以简短结束。不设最低字数，不用重复环境、已完成动作或等待状态填充。", 1536
	}
}

func narrativeDetailInstruction(settings NarrativeSettings) string {
	switch settings.Detail {
	case NarrativeDetailRestrained:
		return "描写保持克制，只写理解本轮所需的动作、对白和环境变化。"
	case NarrativeDetailRich:
		return "可以增加较丰富的感官、环境和动作细节，补充符合当前情境的临时、低影响表现。影响后续进程的关键事实、人物决定和持续状态，以已有设定及本轮确认结果为依据；细节不与已有内容矛盾，不制造新的后续依据。"
	default:
		return "使用平衡的描写密度，以清晰动作和对白为主，补充少量有作用的环境与感官细节。"
	}
}

func narrativePacingInstruction() string {
	return "围绕本轮新增内容展开，在有意义的对白、动作或观察结束处自然收笔。默认以最后一句有内容的对白或动作结束，不另起一段让人物看着主角、等主角回答或解释可以不回答。例如问候可以停在老板的‘晚上好’，观察可以停在一处相关细节；这些结束方式已经把下一步留给玩家。保留玩家控制权，是把尚未选择的重要行动留给玩家，不是每轮提醒轮到玩家操作。仅在等待本身具有剧情意义时呈现等待，例如约定的人迟迟未到；不以‘等你回答——或不答’‘等你多说什么——或不急说’等套话固定收尾。历史正文只提供连续状态，不提供必须模仿的句式或收尾模板；已经完成的动作保持完成，从当前状态接续本轮意图。"
}

func playerElaborationInstruction(settings NarrativeSettings) string {
	switch settings.PlayerElaboration {
	case PlayerElaborationRestrained:
		return "克制扮演：忠实转述玩家已经表达的内容，只补理解动作所需的必要衔接。玩家没有给出具体台词时优先使用间接叙述，不主动替主角拟写对白或感受。"
	case PlayerElaborationExpressive:
		return "小说共创：可以在主角设定和玩家本轮已经选择的方向内，更充分地补充主角的对白、连续日常动作和短暂感受，使段落自然完整；仍不得新增会影响后续的身份信息、秘密、目标、接受或拒绝、承诺、关系变化、关键资源处置、危险行动、移动目的地或下一步选择，也不得自行补出多轮 NPC 对话。"
	default:
		return "自然共创：不要机械复述输入。可以把玩家已经明确表达的意图自然补成与其等价的简短台词、日常动作、即时感官或身体反应和必要衔接；例如“跟老板打招呼”可以写成点头并说“晚上好”。补写只能改善当下表达，不得新增会影响后续的身份信息、秘密、目标、接受或拒绝、承诺、关系变化、关键资源处置、危险行动、移动目的地或下一步选择。"
	}
}

func playerElaborationLabel(settings NarrativeSettings) string {
	switch settings.PlayerElaboration {
	case PlayerElaborationRestrained:
		return "克制扮演"
	case PlayerElaborationExpressive:
		return "小说共创"
	default:
		return "自然共创"
	}
}

func npcInitiativeInstruction(settings NarrativeSettings) string {
	switch settings.NPCInitiative {
	case NPCInitiativeResponsive:
		return "回应为主：对他人的交谈优先按需回应，减少无关插话；仍可根据自己的职责、利益、安全和当前关切处理必要事务。是否说话与是否行动分别判断。"
	case NPCInitiativeProactive:
		return "积极互动：可以依据自己的身份、知识、经历和当前关切主动提问、试探、打趣、转移话题或采取相关行动，不必等待玩家点名；主动行为依据已知处境、个人动机、已有计划和当前机会，不抢走其他人物的回答，不凭空知道信息，也不为热闹而反复插话。"
	default:
		return "按情境主动：不必等玩家点名；根据你的身份职责、兴趣、安全、承诺、当前关切和合适机会，可以主动招呼、追问、试探、打趣或采取相关行动。普通进入、环顾和走动不要求每个在场人物都回应，也不要仅因在场而插话。"
	}
}

func narrativeReference(settings NarrativeSettings, playerName string) string {
	switch settings.Perspective {
	case PerspectiveFirstPerson:
		return "我"
	case PerspectiveThirdPerson:
		return playerName
	default:
		return "你"
	}
}
