package storyapp

import (
	"context"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
	// lanternFixtureDefinition is the definition a fixture world starts from. It is derived
	// from the real package whenever one is loaded, so the fixture cannot drift away from
	// what the shipped story actually says.
)

func lanternFixtureDefinition(pack loadedPack) gameDefinition {
	def := pack.Definition
	def.Plot = lanternPlotDefinition()
	return def
}

func (a *App) createFixtureWorld(ctx context.Context, name, mode, playerName, playerProfile string, activate bool) (wiaworld.WorldSummary, error) {
	p, ok := a.pack(GameID)
	if !ok {
		return wiaworld.WorldSummary{}, ErrWorldNotFound
	}
	p.Definition = lanternFixtureDefinition(p)
	if mode == "" {
		mode = p.Definition.Summary.Mode
	}
	if mode != "open" && mode != "guided" {
		return wiaworld.WorldSummary{}, ErrInvalidRequest
	}
	p.Definition.Summary.Mode = mode
	request := CreateWorldRequest{GameID: GameID, ExpectedRevision: p.Definition.Revision, RequestKey: wire.NewID("fixture"), Name: name, PlayerName: playerName, PlayerProfile: playerProfile, Activate: activate}
	return a.createWorldFromPack(ctx, p, request, request.RequestKey)
}

// lanternDefinition builds the same definition without an application, for tests that
// only need prompt material.
func lanternDefinition() gameDefinition {
	return lanternFixtureDefinition(loadedPack{
		Definition: gameDefinition{
			Revision: "lantern-dusk.pack.v2",
			Summary: GameSummary{
				ID:          GameID,
				Title:       "暮灯镇的失踪信使",
				Description: "一场小型调查冒险：雨夜的旧渡口客栈里，失踪的信使留下了一封没有寄出的信。",
				Modes:       []string{"open", "guided"},
				DefaultMode: "guided",
				Mode:        "guided",
			},
			Opening: "雨水顺着旧渡口客栈的屋檐落下。壁炉旁的老板沈岚擦拭着一只空酒杯，佣兵铁杉坐在窗边，目光落在黑沉沉的河面。门口还挤着十来个避雨的客人。就在你推门时，柜台下传来一声短促的金属碰撞。",
			Scene:   "旧渡口客栈",
			Clock:   "第 1 日 19:00",
			Secret:  "沈岚在柜台下藏着一枚染血的信蜡，知道失踪信使曾在今晚来过；铁杉只注意到有人和沈岚低声交谈过，不知道谈话内容。",
			Characters: []wiaworld.Character{
				{EntityID: "npc:innkeeper", DefinitionID: "innkeeper.v1", Name: "沈岚", Role: "客栈老板", Profile: "谨慎、善于观察，不愿让客人恐慌。她熟悉旧渡口的每一条消息，遇到危险时先保护客栈和无辜者。", InitialConcerns: "今晚的异常让她担心客栈和客人的安全。她想知道还有多少时间能准备，也想弄清眼前这个陌生人会不会把麻烦带进店里；对自己的发现只说到有把握的程度。", SpeakingExamples: []string{"先把雨衣挂上，别站在门口灌风。", "这话我只说一遍：今晚店里不出事，比什么都强。"}, Knowledge: "知道失踪信使曾在今晚来过；知道柜台下的染血信蜡，但不会主动向陌生人承认。", InScene: true},
				{EntityID: "npc:mercenary", DefinitionID: "mercenary.v1", Name: "铁杉", Role: "佣兵", Profile: "寡言、务实、对危险敏感。会根据自己看见和听见的迹象判断，不会凭空知道别人的秘密。", InitialConcerns: "他察觉到的异常要么是危险，要么与他无关，他想在事情变大之前分清是哪一种；不愿替别人出头，也看不惯有人在自己眼皮底下糊弄。", SpeakingExamples: []string{"坐这儿。有事说事。", "我听见了，别的不知道。"}, Knowledge: "看见客栈里的人进出和异常动静；不知道沈岚藏着什么。", InScene: true},
			},
			Bystanders: []string{"卖花的老人", "戴蓝围巾的学生", "赶车人", "河运工", "带孩子的旅客", "醉酒的木匠", "灰帽商人", "修钟匠", "披斗篷的妇人", "打瞌睡的船夫"},
		},
	})
}

func lanternPlotDefinition() *PlotDefinition {
	return &PlotDefinition{Revision: "lantern-dusk.plot.v2", Facts: "故事限于旧渡口客栈、码头、渡口仓棚。末班夜渡在第一日20:00离岸。失踪信使名为陆舟，负伤后藏在仓棚，携带河运账目被篡改的证据，信蜡留在沈岚柜台下。沈岚只知道他来过和信蜡；铁杉只知道客栈异常，不自动知道信使位置。追索账目的陌生人会在渡船离岸前寻找信使。固定事实不得为了满足预设结局改写。未来节点是可受玩家和人物行动改变的计划。",
		Nodes: []PlotNode{
			{ID: "dock_warning", AtMinute: 19*60 + 5, After: []string{}, Audience: []string{"player", "npc:innkeeper", "npc:mercenary"}, Condition: "码头夜渡准备开始，未曾发生本节点。", Development: "外部新刺激：码头传来催渡铃，河运工在他实际在场处提醒末班船将在戌初（20:00）离岸，并提及仓棚有人需要帮助。只向实际听到者投影消息；玩家已离开且未获知时不全知广播。重要NPC若要查看、提醒或处置，先交给本人作决定，节点本身不预写其选择。"},
			{ID: "courier_window", AtMinute: 19*60 + 30, After: []string{"dock_warning"}, Audience: []string{"player", "npc:innkeeper", "npc:mercenary"}, Condition: "确认信使在这个时点的处境：依据此前已经成立的营救、转移、预警、阻拦或未干预。", Development: "未干预时，寻人的陌生人到仓棚外，信使仍有走侧门到渡船的机会；已经转移或有人阻拦时，追索者按实际线索行动，不强行把信使移回仓棚。只呈现当前外部事件与风险，不替玩家接受任务。相关重要NPC需要新的选择时请求本人。只让目击者或实际收到消息者知道，不把未来去向直接广播。"},
			{ID: "night_outcome", AtMinute: 20 * 60, After: []string{"courier_window"}, Audience: []string{"player", "npc:innkeeper", "npc:mercenary"}, Terminal: true, Condition: "夜渡离岸，依据已提交行动和证据确认本段调查的阶段结果。", Development: "已救出信使并取得证据时可形成证据保全的结束；干预阻止追索而未取证时保留相应真实结果。完全不参与时，信使可沿侧门自行上船离开，追索者失去踪迹，玩家错过当夜当面调查机会；如已发生受伤、受阻等改变则依据实际结果，不强行离开。流程型可在本段结果成立时结束，开放型仅结束这一事件，世界仍可继续。仅通过实际可感知的船离岸、目击或转述告知玩家；未获知具体去向不能在正文揭示。"},
		}}
}

func (a *App) worldPath(worldID string) string { return a.worldPathFor(GameID, worldID) }
