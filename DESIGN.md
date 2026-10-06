---
name: "World Is Agent"
description: "Warm paper, olive ink and translucent tools for an open Chinese story."
colors:
  accent: "#566447"
  accent-dark: "#424f36"
  paper: "#f5f1e8"
  paper-deep: "#e6ebdc"
  panel: "#fffdf6"
  soft-panel: "#fbfaf5b3"
  ink: "#30372e"
  muted: "#65695d"
  line: "#d7d9ca"
  success: "#246350"
  success-surface: "#edf7f1"
  error: "#914538"
  error-surface: "#fff0ef"
  selected-fill: "#e9ecdf"
  active-fill: "#f0f3e7"
  glass-solid: "#faf8f0"
  white: "white"
typography:
  display:
    fontFamily: "\"WIA Display\", -apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "36px"
    fontWeight: 700
    lineHeight: 1.4
    letterSpacing: "-.025em"
  cover:
    fontFamily: "\"WIA Display\", -apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "32px"
    fontWeight: 700
    lineHeight: 1.5
    letterSpacing: ".035em"
  headline:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "22px"
    fontWeight: 700
    lineHeight: 1.4
  title:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "17px"
    fontWeight: 700
    lineHeight: 1.4
  reading:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "19px"
    fontWeight: 400
    lineHeight: 1.95
  player:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.85
  body:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.65
  label:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.65
  control:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "14px"
    fontWeight: 650
    lineHeight: 1.65
  location:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "21px"
    fontWeight: 650
    lineHeight: 1.55
    letterSpacing: "-.018em"
  suggestion:
    fontFamily: "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.75
rounded:
  chip: "999px"
  control: "16px"
  item: "20px"
  surface: "12px"
  composer: "30px"
  overlay: "32px"
spacing:
  compact: "6px"
  small: "8px"
  group: "12px"
  inset: "16px"
  section: "24px"
  reading: "32px"
  wide: "40px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.white}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "10px 18px"
  button-primary-hover:
    backgroundColor: "{colors.accent-dark}"
  button-primary-active:
    backgroundColor: "#36452b"
  button-secondary:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.ink}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "10px 18px"
  button-danger:
    backgroundColor: "{colors.error}"
    textColor: "{colors.white}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "10px 18px"
  button-danger-hover:
    backgroundColor: "#852b2b"
  button-quiet:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    typography: "{typography.body}"
    padding: "7px 4px"
  field:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "11px 12px"
    width: "100%"
  recipient-chip:
    backgroundColor: "{colors.paper-deep}"
    textColor: "{colors.accent-dark}"
    typography: "{typography.label}"
    rounded: "{rounded.chip}"
    padding: "3px 9px"
  story-card:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.ink}"
    rounded: "{rounded.surface}"
  character-selected:
    backgroundColor: "{colors.selected-fill}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "12px 8px"
  information-tool:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    rounded: "{rounded.chip}"
    padding: "6px"
  composer:
    textColor: "{colors.ink}"
    rounded: "{rounded.composer}"
    padding: "18px 20px"
  information-panel:
    backgroundColor: "#faf8edcf"
    textColor: "{colors.ink}"
    rounded: "{rounded.overlay}"
    padding: "20px 24px 24px"
    width: "min(360px, 100%)"
  suggestion-action:
    backgroundColor: "{colors.soft-panel}"
    textColor: "{colors.ink}"
    typography: "{typography.suggestion}"
    rounded: "{rounded.item}"
    padding: "12px 16px"
    width: "100%"
---

# Design System: World Is Agent

## Overview

**Creative North Star: "Warm Paper Floating Tools"**

Warm ivory paper carries WIA's narrative, with dark olive ink and generous paragraph leading. Translucent rounded tools sit above that opaque reading ground. Their white edges, softened shadows and restrained olive states give world information and free-form actions a clear, tactile presence.

Locally served display lettering introduces stories; a platform sans carries narration, controls and factual context. Related controls stay compact while reading receives open space. The product name is World Is Agent, abbreviated WIA; player-facing copy is Chinese.

**Key Characteristics:**

- Opaque warm paper and dark olive narrative text.
- White-edged glass tools with soft, downward depth.
- An open reading measure, full-width suggestions and a rounded action composer.
- Locally served display lettering and a consistent platform sans for reading and operation.
- Visible focus, themed browser details and reduced-motion and reduced-transparency states.

## Colors

Warm, low-chroma paper and olive text form the reading environment. The frontmatter owns the palette; [the global stylesheet](frontend/src/style.css) carries the shared runtime custom properties.

### Primary

- **Olive Action** (`accent`): primary actions, title planes, focus outlines, selected-person boundaries and waiting indicators.
- **Deep Olive** (`accent-dark`): primary hover, links, active navigation and recipient text.

### Secondary

- **Forest Feedback** (`success` / `success-surface`): successful operations and connected status.
- **Brick Feedback** (`error` / `error-surface`): errors, destructive actions and recovery messages.

### Neutral

- **Warm Ivory** (`paper`): page canvas, opaque narrative ground and pending-run surface.
- **Soft Sage Paper** (`paper-deep`): quiet hover, recipient chips and destination controls.
- **Cream Panel** (`panel`): story entries, fields and regular dialogs.
- **Translucent Cream** (`soft-panel`): action suggestions.
- **Olive Ink** (`ink`): primary text.
- **Quiet Olive** (`muted`): secondary copy, metadata, placeholders and quiet controls.
- **Paper Rule** (`line`): dividers, fields and outlined factual entries.
- **Selected Sage** (`selected-fill`): player messages and selected people.
- **Active Sage** (`active-fill`): selected saves, options and current-location entries.
- **Solid Glass Fallback** (`glass-solid`): floating tools and information panels under reduced-transparency preferences or unsupported backdrop filtering.
- **White** (`white`): text on olive and destructive fills.

Browser selection uses dark olive on a pale sage highlight. Input carets use Olive Action. The transcript has a thin sage scrollbar with a transparent track. Dates, clocks, balances and operational metadata use tabular numerals.

### Named Rules

**The Olive Action Rule.** Olive identifies story title planes, primary actions and interactive states; success and error colors retain their explicit feedback meanings.

## Typography

**Display Font:** WIA Display, locally served at weight 700 with the platform UI stack as fallback.

**Body Font:** the platform sans stack in the frontmatter, shared by narration, player actions, navigation and factual information.

**Character:** broad display lettering gives story titles their own voice. The reader's larger text and generous leading provide narrative pace; compact operational typography keeps actions and data clear.

### Hierarchy

- **Display:** home and story-detail headings use the display role; narrow headings reduce to 27px at widths at or below 600px.
- **Cover:** cover title planes use the cover role. Story-detail covers grow to 42px; compact covers use 27px.
- **Headline:** section headings use the headline role, reducing to 20px at widths at or below 600px. Regular dialog titles use 24px and reduce to 22px at that width.
- **Title:** smaller headings and information sections use the title role.
- **Location:** current place names use the location role; widths at or below 720px use 20px.
- **Reading:** narration uses the reading role. Widths at or below 600px use 18px with the same leading.
- **Player:** submitted player text uses the player role in a right-aligned Selected Sage surface.
- **Body and Label:** body is the operational default. Supporting text uses the label role; timestamps use 11px. World-clock text uses 13px, reducing to 12px at widths at or below 720px.
- **Control:** primary, secondary and destructive button labels use the control role.
- **Suggestion:** action suggestion rows use the suggestion role, reducing to 13px at widths at or below 720px with the same leading.

Headings balance their wrapping. Story text preserves intentional line breaks and wraps long strings. [The display font](frontend/public/fonts/wia-display-bold.woff2) loads with `font-display: swap`; [its notice](frontend/public/fonts/README.md) and [OFL license](frontend/public/fonts/OFL.txt) stay with the asset.

### Named Rules

**The Reading Measure Rule.** Narration uses the reading role within the established 720px measure, preserving intentional line breaks and wrapping long strings.

## Layout

The general shell is centered and capped at 1280px with 32px side gutters. Story entries pair a 280px cover plane with a flexible description and action surface. Page sections have open vertical separation; labels and related controls use the compact spacing scale.

The play shell is capped at 1080px with 24px side gutters and follows the visible viewport through `--viewport-height`, initialized to `100dvh`. The masthead and dated location context remain above the independently scrolling transcript. A centered reading flow contains narrative, suggestions and the composer. The transcript preserves historical reading position; a separate floating control reaches the latest action area.

Desktop information controls occupy a vertical capsule beside the reading flow. One shared information panel switches between character, inventory, map and people. At narrow widths the capsule becomes a horizontal row above the dated scene, and the information panel becomes a bottom sheet.

### Responsive behavior

| Condition | Layout behavior |
| --- | --- |
| Width ≤1100px | General-shell gutters become 20px; secondary header actions move into More. The play shell retains its own width rule. |
| Width ≤859px | Story-detail cover planes are hidden; home cover columns become 220px. |
| Width ≤720px | Play-shell gutters become 16px. Information tools become a horizontal row; the composer uses a 27px radius and 15px by 17px padding. The information panel is a modal bottom sheet, capped at 480px wide. |
| Width ≤600px | General-shell gutters become 14px; the masthead uses the WIA mark; home entries stack cover and description. Settings options become one column and regular dialogs have 12px outer clearance. |
| Height ≤600px | Scene context compacts; the transcript retains a 90px minimum. Textarea height is capped at the smaller of 100px or 30% of the visible viewport; the information panel receives a height cap of viewport minus 104px. |

The transcript has contained overscroll, a stable scrollbar gutter and explicit reading-anchor control. At narrow widths its bottom padding uses the larger of 30px and the safe-area inset. The composer textarea scrolls internally beyond its viewport-relative height cap; the surrounding form remains part of the reading flow.

### Named Rules

**The Shared Reading Rule.** Narrative, suggestions and the action composer share the transcript scroll surface; the reader retains its anchor and has a separate return-to-latest action.

## Elevation & Depth

Opaque paper carries prose. Translucent warm surfaces, white edge highlights and diffuse downward shadows distinguish interactive layers. The information capsule and composer use a warm translucent gradient with backdrop blur (24px) and saturation (145%). The information panel uses its own translucent warm fill with backdrop blur (30px) and saturation (150%).

### Shadow Vocabulary

- **Glass tools:** `0 14px 42px #554d391c, 0 3px 8px #554d3910`, plus `inset 0 1px 0 #fff9`.
- **More menu:** `0 12px 35px #554d3924`.
- **Return to latest:** `0 5px 18px #554d391a`.
- **Regular dialog:** `0 24px 70px #3d402733`.
- **Information panel:** `0 24px 80px #3d402737, inset 0 1px 0 #ffffffe0`.
- **Selected information tab:** `0 2px 5px #4b543813`.

Regular dialogs use a translucent Olive Ink backdrop. Desktop information access uses a transparent nonmodal backdrop so the reading surface remains operable. Narrow information sheets dim the page and apply backdrop blur (5px). Unsupported backdrop filtering uses Solid Glass Fallback. Reduced-transparency preference uses that solid surface and removes panel and backdrop blur.

The information sheet enters over 220ms using the shared exponential ease-out, moving from a visible 12px downward offset and opacity 0.3. Reduced-motion preference removes that entrance, button transitions and the waiting pulse.

### Named Rules

**The Paper and Tool Rule.** Narrative text sits on opaque Warm Ivory; glass belongs to information controls, the action composer and the information panel.

## Shapes

Gently curved rectangles hold fields and bounded content; pill silhouettes identify compact tool groups and recipient controls. The frontmatter defines the reused radius scale. Story entries use the surface radius, controls and fields use the control radius, and saves, player messages and suggestions use the item radius. The composer and overlays use their larger dedicated radii.

Glass tools have a one-pixel white edge and inset highlight. Fields and factual entries use one-pixel Paper Rule boundaries. Pending-run surfaces use a dashed boundary. Person initials are content in circular 38px avatars; selected people fill the avatar with Olive Action. Drawn SVG icons use rounded caps and joins, with a stroke weight around 1.7px; the close icon uses 1.8px.

## Components

### Buttons

Clear, rounded actions share a minimum height of 44px. Primary buttons use Olive Action with White text; hover uses Deep Olive and pressed state uses the recorded primary-active variant. Secondary buttons use Cream Panel with a Paper Rule stroke; hover changes stroke and text to olive. Destructive buttons use Brick Feedback. Quiet text controls are transparent with a 40px minimum height.

Color and border changes take 180ms with the shared exponential ease-out. Disabled buttons use opacity 0.55. Keyboard focus uses a 2px Olive Action outline with 3px clearance. The composer submit button uses a pill radius and a 116px minimum width, reducing to 110px at widths at or below 600px.

### Inputs / Fields

Labeled fields use Cream Panel, Olive Ink and a Paper Rule stroke. Placeholders use Quiet Olive, and carets use Olive Action. Focus has a 2px accent outline with 1px clearance.

The composer field is transparent within the glass surface, with 16px type and line-height 1.75. It has an 83px minimum height, rising to 87px at narrow widths, and a maximum of the smaller of 180px or 30% of the visible viewport. Short viewports use the compact height rule in Layout.

### Navigation

The masthead carries a text WIA mark, quiet Chinese actions and connection state. Secondary actions move into More at compact widths; the play masthead uses that compact arrangement at every width.

Information tools pair drawn SVG icons with labels in a glass capsule. Desktop targets have a 62px minimum height; narrow targets have a 44px minimum and place icon beside text. Hover and expanded states use a warm light fill with Deep Olive text. Four pill-shaped information tabs share one panel; the selected tab has a cream fill, a soft shadow and stronger text.

### Cards / Containers

Story entries pair an olive title plane or story-provided image with a Cream Panel description and action surface. Images use `object-fit: cover` and their supplied alternative text.

Save entries have an outlined item surface and a separately bounded deletion action. Active saves use an olive boundary and Active Sage fill. Factual location entries use compact outlines and mark the current place with the same active treatment.

### People and recipient chips

Person rows use a circular initial avatar, name and role. Selected rows use an olive boundary, Selected Sage fill and filled avatar. The composer recipient reflects the selected person. Its removable pill supplements the destination select; public or private expression stays in the player's action text.

### Suggestions and waiting

Suggestions are full-width, vertically stacked action rows with Translucent Cream fill, soft item corners and a drawn right-arrow SVG. Their text uses the suggestion role and its narrow-screen size. Hover uses Soft Sage Paper. Long text wraps within the row. Existing draft text disables selection at opacity 0.55; unavailable suggestions retain visible Chinese status and recovery copy.

A pending run uses a dashed paper surface and one olive dot. The dot pulses over 1.6s with ease-in-out, reaching opacity 0.35 halfway through. Errors use a red-tinted surface with an explicit recovery action.

### Composer

The rounded glass composer groups the action label, recipient select, scope hint, draft and save state. Its footer places the keyboard hint opposite the pill submit control. The form follows suggestions in the shared transcript so historical reading and the action area have one scroll position.

### Dialogs and the information panel

Regular dialogs center an opaque Cream Panel surface above a dimmed page, cap their width at 660px and scroll internally.

The information panel is a rounded glass surface. Desktop access is nonmodal and right-aligned with a 360px width cap; reading controls remain operable. Widths at or below 720px use a modal bottom sheet with a 480px width cap, dimming and backdrop blur. Modal mode traps focus and blocks outside interaction. Escape and the visible close control dismiss the panel; closing restores focus to the invoking control or its visible fallback without scrolling. Busy state protects ongoing operations, and destructive confirmation uses explicit dismissal.

## Do's and Don'ts

### Do:

- **Do** use the global palette and font custom properties for shared visual roles.
- **Do** keep WIA Display with its locally served font asset, notice and license.
- **Do** preserve opaque prose, its established measure and generous leading.
- **Do** keep narrative, suggestions and the composer in the shared reading scroll surface.
- **Do** preserve desktop nonmodal information access and the narrow-screen modal bottom sheet.
- **Do** carry recipient selection consistently between person rows and the composer.
- **Do** retain Chinese action labels, drawn SVG icons, visible focus and accessible material preferences.

### Don't:

- **Don't** replace the locally served display face with a platform display face.
- **Don't** place translucent material beneath narrative paragraphs.
- **Don't** add permanent information columns or card frames around prose.
- **Don't** use feedback colors without their established success, error or destructive-action meaning.
- **Don't** use text glyphs as the reusable icon vocabulary.
