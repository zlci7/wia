package turn

import (
	"strings"
	"testing"
)

func TestNarrativePrefixOnlyProjectsTopLevelProse(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{`{"scene_changes":{"positions":{"narrative":"secret"}},"narrative":"你看到`, "你看到"},
		{`{"narrative":"你说：\"你好\"。\n然后`, "你说：\"你好\"。\n然后"},
		{`{"narrative":"\u4f60\u597d\ud83d`, "你好"},
		{`{"narrative":"\ud83d\ude00"}`, "😀"},
		{`{"narrative":"片段\u4f`, "片段"},
		{`{"continuity_notes":[{"narrative":"secret"}]}`, ""},
		{`{"narrative":"正文","continuity_notes":[{"content":"秘密"}]}`, "正文"},
	} {
		if got := NarrativePrefix(tc.text); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.text, got, tc.want)
		}
	}
}

func TestCreationPlayerSnapshotPreservesOrderAndIsolation(t *testing.T) {
	session := newCreationTestSession(t)
	session.exchanges = []creationExchange{{Input: "原文", Narrative: "正文", RunID: "r1"}, {Input: "追问", Narrative: "回应", RunID: "r2"}}
	snapshot := session.PlayerSnapshot()
	if len(snapshot.Messages) != 5 {
		t.Fatal("messages")
	}
	for i, m := range snapshot.Messages {
		if m.Seq != int64(i+1) {
			t.Fatal("sequence", m)
		}
	}
	snapshot.Positions["player"] = "island"
	if session.Status().Positions["player"] != "dock" {
		t.Fatal("view mutated positions")
	}
	session.recordCreation("npc:smith", "r1", "npc:smith", "statement", "private-secret")
	_, material := session.SuggestionContext()
	if material.Required == "" {
		t.Fatal("missing player material")
	}
}

func TestCreationRosterNamesRemotePeopleWithoutGrantingPresence(t *testing.T) {
	session := newCreationTestSession(t)
	material, _ := session.creationMaterial(CreationOptions{Input: "我请商人一起讨论。"})
	if !strings.Contains(material.Final, "npc:smith：工匠 @ dock") || !strings.Contains(material.Final, "npc:merchant：商人 @ inn") {
		t.Fatal("identity/location roster missing", material.Final)
	}
	remote := strings.Split(material.Final, "本轮异地人物（当前不能当面交谈）：")[1]
	if !strings.Contains(remote, "npc:merchant") || strings.Contains(remote, "npc:smith") {
		t.Fatal("presence not derived from positions", remote)
	}
}
