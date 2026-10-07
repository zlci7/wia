---
version: 1
slug: "frontend-src-app-vue"
primary_target: "frontend/src/App.vue"
related_targets: ["frontend/src/style.css", "frontend/src/components/AppDialog.vue", "frontend/src/components/SuggestionPanel.vue"]
---

# Player surface

Mode: Read. The player reads an open story, writes freely, and checks their actual situation on demand. Platform: web. Path: code-led, based on approved A “Warm Paper Floating Tools”.

## Direction contract

THESIS: Warm paper carries the story; translucent, rounded tools provide immediate access to world information.

OWN-WORLD: Warm ivory #f5f1e8, dark olive ink #30372e, olive action #566447, platform sans typography. Open prose has a 720px measure, 19px type and 1.95 line-height; narrow screens use 18px. Glass controls carry a white edge, blur and soft offset depth. The story stays on an opaque ground.

STORY: Enter a dated scene, follow independently acting people, write a free action, inspect known data and return to the same reading position.

FORM: Approved A code prototype, grounded structure 2 from the warm Apple reading study (seed 4027323d). The user selected A for formal integration on 2026-10-06.

FIRST VIEWPORT: A compact WIA masthead, world date and location frame the open reading column. Four tools occupy a vertical glass capsule on desktop and a horizontal capsule on narrow screens. Story, three full-width suggestions and the rounded composer share one scroll surface. Saved history retains its anchor. “Back to latest” reaches the action area.

SIGNATURE INTERACTION: One shared information panel switches between character, inventory, map and people. Desktop viewing is nonmodal; screens at or below 720px use a modal bottom sheet. The information panel stays open until its close button is selected; outside clicks, Escape and conversation partner selection preserve it. Closing restores focus without scrolling. Draft, conversation partner and game time persist. Choosing a suggestion fills an editable empty draft; submitting uses the existing run and recovery mechanism.

STATES: Reading, suggestions ready/disabled/unavailable, generation, cancellation, failed run, unknown submission, historic reading, information open, story ended and missing model connection. Model and story settings, saves and retrospective corrections retain their existing workflows.

STORY ENTRY: Script-authored starting identities use rounded, stacked radio rows with one visible selection and the selected background. Continuing preserves the current session; starting with a selected identity creates a fresh session. Creation failure preserves the current story and draft.

COMPOSER: Deep thinking is a visible checkbox, enabled for a new session. Thinking, voluntary plot advancement and transport are separate controls. A pending request freezes their values. The story keeps the same open prose node during streaming and acceptance.

QUALITY BAR: Match the approved rounded paper and glass language; no permanent information columns or cards around prose. Keep real server-projected date, cash, ownership and knowledge. Desktop, narrow and short-screen controls fit without horizontal overflow. Focus, Escape, draft and scroll recovery are verified against the real client with isolated controlled-model fixture data.

FINISH: Batched desktop/mobile/short-screen evidence, independent finish review and canonical design documentation. Real-model narrative quality and physical phone keyboard behavior remain separate verification boundaries.
