---
name: World Is Agent
description: A contemporary novel jacket and a quiet Chinese reading room.
colors:
  accent: "#354fc4"
  accent-dark: "#253b9a"
  paper: "#f3f5fb"
  paper-deep: "#e7ecf8"
  panel: "#fff"
  ink: "#202b43"
  muted: "#596781"
  line: "#d8dfed"
  success: "#246350"
  success-surface: "#edf7f1"
  error: "#a03838"
  error-surface: "#fff0ef"
typography:
  display:
    fontFamily: '"WIA Display", "Microsoft YaHei", "PingFang SC", "Segoe UI", sans-serif'
    fontSize: "36px"
    fontWeight: 700
    lineHeight: 1.4
    letterSpacing: "-.025em"
  cover:
    fontFamily: '"WIA Display", "Microsoft YaHei", "PingFang SC", "Segoe UI", sans-serif'
    fontSize: "32px"
    fontWeight: 700
    lineHeight: 1.5
    letterSpacing: ".035em"
  headline:
    fontFamily: '"Microsoft YaHei", "PingFang SC", "Segoe UI", sans-serif'
    fontSize: "22px"
    fontWeight: 700
    lineHeight: 1.4
  title:
    fontFamily: '"Microsoft YaHei", "PingFang SC", "Segoe UI", sans-serif'
    fontSize: "17px"
    fontWeight: 700
    lineHeight: 1.4
  reading:
    fontFamily: '"Noto Serif SC", "Source Han Serif SC", "Songti SC", "SimSun", serif'
    fontSize: "18px"
    fontWeight: 400
    lineHeight: 2
  player:
    fontFamily: '"Microsoft YaHei", "PingFang SC", "Segoe UI", sans-serif'
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.85
  body:
    fontFamily: '"Microsoft YaHei", "PingFang SC", "Segoe UI", sans-serif'
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.65
  label:
    fontFamily: '"Microsoft YaHei", "PingFang SC", "Segoe UI", sans-serif'
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.65
  control:
    fontFamily: '"Microsoft YaHei", "PingFang SC", "Segoe UI", sans-serif'
    fontSize: "14px"
    fontWeight: 650
    lineHeight: 1.65
rounded:
  chip: "4px"
  control: "6px"
  item: "8px"
  surface: "12px"
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
    textColor: "{colors.panel}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "10px 18px"
  button-primary-hover:
    backgroundColor: "{colors.accent-dark}"
  button-secondary:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.ink}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "10px 18px"
  button-danger:
    backgroundColor: "{colors.error}"
    textColor: "{colors.panel}"
    typography: "{typography.control}"
    rounded: "{rounded.control}"
    padding: "10px 18px"
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
    backgroundColor: "#e5ebff"
    textColor: "{colors.accent-dark}"
    typography: "{typography.label}"
    rounded: "{rounded.chip}"
    padding: "3px 8px"
  story-card:
    backgroundColor: "{colors.panel}"
    textColor: "{colors.ink}"
    rounded: "{rounded.surface}"
  character-selected:
    backgroundColor: "#e8edff"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "12px 8px"
---

# Design System: World Is Agent

## Overview

**Creative North Star: "Contemporary Novel Jacket"**

WIA pairs saturated title planes with cool white reading pages and dark blue ink. Its visual identity comes from the relationship between the cover, the page and the typography. Chinese story text has the generous leading of a book; navigation, factual context and controls use a familiar platform sans.

Surfaces are quiet and flat. Fine rules separate reading, writing and factual context; softened rectangular controls make actions legible without competing with the story. Player-facing language is Chinese. The name is World Is Agent, abbreviated WIA.

**Key Characteristics:**

- Ultramarine cover fields and decisive primary actions.
- White reading surfaces against cool blue-gray paper.
- Locally served display lettering, platform Chinese book text and neutral operational type.
- Wide paragraph spacing, restrained borders and visible keyboard focus.
- Compact state transitions and a single waiting pulse.

## Colors

The palette places clear ultramarine on a cool, low-chroma reading environment. The frontmatter is the normative palette; CSS custom properties in [the global stylesheet](frontend/src/style.css) carry these values at runtime.

### Primary

- **Ultramarine** (`accent`): story title planes, the WIA mark, primary actions, selected-person borders, focus outlines and waiting indicators.
- **Deep Ultramarine** (`accent-dark`): primary hover, links, current navigation and selected-recipient text.

### Secondary

- **Forest Feedback** (`success` / `success-surface`): connection and successful-operation feedback.
- **Brick Feedback** (`error` / `error-surface`): errors, destructive actions and recovery messages.

### Neutral

- **Cool Paper** (`paper`): the page canvas, input canvas in the composer and temporary run surfaces.
- **Folded Paper** (`paper-deep`): quiet hover surfaces.
- **Reading White** (`panel`): reading, story, form and dialog surfaces.
- **Blue Ink** (`ink`): primary text.
- **Slate Ink** (`muted`): secondary descriptions, labels, timestamps and quiet controls.
- **Paper Rule** (`line`): dividers, field strokes and unselected save boundaries.

Selection uses dark blue text on a pale blue highlight. The caret uses Ultramarine, and scrollbars use a subdued blue-gray thumb on a transparent track. Selected people, recipient chips, player messages and selected options use their component-specific pale blue fills.

### Named Rules

**The Cover and Action Rule.** Ultramarine names a story, identifies a primary action or marks an interactive state.

**The Feedback Rule.** Green and red belong to explicit status and destructive-action semantics.

## Typography

**Display Font:** WIA Display, locally served at weight 700 with the UI stack as fallback.

**Body Font:** the platform Chinese book stack for narration; the platform Chinese UI stack for navigation, controls, player actions and factual context.

**Character:** broad, deliberate display lettering introduces stories. Serif narration keeps long passages readable, while neutral sans typography keeps operating details compact.

### Hierarchy

- **Display:** the frontmatter display role serves home and story-detail headings. Mobile headings reduce to 27px.
- **Cover:** the cover role serves title planes. Detail cover lettering grows to 42px; compact covers use 27px.
- **Headline:** section headings use the headline role. Mobile section headings use 20px; dialogs use 24px, reducing to 22px on mobile.
- **Title:** smaller headings use the title role. Reader titles use 19px, reducing to 17px on mobile; factual-rail headings use 16px.
- **Reading:** narration uses the reading role in a maximum measure of 70ch. Mobile narration uses 17px with line-height 1.95. Text preserves intentional line breaks and wraps long strings.
- **Player:** submitted player text uses the player role inside a pale blue surface aligned to the right.
- **Body and Label:** the body role is the operational default. Labels and factual metadata use the label role; timestamps use 11px. Dates, clocks, status values and save metadata use tabular numerals.
- **Control:** primary, secondary and destructive actions use the control role.

The display face is loaded from [the local font asset](frontend/public/fonts/wia-display-bold.woff2) with `font-display: swap`. It covers Latin, Chinese ideographs, Chinese punctuation and full-width characters. [The font notice](frontend/public/fonts/README.md) and [OFL license](frontend/public/fonts/OFL.txt) remain with the asset. Reading fonts resolve from the user's installed platform fonts.

### Named Rules

**The Three Voices Rule.** WIA Display introduces a story, the book stack carries narration, and the UI stack carries actions and factual information.

## Layout

The centered shell is capped at 1280px with 32px side gutters. Page content has generous vertical separation, while controls and related labels use the compact spacing scale. Home story entries pair a 280px cover plane with a flexible description surface. Reading uses a flexible story column beside a 280px factual rail, separated by 40px.

The play shell follows the visible viewport through `--viewport-height`, initialized to `100dvh`. Its header takes natural height and its content takes the remaining space. The story column contains a fixed-size heading, a flexible independently scrolling transcript and the composer. The factual rail scrolls independently. Transcript scrolling preserves its own reading position; returning to the latest content is a separate visible action.

The composer divides into a scrollable body and a non-shrinking submit footer. Its text field grows within a viewport-relative cap. Safe-area padding protects the bottom control row. Long suggestions scroll horizontally within their own strip and retain their text inside individually scrollable options.

### Responsive behavior

| Condition | Layout behavior |
| --- | --- |
| Width ≤1100px | Shell gutters become 20px; factual rail becomes 240px with a 24px gap; secondary header actions move into the More menu. |
| Width ≤859px | Reading becomes one column; factual information is available in the scene drawer; story-detail cover planes are hidden; home cover columns become 220px. |
| Width ≤600px | Shell gutters become 14px; the header uses the WIA mark; home entries stack cover and description; reading insets reduce; settings options become one column; dialogs use 12px outer clearance. |
| Height ≤600px | Reader chrome and composer spacing compact; the transcript retains a 90px CSS minimum; the composer is capped at the remaining column height minus 160px; textarea, errors and suggestions receive smaller scrollable caps. |

On mobile, the reader surface has rounded upper corners and meets the viewport bottom. The scene drawer is right-aligned, limited to `min(380px, 92vw)`, and fills the visible viewport height. Regular dialogs are capped at 660px wide and scroll within the visible viewport.

### Named Rules

**The Reading and Submission Rule.** The transcript remains independently scrollable, and the submit footer remains outside the scrollable composer body.

## Elevation & Depth

Resting reading surfaces and story entries use flat tonal separation. Rules define content boundaries. Soft directional shadows belong to floating controls and overlays.

### Shadow Vocabulary

- **More menu:** `0 12px 35px #24345924`.
- **Return to latest:** `0 5px 18px #2434591a`.
- **Dialog:** `0 24px 70px #17234333`.
- **Scene drawer:** `-12px 0 40px #17234322`.

Dialogs and drawers dim the page with a translucent Blue Ink backdrop. The application header uses stacking level 5, the latest-content control uses 2 within the reader, and overlays use 20.

### Named Rules

**The Floating Layer Rule.** Shadows identify controls and panels that float above the reading surface.

## Shapes

The form language is softened rectangles. Controls and fields use the control radius; saves, messages and run surfaces use the item radius; story and reading surfaces and regular dialogs use the surface radius. Recipient chips use the compact chip radius. The scene drawer has square edges.

Dividers and field boundaries are one-pixel strokes. Temporary run surfaces use a dashed rule. Person avatars are small rectangular initials, with a 5px radius; selecting a person fills that initial tile with Ultramarine. These initials are content, while the dialog close control is an authored stroke SVG.

## Components

### Buttons

Primary actions have an Ultramarine fill and white text. Secondary actions use Reading White and a Paper Rule stroke. Destructive actions use Brick Feedback. Quiet navigation is transparent and uses Slate Ink.

Primary, secondary and destructive buttons share the control shape and a 44px minimum height. Quiet controls use a 40px minimum. Primary hover uses Deep Ultramarine; secondary hover changes the stroke and text to the accent. Disabled buttons use opacity 0.55 and a disabled cursor.

Button color and border changes last 180ms with `cubic-bezier(.16, 1, .3, 1)`. Keyboard focus uses a 2px Ultramarine outline with 3px clearance. Reduced-motion preference removes button transitions and the waiting pulse.

### Inputs / Fields

Inputs, selects and textareas use Reading White, a Paper Rule stroke and the control radius. Placeholders use Slate Ink; the caret uses Ultramarine. Focus uses a 2px accent outline with 1px clearance. Labels stay associated with their fields.

The composer field uses Cool Paper, a 16px font and line-height 1.7. It preserves free-form text, grows up to a quarter of the visible viewport height and scrolls internally beyond that cap.

### Navigation

The masthead combines the compact WIA tile, product name, connection status and quiet text actions. The current or hovered action uses Deep Ultramarine. At compact widths, secondary actions appear in the More menu; on mobile the WIA tile carries the brand.

### Cards / Containers

Story entries use a saturated title plane beside a white description and action surface. The cover title is the entry title when no cover image exists. Real story-provided cover images fill the plane with `object-fit: cover` and retain their supplied alternative text.

Save entries use an outlined item surface, with a separately bounded deletion action. Active saves add an accent outline and pale blue fill. Reading surfaces keep their heading, transcript and composer inside a single surface.

### People and recipient chips

Person rows combine an initial tile, name and role. Selection uses an accent outline, pale blue fill and a filled initial tile, exposed with `aria-pressed`. The composer destination reflects the same selected person. The removable recipient chip supplements the destination select and is hidden on mobile.

### Suggestions and waiting

Suggestions are numbered choices in a horizontal strip. The numbering represents selectable alternatives. The strip and each option scroll within bounded space; existing input and busy state are visible disabled states.

A pending run uses a dashed neutral surface and one small Ultramarine dot pulsing over 1.6s with ease-in-out. Reduced-motion preference leaves the dot static. Errors use a red-tinted surface with an explicit recovery action.

### Dialogs and the scene drawer

Regular dialogs center a white surface over the dimmed page; the scene drawer anchors to the right edge. Both cap their content to the visible viewport and scroll internally. The dialog traps focus, handles Escape, blocks page scrolling and returns focus to the original control or its visible fallback. Busy state protects ongoing operations; destructive confirmation requires an explicit dismissal control or Escape.

## Do's and Don'ts

### Do:

- **Do** use the global CSS custom properties for shared palette and font roles.
- **Do** keep WIA Display, its local font asset and its license together.
- **Do** preserve the distinct display, narration and UI typography roles.
- **Do** keep reading, composer content and submission controls within their established scroll boundaries.
- **Do** carry destination selection consistently between person rows and the composer.
- **Do** preserve visible keyboard focus, Chinese action labels and reduced-motion behavior.
- **Do** keep this file and its sidecar aligned with durable changes in the implemented styles.

### Don't:

- **Don't** replace the locally served display voice with a platform UI face.
- **Don't** place the submit footer inside the scrollable composer body.
- **Don't** use accent, success or error colors without their established story, action or state meaning.
- **Don't** add shadows to flat reading surfaces or use text glyphs as the reusable icon vocabulary.
