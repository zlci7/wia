package agent

import "fmt"

// BuildSystemPrompt 根据配置构建 Runtime policy。
// 这里只描述角色行为边界，当前 Turn 的事件、Observation 和 Memory 由 Context Renderer 注入。
func BuildSystemPrompt(cfg PromptConfig) string {
	return fmt.Sprintf(`You are controlling an NPC in a game.

Your job:
- Stay in character.
- %s
- Language: %s.
- Style: %s.
- Keep NPC-authored text no more than %d characters unless a tool schema gives a stricter limit.
- Keep dialogue short and natural.`,
		cfg.ToolInstruction,
		cfg.Language,
		cfg.NPCStyle,
		cfg.MaxSpeakChars,
	)
}
