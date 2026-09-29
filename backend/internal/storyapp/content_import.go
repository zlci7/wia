package storyapp

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"gameagent/backend/internal/wire"
	wiaworld "gameagent/backend/internal/world"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Imports are previews first: a detected format is mapped onto a draft, the author
// sees what was understood, unsupported and truncated, and only then confirms. The
// preview draft is an ordinary draft with status import_preview, so there is one
// draft identity and no second temporary object.
const (
	importTextLimit  = 1 * 1024 * 1024
	importZIPLimit   = 8 * 1024 * 1024
	importUnzipLimit = 16 * 1024 * 1024
	importZipEntries = 128
	importZipDepth   = 8
	importSourceName = "import-source"
	// Imported cards carry no world structure, so a draft starts with one usable place
	// and time that the author can rename.
	defaultImportLocation     = "main"
	defaultImportLocationName = "主要场景"
	defaultImportClock        = "第 1 日 09:00"
	defaultImportGameplay     = "按人物卡片的设定与这名人物互动，并决定关系走向。"
)

type ImportMapping struct {
	Field      string `json:"field"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Confidence string `json:"confidence"`
	Note       string `json:"note,omitempty"`
}

type ImportReport struct {
	Format       string          `json:"format"`
	FormatDetail string          `json:"format_detail"`
	Summary      string          `json:"summary"`
	Mappings     []ImportMapping `json:"mappings"`
	Unsupported  []string        `json:"unsupported"`
	NeedsConfirm []string        `json:"needs_confirmation"`
	SourceBytes  int             `json:"source_bytes"`
}

// ImportPreview is the result of one import attempt: the report plus the draft the
// author may edit and later confirm.
type ImportPreview struct {
	DraftID string       `json:"draft_id"`
	Version int64        `json:"version"`
	Project string       `json:"project_id"`
	Report  ImportReport `json:"report"`
}

// PreviewContentImport detects the format, maps it onto a draft and stores the
// original bytes as a non-executed source attachment.
func (a *App) PreviewContentImport(ctx context.Context, projectID, fileName string, body []byte) (ImportPreview, error) {
	project, _, err := a.ReadContentProject(ctx, projectID)
	if err != nil {
		return ImportPreview{}, err
	}
	if len(body) == 0 {
		return ImportPreview{}, fmt.Errorf("%w: empty upload", ErrContentInvalid)
	}
	draft, report, assets, err := a.buildImportDraft(ctx, project, fileName, body)
	if err != nil {
		return ImportPreview{}, err
	}
	// A preview draft owns the images its source used, so confirming and publishing it
	// has everything it references.
	if err = a.copyPackAssets(ctx, draft.DraftID, assets); err != nil {
		return ImportPreview{}, err
	}
	staged := a.importSourcePath(draft.DraftID, fileName)
	if err := writeImportSource(staged, body); err != nil {
		return ImportPreview{}, err
	}
	draft.Status = draftStatusPreview
	payload, err := json.Marshal(draft.Payload)
	if err != nil {
		return ImportPreview{}, err
	}
	source, err := json.Marshal(map[string]any{"file": safeFileName(fileName), "format": report.Format, "bytes": len(body), "staged": staged})
	if err != nil {
		return ImportPreview{}, err
	}
	if _, err = a.appDB.ExecContext(ctx, `INSERT INTO content_drafts(user_id,draft_id,project_id,base_revision,version,status,payload_json,source_json,created_at,updated_at)
		VALUES(?,?,?,'',1,?,?,?,?,?)`,
		a.userID, draft.DraftID, project.ProjectID, draftStatusPreview, string(payload), string(source), wire.NowText(), wire.NowText()); err != nil {
		return ImportPreview{}, err
	}
	return ImportPreview{DraftID: draft.DraftID, Version: 1, Project: project.ProjectID, Report: report}, nil
}

// ConfirmContentImport turns a preview into an editable draft. Confirmation is a
// versioned, idempotent transition: the same confirmation returns the same result
// instead of failing, a stale one conflicts, and anything already past the preview
// state is refused.
func (a *App) ConfirmContentImport(ctx context.Context, draftID, requestKey string, expectedVersion int64) (ContentDraft, error) {
	draft, err := a.ReadContentDraft(ctx, draftID)
	if err != nil {
		return ContentDraft{}, err
	}
	if draft.Status == draftStatusEditing {
		// Already confirmed. The same confirmation is satisfied; a different request key
		// is not a second confirmation of a different preview.
		if confirmation, found, err := a.readDraftConfirmation(ctx, draft.DraftID); err != nil {
			return ContentDraft{}, err
		} else if found {
			if requestKey != "" && confirmation != requestKey {
				return ContentDraft{}, ErrIdempotencyConflict
			}
			return draft, nil
		}
		return draft, nil
	}
	if draft.Status != draftStatusPreview {
		return ContentDraft{}, fmt.Errorf("%w: this draft is not an import preview", ErrContentInvalid)
	}
	if expectedVersion > 0 && draft.Version != expectedVersion {
		return ContentDraft{}, ErrVersionConflict
	}
	if requestKey != "" {
		if prior, found, err := a.readDraftConfirmation(ctx, draft.DraftID); err != nil {
			return ContentDraft{}, err
		} else if found && prior != requestKey {
			return ContentDraft{}, ErrIdempotencyConflict
		}
	}
	result, err := a.appDB.ExecContext(ctx, `UPDATE content_drafts SET status=?,version=version+1,confirmation_key=?,updated_at=? WHERE user_id=? AND draft_id=? AND status=? AND version=?`,
		draftStatusEditing, requestKey, wire.NowText(), a.userID, draft.DraftID, draftStatusPreview, draft.Version)
	if err != nil {
		return ContentDraft{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ContentDraft{}, err
	}
	if affected == 0 {
		// Someone else moved the draft between the read and the write.
		return ContentDraft{}, ErrVersionConflict
	}
	return a.ReadContentDraft(ctx, draft.DraftID)
}

// A draft records which confirmation request produced it, so a repeat is answered
// instead of applied twice.
func (a *App) readDraftConfirmation(ctx context.Context, draftID string) (string, bool, error) {
	var key string
	err := a.appDB.QueryRowContext(ctx, `SELECT confirmation_key FROM content_drafts WHERE user_id=? AND draft_id=?`, a.userID, draftID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return key, key != "", nil
}

func (a *App) writeDraftConfirmation(ctx context.Context, draftID, requestKey string) error {
	_, err := a.appDB.ExecContext(ctx, `UPDATE content_drafts SET confirmation_key=? WHERE user_id=? AND draft_id=?`, requestKey, a.userID, draftID)
	return err
}

// ImportUploadLimit is the largest upload the import entry point accepts. Each format
// still applies its own smaller limit inside the service.
func ImportUploadLimit() int64 {
	if importZIPLimit > importTextLimit {
		return importZIPLimit
	}
	return importTextLimit
}

// ImportContent validates the declared file name and bytes, keeping the size decision
// in one place per format.
// buildImportDraft picks the format from the bytes and the declared name together: a
// card can arrive with a byte-order mark or an ambiguous first byte, and the extension
// is what the author chose.
func (a *App) buildImportDraft(ctx context.Context, project ContentProject, fileName string, body []byte) (ContentDraft, ImportReport, map[string][]byte, error) {
	lower := strings.ToLower(safeFileName(fileName))
	switch {
	case isZIP(body) || strings.HasSuffix(lower, ".zip"):
		return a.importWIAPackage(ctx, project, body)
	case looksLikeJSON(body) || strings.HasSuffix(lower, ".json"):
		return a.importCharacterCard(project, fileName, body)
	default:
		return a.importPlainText(project, fileName, body)
	}
}

// ---- plain text and Markdown -------------------------------------------------

func (a *App) importPlainText(project ContentProject, fileName string, body []byte) (ContentDraft, ImportReport, map[string][]byte, error) {
	if len(body) > importTextLimit {
		return ContentDraft{}, ImportReport{}, nil, fmt.Errorf("%w: text import exceeds the size limit", ErrContentInvalid)
	}
	text := strings.ToValidUTF8(string(body), "")
	if strings.TrimSpace(text) == "" {
		return ContentDraft{}, ImportReport{}, nil, fmt.Errorf("%w: the file has no readable text", ErrContentInvalid)
	}
	name := safeFileName(fileName)
	format, detail := "text", "plain text"
	if strings.HasSuffix(strings.ToLower(name), ".md") || strings.HasSuffix(strings.ToLower(name), ".markdown") {
		format, detail = "markdown", "markdown is treated as text; no HTML or remote resource is loaded"
	}
	payload := newDraftPayload(project)
	payload.Background = text
	payload.Title = project.Title
	payload.Description = firstLine(text, 400)
	report := ImportReport{
		Format: format, FormatDetail: detail, SourceBytes: len(body),
		Summary: "文本已作为公开背景候选；确认前可以改派到其他字段。",
		Mappings: []ImportMapping{
			{Field: "background", Source: name, Target: "公开背景", Confidence: "high"},
			{Field: "description", Source: "first line", Target: "简介", Confidence: "low", Note: "取首行，可改写"},
		},
		Unsupported:  []string{"HTML、脚本、远程图片与链接命令不会被执行"},
		NeedsConfirm: []string{"背景之外的内容只作候选，需要作者确认"},
	}
	return ContentDraft{ContentDraftSummary: ContentDraftSummary{DraftID: wire.NewID("draft"), ProjectID: project.ProjectID, Version: 1, Status: draftStatusPreview, UpdatedAt: wire.NowText()}, Payload: payload}, report, nil, nil
}

// ---- WIA package -------------------------------------------------------------

func (a *App) importWIAPackage(ctx context.Context, project ContentProject, body []byte) (ContentDraft, ImportReport, map[string][]byte, error) {
	if len(body) > importZIPLimit {
		return ContentDraft{}, ImportReport{}, nil, fmt.Errorf("%w: package exceeds the size limit", ErrContentInvalid)
	}
	files, err := readPackageZip(body)
	if err != nil {
		return ContentDraft{}, ImportReport{}, nil, err
	}
	if _, ok := files["story.json"]; !ok {
		return ContentDraft{}, ImportReport{}, nil, fmt.Errorf("%w: story.json is missing", ErrContentInvalid)
	}
	staging, err := os.MkdirTemp(a.contentRoot(), "import-")
	if err != nil {
		return ContentDraft{}, ImportReport{}, nil, err
	}
	defer os.RemoveAll(staging)
	for name, content := range files {
		target := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return ContentDraft{}, ImportReport{}, nil, err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return ContentDraft{}, ImportReport{}, nil, err
		}
	}
	pack, err := loadPack(staging)
	if err != nil {
		return ContentDraft{}, ImportReport{}, nil, fmt.Errorf("%w: %s", ErrContentInvalid, err.Error())
	}
	payload := newDraftPayload(project)
	copied, err := a.draftPayloadFromLoadedPack(pack, project.GameID)
	if err != nil {
		return ContentDraft{}, ImportReport{}, nil, err
	}
	// The imported identity is never taken over: the project keeps its own game_id.
	payload = copied
	payload.GameID = project.GameID
	report := ImportReport{
		Format: "wia_pack", FormatDetail: "WIA story package", SourceBytes: len(body),
		Summary: fmt.Sprintf("已读取 %d 个文件，包含 %d 名重要人物与 %d 个地点。", len(files), len(payload.NPCs), len(payload.Locations)),
		Mappings: []ImportMapping{
			{Field: "story", Source: "story.json", Target: "剧本基本信息与世界设定", Confidence: "high"},
			{Field: "npcs", Source: "npcs/*.json", Target: "人物", Confidence: "high"},
			{Field: "bystanders", Source: "story.json", Target: "路人", Confidence: "high"},
			{Field: "assets", Source: "assets/*", Target: "资源引用", Confidence: "high"},
		},
		Unsupported:  []string{"附件不执行；包内脚本或指令不会被读取"},
		NeedsConfirm: []string{"来源剧本的游戏标识不会沿用，确认后按本项目的标识保存"},
	}
	if len(pack.Definition.BystanderRefs) > 0 {
		report.Mappings = append(report.Mappings, ImportMapping{Field: "bystanders", Source: "story.json", Target: "路人稳定身份", Confidence: "high"})
	}
	_ = ctx
	return ContentDraft{ContentDraftSummary: ContentDraftSummary{DraftID: wire.NewID("draft"), ProjectID: project.ProjectID, Version: 1, Status: draftStatusPreview, UpdatedAt: wire.NowText()}, Payload: payload}, report, pack.Assets, nil
}

// readPackageZip reads a package archive with hard limits and no path escapes.
func readPackageZip(body []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("%w: not a readable zip archive", ErrContentInvalid)
	}
	if len(reader.File) > importZipEntries {
		return nil, fmt.Errorf("%w: too many entries", ErrContentInvalid)
	}
	files := map[string][]byte{}
	total := 0
	for _, entry := range reader.File {
		name := path.Clean(strings.ReplaceAll(entry.Name, "\\", "/"))
		if name == "." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || path.IsAbs(name) {
			return nil, fmt.Errorf("%w: entry path escapes the package", ErrContentInvalid)
		}
		if strings.Count(name, "/") >= importZipDepth {
			return nil, fmt.Errorf("%w: entry nesting is too deep", ErrContentInvalid)
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 || entry.Mode()&os.ModeDevice != 0 || !entry.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: only regular files are accepted", ErrContentInvalid)
		}
		if _, exists := files[name]; exists {
			return nil, fmt.Errorf("%w: duplicate entry %s", ErrContentInvalid, name)
		}
		file, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: entry could not be read", ErrContentInvalid)
		}
		content, err := io.ReadAll(io.LimitReader(file, importUnzipLimit+1))
		file.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: entry could not be read", ErrContentInvalid)
		}
		total += len(content)
		if total > importUnzipLimit {
			return nil, fmt.Errorf("%w: unpacked content exceeds the size limit", ErrContentInvalid)
		}
		files[name] = content
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: the archive has no files", ErrContentInvalid)
	}
	return files, nil
}

// ---- Character Card V2 -------------------------------------------------------

func (a *App) importCharacterCard(project ContentProject, fileName string, body []byte) (ContentDraft, ImportReport, map[string][]byte, error) {
	if len(body) > importTextLimit {
		return ContentDraft{}, ImportReport{}, nil, fmt.Errorf("%w: card exceeds the size limit", ErrContentInvalid)
	}
	var card struct {
		Spec        string `json:"spec"`
		SpecVersion string `json:"spec_version"`
		Data        struct {
			Name               string   `json:"name"`
			Description        string   `json:"description"`
			Personality        string   `json:"personality"`
			Scenario           string   `json:"scenario"`
			FirstMes           string   `json:"first_mes"`
			MesExample         string   `json:"mes_example"`
			CreatorNotes       string   `json:"creator_notes"`
			AlternateGreetings []string `json:"alternate_greetings"`
			Tags               []string `json:"tags"`
			Creator            string   `json:"creator"`
			CharacterVersion   string   `json:"character_version"`
			SystemPrompt       string   `json:"system_prompt"`
			PostHistoryInstr   string   `json:"post_history_instructions"`
			CharacterBook      any      `json:"character_book"`
			Extensions         any      `json:"extensions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(bytes.TrimSpace(body), []byte{0xEF, 0xBB, 0xBF}), &card); err != nil {
		return ContentDraft{}, ImportReport{}, nil, fmt.Errorf("%w: the JSON could not be read", ErrContentInvalid)
	}
	if card.Spec != "chara_card_v2" || card.SpecVersion != "2.0" {
		return ContentDraft{}, ImportReport{}, nil, fmt.Errorf("%w: only chara_card_v2 version 2.0 text cards are supported", ErrContentInvalid)
	}
	if strings.TrimSpace(card.Data.Name) == "" {
		return ContentDraft{}, ImportReport{}, nil, fmt.Errorf("%w: the card has no character name", ErrContentInvalid)
	}
	payload := newDraftPayload(project)
	replacements := map[string]string{"{{char}}": card.Data.Name, "{{user}}": payload.Player.Name}
	profile := joinCardText(card.Data.Description, card.Data.Personality)
	profile = replaceCardMacros(profile, replacements)
	examples := cardExamples(card.Data.MesExample, replacements)
	name := card.Data.Name
	npc := ContentDraftNPC{
		DefinitionID: importDefinitionID(name), Revision: "v1", EntityID: "npc:" + importDefinitionID(name),
		Name: name, Role: "导入人物",
		Profile:          profile,
		Knowledge:        replaceCardMacros(card.Data.Scenario, replacements),
		InitialLocation:  "",
		SpeakingExamples: examples,
	}
	if card.Data.FirstMes != "" {
		npc.SpeakingExamples = append(npc.SpeakingExamples, replaceCardMacros(card.Data.FirstMes, replacements))
	}
	payload.NPCs = []ContentDraftNPC{npc}
	payload.Title = project.Title
	payload.Description = firstLine(profile, 400)
	// A card has no world structure, so the draft starts with the minimum a package
	// needs: one usable place, a starting time, the greeting as the opening candidate
	// and a neutral goal line the author replaces.
	payload.Locations = []PackLocation{{ID: defaultImportLocation, Name: defaultImportLocationName, Connections: []string{}}}
	payload.InitialLocation = defaultImportLocation
	npc.InitialLocation = defaultImportLocation
	payload.NPCs = []ContentDraftNPC{npc}
	if payload.Clock == "" {
		payload.Clock = defaultImportClock
	}
	if payload.Gameplay == "" {
		payload.Gameplay = defaultImportGameplay
	}
	if greeting := replaceCardMacros(strings.TrimSpace(card.Data.FirstMes), replacements); greeting != "" {
		payload.Opening = truncateRunes(greeting, 8000)
	}
	report := ImportReport{
		Format: "ccv2", FormatDetail: "Character Card V2 text card", SourceBytes: len(body),
		Summary: "卡片已映射为一名重要人物候选；情境与开场需要确认后才能作为世界背景或开场。",
		Mappings: []ImportMapping{
			{Field: "data.name", Source: safeFileName(fileName), Target: "人物姓名", Confidence: "high"},
			{Field: "description + personality", Source: "card", Target: "人物资料", Confidence: "high"},
			{Field: "scenario", Source: "card", Target: "初始知情候选", Confidence: "low", Note: "需要确认；卡片缺少人物知情范围"},
			{Field: "first_mes", Source: "card", Target: "说话示例候选", Confidence: "low"},
			{Field: "mes_example", Source: "card", Target: "说话示例", Confidence: "medium"},
			{Field: "creator_notes", Source: "card", Target: "作者备注（不进入模型上下文）", Confidence: "high"},
			{Field: "tags / creator / character_version", Source: "card", Target: "来源元数据", Confidence: "high"},
		},
		Unsupported:  []string{},
		NeedsConfirm: []string{"情境与开场只作候选，需作者确认", "卡片没有人物知情范围，不能声称已还原秘密边界"},
	}
	if strings.TrimSpace(card.Data.SystemPrompt) != "" || strings.TrimSpace(card.Data.PostHistoryInstr) != "" {
		report.Unsupported = append(report.Unsupported, "system_prompt 与 post_history_instructions 作为行为指令不会被执行")
	}
	if card.Data.CharacterBook != nil {
		report.Unsupported = append(report.Unsupported, "character_book 保留在来源附件，不自动转换为世界知识")
	}
	if card.Data.Extensions != nil {
		report.Unsupported = append(report.Unsupported, "extensions 原样保留，不执行也不解释")
	}
	if len(card.Data.AlternateGreetings) > 0 {
		report.NeedsConfirm = append(report.NeedsConfirm, fmt.Sprintf("%d 条替代开场只作候选，不自动采用", len(card.Data.AlternateGreetings)))
	}
	if unsupported := unsupportedMacros(map[string]string{"{{char}}": card.Data.Name, "{{user}}": payload.Player.Name}, []byte(strings.Join(append([]string{profile, npc.Knowledge}, npc.SpeakingExamples...), "\n"))); len(unsupported) > 0 {
		report.Unsupported = append(report.Unsupported, "未支持的宏保持原文："+strings.Join(unsupported, ", "))
	}
	return ContentDraft{ContentDraftSummary: ContentDraftSummary{DraftID: wire.NewID("draft"), ProjectID: project.ProjectID, Version: 1, Status: draftStatusPreview, UpdatedAt: wire.NowText()}, Payload: payload}, report, nil, nil
}

// ---- export ------------------------------------------------------------------

// ExportContentRevision rebuilds a canonical package archive for one published
// revision. It never packs an arbitrary directory and never includes worlds,
// credentials, sessions, logs or usage.
func (a *App) ExportContentRevision(ctx context.Context, gameID, revision string) ([]byte, string, error) {
	if !packID.MatchString(gameID) || !packID.MatchString(revision) {
		return nil, "", ErrInvalidRequest
	}
	var storedPath, digest string
	err := a.appDB.QueryRowContext(ctx, `SELECT path,digest FROM content_revisions WHERE user_id=? AND game_id=? AND revision=? AND status=?`, a.userID, gameID, revision, publishReady).Scan(&storedPath, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrContentNotFound
	}
	if err != nil {
		return nil, "", err
	}
	pack, err := loadPack(storedPath)
	if err != nil {
		return nil, "", fmt.Errorf("%w: the published revision is unreadable", ErrContentInvalid)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	write := func(name string, body []byte) error {
		entry, err := writer.Create(name)
		if err != nil {
			return err
		}
		_, err = entry.Write(body)
		return err
	}
	if err := write("story.json", mustJSON(pack.Story)); err != nil {
		return nil, "", err
	}
	for name, body := range pack.NPCFiles {
		if err := write(name, body); err != nil {
			return nil, "", err
		}
	}
	for name, body := range pack.Assets {
		if err := write(name, body); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), gameID + "-" + revision + ".wia-story.zip", nil
}

func newDraftPayload(project ContentProject) ContentDraftPayload {
	return ContentDraftPayload{
		SchemaVersion: packSchemaV2, GameID: project.GameID, Mode: "open", Title: project.Title,
		NPCs: []ContentDraftNPC{}, Locations: []PackLocation{}, Bystanders: []PackBystander{},
		// Imported cards address the player as {{user}}; the default lead keeps that
		// replacement previewable and the draft publishable.
		Player: PlayerDefaults{Name: defaultPlayerName, Profile: defaultPlayerProfile, Editable: true},
	}
}

func (a *App) importSourcePath(draftID, fileName string) string {
	return filepath.Join(a.contentRoot(), "imports", draftID+"-"+safeFileName(fileName))
}

func writeImportSource(target string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, body, 0o644)
}

func isZIP(body []byte) bool {
	return len(body) > 4 && body[0] == 'P' && body[1] == 'K' && (body[2] == 3 || body[2] == 5 || body[2] == 7)
}

func looksLikeJSON(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	// A byte-order mark would otherwise hide the opening brace.
	trimmed = bytes.TrimPrefix(trimmed, []byte{0xEF, 0xBB, 0xBF})
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}

func safeFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == "/" {
		return importSourceName
	}
	var builder strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		case r > 127:
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	out := builder.String()
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

func firstLine(text string, limit int) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return truncateRunes(trimmed, limit)
		}
	}
	return ""
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func joinCardText(parts ...string) string {
	chunks := []string{}
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			chunks = append(chunks, strings.TrimSpace(part))
		}
	}
	return strings.Join(chunks, "\n\n")
}

func replaceCardMacros(text string, replacements map[string]string) string {
	for macro, value := range replacements {
		text = strings.ReplaceAll(text, macro, value)
	}
	return text
}

func cardExamples(mesExample string, replacements map[string]string) []string {
	out := []string{}
	for _, block := range strings.Split(mesExample, "<START>") {
		for _, line := range strings.Split(block, "\n") {
			trimmed := stripSpeakerPrefix(strings.TrimSpace(replaceCardMacros(line, replacements)))
			if trimmed == "" {
				continue
			}
			out = append(out, truncateRunes(trimmed, 500))
		}
	}
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

// cardExamples and the greeting may carry the card's own `Name:` speaker prefix.
func stripSpeakerPrefix(line string) string {
	index := strings.Index(line, ":")
	if index <= 0 || index > 24 {
		return line
	}
	return strings.TrimSpace(line[index+1:])
}

func importDefinitionID(name string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r > 127:
			builder.WriteString(fmt.Sprintf("%x", r))
		}
	}
	out := builder.String()
	if out == "" {
		out = "imported"
	}
	if len(out) > 60 {
		out = out[:60]
	}
	if out[0] < 'a' || out[0] > 'z' {
		out = "npc" + out
	}
	return out
}

func unsupportedMacros(replacements map[string]string, body []byte) []string {
	text := replaceCardMacros(string(body), replacements)
	seen := map[string]bool{}
	out := []string{}
	for {
		start := strings.Index(text, "{{")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], "}}")
		if end < 0 {
			break
		}
		macro := text[start : start+end+2]
		text = text[start+end+2:]
		if !seen[macro] && len(out) < 8 {
			seen[macro] = true
			out = append(out, macro)
		}
	}
	return out
}

func mustJSON(value any) []byte {
	body, _ := json.Marshal(value)
	return body
}

// draftNPCRefs projects characters into the asset-reference shape used by
// referencedAssets, so package loading can collect the images a package uses.
func draftNPCRefs(characters []wiaworld.Character) []ContentDraftNPC {
	out := make([]ContentDraftNPC, 0, len(characters))
	for _, character := range characters {
		out = append(out, ContentDraftNPC{Avatar: character.Avatar})
	}
	return out
}
