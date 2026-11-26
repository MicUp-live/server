# MicUp.live — Brand Identity & Design System Guide
*Version 1.0 • Stage Obsidian & Neon Spotlight*

---

## 1. Brand Essence & Narrative

### 1.1 The Purpose
Karaoke is high-energy entertainment trapped in a 1995 operational nightmare: grease-stained, 400-page paper binders, broken pens, missed queue rotations, and singers yelling track numbers across a deafening dive bar.

**MicUp.live** is a professional management platform for karaoke hosts (KJs) that transforms messy local media folders into a fast, zero-lag, searchable digital songbook in every pocket in the room.

### 1.2 Core Mottos & Taglines
* **Primary Brand Slogan**: *"The mic is hot. The room is ready."*
* **Singer Callout**: *"The Stage Is Calling"*
* **Value Proposition**: *"Your entire karaoke library in every pocket in the room. Instant search, zero binders, zero lag."*
* **Singer Repertoire Promise**: *"Your favorites and your vocal key follow you to every show."*

### 1.3 Brand Personality Attributes
| Trait | What It Means | How It Translates in the Product |
| :--- | :--- | :--- |
| **Electrifying & Theatrical** | Feels like live nightlife, stage lights, and rock concerts—never sterile enterprise SaaS. | Obsidian black canvas with luminous neon corals, warm stage golds, glowing audio meters, and live pulsing status dots. |
| **Utilitarian & Frictionless** | Respects the reality of a loud, dark bar with tipsy singers. Zero fluff, zero sign-up friction. | 1-tap favorites, instant client-side Web Worker fuzzy search over 50,000+ tracks, big 48px thumb targets. |
| **Pro-Audio Savvy** | Speaks the authentic language of pro audio and seasoned KJs. | Uses real pro-audio concepts: XLR pinouts, semitone pitch transposition (`-3♭` to `+3♯`), manufacturer disc codes (`SC`, `CB`, `SF`), fair rotation rules. |
| **Rock-Solid & Reliable** | Works seamlessly even under spotty venue Wi-Fi and cell dead-zones. | Static-first edge delivery via CloudFront/S3, client-side MiniSearch index, offline-capable LocalStorage favorites. |

---

## 2. Logo & Brandmark Specification

The MicUp brandmark represents a professional stage dynamic microphone (modeled after the legendary Shure SM58 / stage vocal standard) captured in mid-performance, tilted at a dynamic angle with radiating soundwaves.

```
                  .-''''-.   (Sound Wave 2: Golden Amber)
                .'  .-'''-. '.
               /   /       \  \  (Sound Wave 1: Electric Coral)
             .'   /  .---.  \  '.
            /    /  /  .-. \  \   \
           |    |  |  ( # ) |  |   |   <-- Satin Steel Wire Mesh Grille
            \    \  \  '-' /  /   /
             '.   \  '---'  /  .'
               \   \       /  /
                '.  '-...-' .'
                  '-.....-'
                      ||   <-- "Hot Mic" Live Ring Collar (#ff2a5f -> #f59e0b)
                   [======]
                   |  ||  | <-- Ribbed Gunmetal Handle (#1e293b / #334155)
                   |  ||  |
                   | [ON] | <-- Live Red Rocker Switch Slider (Locked On)
                   |  ||  |
                   |======|
                   \======/
                    |::::|  <-- 3-Pin XLR Connector Base
```

### 2.1 Visual Anatomy
1. **Dynamic Angle**: The microphone is angled precisely at **-35°** (tilted counter-clockwise), conveying action, forward momentum, and stage performance.
2. **The Acoustic Capsule & Grille**:
   - Dual-hemisphere wire mesh rendered with a satin chrome gradient (`#ffffff` highlight tapering down to `#1e293b`).
   - Subtle geometric diamond hatch pattern evoking woven steel protection.
3. **The "Hot Mic" Live Collar Ring**:
   - A bright transition collar separating the grille from the handle, filled with an **Electric Coral (`#ff2a5f`) to Golden Amber (`#f59e0b`)** linear gradient.
   - Represents an "ON AIR" indicator—the stage is active.
4. **The Gunmetal Handle & Ergonomics**:
   - Heavy anodized zinc-alloy handle in deep slate/gunmetal (`#475569` to `#0f172a`).
   - Dual recessed ribbed grip bands near the base for tactile realism.
5. **The Live Rocker Switch**:
   - Recessed slide switch bezel with the toggle slider positioned upward in the **ON** position.
   - Features a bright glowing red indicator dot (`#ff2a5f`).
6. **XLR Three-Pin Base**:
   - Authentic 3-pin balanced audio termination connector (`#1e293b` with `#64748b` contact pins), grounding the mark in professional audio gear.
7. **Twin Radiant Sonic Arcs**:
   - Two concentric forward acoustic waves:
     * **Inner Arc**: Electric Coral (`#ff2a5f`), 1.6px stroke, high opacity.
     * **Outer Arc**: Golden Amber (`#f59e0b`), 1.4px stroke, 80% opacity.
8. **Atmospheric Backlight Aura**:
   - Soft radial gradient glow (`rgba(255, 42, 95, 0.45)` to `rgba(245, 158, 11, 0.15)`) behind the capsule.

### 2.2 Sizing & Safe Margin Guidelines
* **Minimum App Icon / Favicon Size**: `20px × 20px` (disable detailed mesh; preserve outer silhouette, collar, and sound wave).
* **Navbar / Header Size**: `28px × 28px`.
* **Mobile Hero Mark**: `56px × 56px` with continuous floating micro-animation (`animation: float 4s ease-in-out infinite`).
* **Stage TV Display & Kiosk**: `96px–160px` with ambient glow drop-shadow (`filter: drop-shadow(0 0 24px rgba(255, 42, 95, 0.5))`).
* **Clear Space Rule**: Maintain at least **1.5× the capsule width** of clear space around the mark. No typography or interface elements may collide with the radiating sonic arcs.

### 2.3 Logo Misuse (What NEVER To Do)
* ❌ Never rotate the microphone to 0° (vertical) or 90° (horizontal)—the -35° angle is required for stage momentum.
* ❌ Never render the brandmark on a white or light gray background without an obsidian container.
* ❌ Never tint the "Hot Mic" collar green, blue, or purple. It must always be Electric Coral to Amber.
* ❌ Never position the slide switch in the "OFF" position. In MicUp, the mic is always hot.

---

## 3. Color System — "Stage Obsidian & Neon Spotlight"

Our palette is engineered specifically for low-light bar and nightclub environments (0.5 to 5 lux). It eliminates screen glare, saves OLED mobile battery, and directs the eye straight to stage status.

### 3.1 Core Palette Tokens

```
=======================================================================================
Stage Obsidian (Canvas)  Backstage Surface       Rack Mount Alt         Truss Border
#0b0b10                  #13131c                 #1c1c28                #28283c
---------------------------------------------------------------------------------------
The Hot Mic (Primary)    Hot Mic Glow            The Chorus Spotlight   Chorus Glow
#ff2a5f                  rgba(255,42,95,0.40)    #f59e0b                rgba(245,158,11,0.35)
---------------------------------------------------------------------------------------
High-Luminance Text      Stage Silver (Muted)    Dim Slate (Tertiary)   Emerald Live (Success)
#f8fafc                  #a6b4c9                 #7b8ba5                #10b981
=======================================================================================
```

| Token Name | Hex Value | CSS Variable | Semantic Usage |
| :--- | :--- | :--- | :--- |
| **Stage Obsidian** | `#0b0b10` | `--color-bg` | Main app background, body canvas, stage TV backdrop. Deepest black without looking completely crushed. |
| **Backstage Surface** | `#13131c` | `--color-surface` | Primary cards, modal dialogs, drawer panels, sticky headers. |
| **Rack Mount Alt** | `#1c1c28` | `--color-surface-alt` | Search input fields, secondary table rows, nested metadata containers. |
| **Truss & Border** | `#28283c` | `--color-border` | 1px clean separation lines, card strokes, dividers. |
| **The Hot Mic** | `#ff2a5f` | `--color-accent` | Primary action buttons ("Explore", "Request Track", "Sing Tonight"), live badge indicators, key brand accents. |
| **Hot Mic Glow** | `rgba(255, 42, 95, 0.4)` | `--color-accent-glow` | Button drop-shadow glow, live pulsing dot aura. |
| **Hot Mic Pressed**| `#dc1d4c` | `--color-accent-dark` | Button active/hover pressed state. |
| **The Chorus Spotlight** | `#f59e0b` | `--color-chorus` | Golden stars, favorite toggles, "Up Next" announcements, tip jars, verified badges. |
| **Chorus Glow** | `rgba(245, 158, 11, 0.35)` | `--color-chorus-glow` | Spotlight badge glow, star illumination. |
| **High-Luminance White** | `#f8fafc` | `--color-text` | Primary headings, song titles, main labels. Contrast ratio > 16:1 against obsidian. |
| **Stage Silver** | `#a6b4c9` | `--color-text-muted` | Artist names, secondary instructions, table headers. |
| **Dim Slate** | `#7b8ba5` | `--color-text-dim` | Song key, track duration, manufacturer codes (`SC8101`), timestamps. |

### 3.2 Functional Status Palette
* **Live / On Stage / Success**: `#10b981` (Emerald) — Currently singing, verified track, successful sync.
* **On Deck / Attention / Warning**: `#f59e0b` (Amber Gold) — 1 song away, review suggestions, pending confirmation.
* **Hot Mic / Urgent / Danger**: `#ef4444` / `#ff2a5f` (Coral Red) — Rejected request, song deletion, critical error.
* **Sync / Audio Link / Info**: `#06b6d4` (Cyan) — Web Worker indexing, CloudFront cache sync, desktop API connection.

---

## 4. Typography System

The typography pairs an energetic, bold geometric display face with an ultra-legible modern sans-serif and an engineered monospace for musical pitch notation and show codes.

```
       HEADINGS: Syne (Geometric, Dramatic, Theatrical)
       ----------------------------------------------------
       "The mic is hot. The room is ready."

       BODY & CATALOG: Plus Jakarta Sans (Clean, High x-Height)
       ----------------------------------------------------
       Don't Stop Believin' • Journey • 4:11

       TECHNICAL & KEYS: JetBrains Mono (Engineered, Precise)
       ----------------------------------------------------
       SHOW: OMALLEY  |  PITCH: -1♭  |  DISC: SC8101-02
```

### 4.1 Type Families
1. **Display Headings (`--font-display`)**: **Syne** (Weights: 700 Bold, 800 Extra-Bold)
   * *Role*: Stage TV hero announcements, landing page value propositions, screen headers.
   * *Character*: High contrast, architectural curves, memorable stage presence.
   * *Fallback*: `-apple-system, BlinkMacSystemFont, sans-serif`.
2. **Interface & Catalog (`--font-sans`)**: **Plus Jakarta Sans** (Weights: 400 Regular, 500 Medium, 600 Semi-Bold, 700 Bold)
   * *Role*: Song titles, artist labels, navigation items, buttons, modal copy.
   * *Character*: Wide open apertures, large x-height, maximum readability on low-brightness mobile screens in dark bars.
   * *Fallback*: `Inter, -apple-system, BlinkMacSystemFont, sans-serif`.
3. **Data, Pitch & Codes (`--font-mono`)**: **JetBrains Mono** (Weights: 500 Medium, 600 Semi-Bold)
   * *Role*: Event Join Codes (`OMALLEY`, `DIVENIGHT`), pitch shift values (`-2♭`, `+1♯`), queue position tokens (`#01`, `#02`), API keys (`mk_live_...`).
   * *Character*: Clear tabular numeral alignment, distinct glyphs for musical flat (`♭`) and sharp (`♯`).

### 4.2 Type Scale & Hierarchy

| Level | Size (rem) | Size (px) | Weight | Line Height | Tracking | Application |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| **Display (Stage)** | `3.00rem` | `48px` | `800` | `1.10` | `-0.02em` | TV Stage display singer callout ("UP NEXT: SARAH K.") |
| **Hero Title** | `2.50rem` | `40px` | `700` | `1.15` | `-0.01em` | Landing page main headline |
| **Heading 1** | `2.00rem` | `32px` | `700` | `1.20` | `-0.01em` | KJ command dashboard titles, kiosk title |
| **Heading 2** | `1.50rem` | `24px` | `600` | `1.25` | `0.00em` | Section cards, modal titles, singer drawer header |
| **Heading 3** | `1.25rem` | `20px` | `600` | `1.30` | `0.00em` | Song title in catalog list, artist group header |
| **Body Large** | `1.125rem`| `18px` | `500` | `1.50` | `0.00em` | Hero description, stage ticker announcements |
| **Body Base** | `1.00rem` | `16px` | `400` | `1.50` | `0.00em` | Default UI copy, button labels, search input |
| **Body Small** | `0.875rem`| `14px` | `500` | `1.40` | `0.01em` | Artist name under song title, singer notes |
| **Micro / Tag** | `0.75rem` | `12px` | `600` | `1.30` | `0.08em` | Uppercase stage badges (`LIVE`, `ON DECK`, `POPULAR`) |

---

## 5. UI Components & Surface Treatments

### 5.1 The "Frosted Stage Glass" Surface Pattern
To preserve depth while scrolling through 50,000+ tracks, top search bars and bottom action drawers use subtle frosted stage glass rather than opaque blocks.

```css
/* Frosted Sticky Search Header */
.sticky-stage-header {
  background: rgba(19, 19, 28, 0.85);
  backdrop-filter: blur(16px);
  -webkit-backdrop-filter: blur(16px);
  border-bottom: 1px solid var(--color-border);
}
```

### 5.2 Glowing Buttons & States
* **Primary Button (`.btn-primary`)**:
  - Background: `linear-gradient(135deg, #ff2a5f 0%, #dc1d4c 100%)`
  - Color: `#ffffff` (Font Weight 600)
  - Box Shadow: `0 4px 16px rgba(255, 42, 95, 0.35)`
  - Hover: `transform: translateY(-1px); box-shadow: 0 6px 20px rgba(255, 42, 95, 0.5);`
  - Active: `transform: translateY(1px); filter: brightness(0.95);`
* **Secondary Button (`.btn-secondary`)**:
  - Background: `var(--color-surface-alt)` (`#1c1c28`)
  - Border: `1px solid var(--color-border)` (`#28283c`)
  - Color: `var(--color-text)` (`#f8fafc`)
  - Hover: `border-color: var(--color-accent); color: #ffffff;`
* **Chorus Spotlight Action (`.btn-chorus` / Star Toggle)**:
  - Active / Starred: Color `#f59e0b`, Glow `0 0 12px rgba(245, 158, 11, 0.5)`

### 5.3 Touch Ergonomics (The "Bar & Cocktail Rule")
Every button and interactive element intended for singers or busy KJs must respect:
1. **Minimum Touch Target**: At least **`48px × 48px`** bounding box.
2. **Bottom-Heavy Layout**: Main action buttons (Request Song, Pitch Adjust, Search Clear) stay in the lower 40% of the mobile screen (the natural thumb reach zone).
3. **No Accidental Double-Tap Disasters**: Debounce request buttons with immediate tactile feedback (`"Adding to Queue..."`) to prevent double-submitting tracks.

### 5.4 Micro-Animations
1. **The Live Pulse (`live-pulse`)**:
   ```css
   @keyframes live-pulse {
     0%, 100% {
       transform: scale(1);
       box-shadow: 0 0 6px var(--color-accent);
     }
     50% {
       transform: scale(1.25);
       box-shadow: 0 0 14px var(--color-accent);
     }
   }
   ```
2. **The Stage Shake (`stage-bump`)**:
   - Triggers for 900ms whenever the stage queue rotates or a singer is bumped to "Up Next".
   - Subtle tactile 2px horizontal vibration drawing eyes to the TV display.

---

## 6. Voice, Tone & Copywriting Standards

Our copy is punchy, supportive, and direct. It balances the high-energy excitement of a live concert with the crystal-clear instructions required in a chaotic bar.

### 6.1 Principles

| Principle | Guideline |
| :--- | :--- |
| **Celebrate the Singer** | Treat every singer like a rockstar stepping up to the microphone. Never make them feel anxious or judged. |
| **Zero Jargon for Patrons** | Never mention databases, caches, Web Workers, or indexes. It’s just "The Songbook" and "The Stage". |
| **Pro Audio Respect for Hosts** | Acknowledge that the KJ is running the audio board and managing a live crowd. Speak their language without being condescending. |
| **High Urgency in Live States** | When a singer is on deck or up next, instructions must be unmissable. |

### 6.2 Copy Comparison (Do's & Don'ts)

| Scenario | ❌ Don't Say | ✅ Say This |
| :--- | :--- | :--- |
| **Queue Position** | "Your request #104 has transitioned to position 2 in the database queue." | "You're **On Deck**! 🎤 Grab a drink and head towards the stage." |
| **Pitch Adjustment** | "Configure semitone key pitch transposition parameter: -1." | "Pitch Shift: **-1♭** (Down half step — we'll remember this forever)." |
| **Missing / Custom Song** | "Item not found in canonical reference dictionary." | "Can't find it? Request a custom song from the KJ's private vault." |
| **Show Code Entry** | "Input 6-character alphanumeric venue identifier." | "Enter show code (e.g. **OMALLEY**) or scan the stage QR code." |
| **Catalog Sync** | "S3 multipart manifest batch ingestion completed with soft-pruning." | "Your entire library is synced: **42,810 songs** ready for tonight." |
| **Fair Rotation** | "Priority algorithm has balanced the wait times." | "Fair rotation: every singer gets their spotlight." |

---

## 7. Multi-Surface Experience Guidelines

MicUp.live runs across four distinct physical surfaces. Each surface has tailored brand styling:

```
[ 1. Singer Mobile Web ]      [ 2. KJ Command Center ]
 - One-thumb ergonomics        - High-density table rows
 - High contrast catalog       - Fast bump & queue reorder
 - Pitch shift memory          - Real-time polling badges

[ 3. TV Stage Display ]       [ 4. Walk-Up Kiosk ]
 - 1080p/4K TV visible at 40ft - Touchscreen self-service
 - Massive glowing "UP NEXT"   - 60s inactivity auto-reset
 - Large 240px+ QR code        - Fullscreen kiosk lock
```

### 7.1 Singer Mobile Web (`/e/:join_code`, `/book/:id`)
* **Primary Focus**: Search velocity and personal repertoire.
* **Palette**: Full Stage Obsidian background.
* **Key Feature**: Starred favorites persist across venues; pitch shift remembers vocal keys per song.
* **Notifications**: Native browser vibration + Web Notifications for *"You're On Deck"* and *"You're Up Next"*.

### 7.2 KJ Command Center (`/kj/queue/:id`, `/kj/queue/event/:id`)
* **Primary Focus**: Operational speed and crowd rotation control.
* **Palette**: Dark Rack Mount surfaces with high-density data tables (`JetBrains Mono` for timers and singer counts).
* **Controls**: 1-click bump up/down, accept/decline requests, pause show, broadcast ticker announcements.

### 7.3 TV Stage Display (`/stage/event/:id`)
* **Primary Focus**: Visible from 40 feet across a smoky bar room.
* **Design Elements**:
  - High-visibility QR code with high-contrast quiet zone (minimum 240px).
  - Ticker banner scrolling announcements along the bottom.
  - Large display font (`Syne` 800) for "NOW SINGING" and "UP NEXT".
  - Stage bump animation whenever rotation shifts.

### 7.4 Walk-Up Kiosk (`/kiosk/event/:id`)
* **Primary Focus**: Simple walk-up browsing on an iPad/touchscreen at the KJ booth.
* **Design Elements**:
  - Auto-reset timer (defaults to 45–60 seconds of inactivity).
  - Virtual on-screen touch targets with enlarged touch spacing.
  - Virtual keyboard mitigation.

---

## 8. Physical Brand Collateral Standards

When printing table tents, QR coasters, and DJ booth banners:
1. **QR Code Quiet Zone**: Always provide a solid white border around QR codes so camera lenses can lock on under dim bar lighting.
2. **Color Profile**:
   * *The Hot Mic Coral*: CMYK `0 / 92 / 52 / 0` (Pantone 192 C)
   * *The Chorus Gold*: CMYK `0 / 40 / 100 / 0` (Pantone 137 C)
   * *Stage Obsidian*: Rich Black `60 / 40 / 40 / 100`
3. **Table Tent Standard Text**:
   > **Scan. Search. Sing.**  
   > Skip the paper binders. Enter code **[JOIN_CODE]** at **micup.live** to browse the songbook and save your vocal pitch.

---

## 9. Design Foundations & Standards (Grounded in Industry Guidelines)

The MicUp.live visual and ergonomic architecture is anchored in the foundational standards, design systems, and UX laws curated in industry benchmarks (such as Nicole Saidy's *Awesome Web Design* index):

### 9.1 Behavioral Psychology & Laws of UX (via *Laws of UX*)
* **Fitts's Law (Touch Target Acquisition)**:
  * In a crowded, dimly lit venue with drinks in hand, fine motor control is reduced.
  * *Standard*: Every interactive surface (request buttons, pitch toggles, star icons) must maintain a minimum target boundary of **48px × 48px** and reside primarily in the bottom 40% "thumb reach" zone.
* **Hick’s Law & Miller’s Law (Minimizing Decision Paralysis)**:
  * A catalog of 50,000+ tracks can easily overwhelm a patron.
  * *Standard*: Avoid multi-level nested menus or endless filter trees. Browsing is simplified into three primary views: `Flat Song List`, `Grouped by Artist` (with track counts), and personal `⭐ Favorites`. Search results are chunked into visible viewport clusters via virtualized lists.
* **The Doherty Threshold (Sub-100ms Feedback)**:
  * Users perceive interactions as instantaneous if system feedback occurs within 400ms (and ideally <100ms).
  * *Standard*: Instant fuzzy search queries execute off the main thread via a dedicated Web Worker (`catalogWorker.js` + MiniSearch) in under **30ms**. Requests update optimistic UI states immediately.
* **Jakob’s Law (Familiar Mental Models)**:
  * Singers should not have to learn novel UI metaphors in the middle of a party.
  * *Standard*: Use universally recognized icons (star for favorites, search magnifying glass, clear 'X' chips, musical flat `♭` and sharp `♯` notations).

### 9.2 Surface Elevation & Lighting (via *Google Material Design Dark Theme*)
* **Elevation Through Lightness, Not Drop Shadows**:
  * In dark mode, traditional cast drop-shadows are virtually invisible against dark backgrounds.
  * *Standard*: MicUp conveys depth through progressive surface luminosity:
    * `0dp` (Base Stage Canvas): `#0b0b10` (0% elevation tint)
    * `1dp` (Backstage Surface / Cards): `#13131c` (+4% surface luminosity)
    * `2dp` (Rack Mount Alt / Floating Search): `#1c1c28` (+8% surface luminosity)
    * `3dp` (Modals / Drawers): `#1c1c28` with 1px border stroke (`#28283c`) and ambient Hot Mic glow (`rgba(255, 42, 95, 0.25)`).

### 9.3 Mobile-First Ergonomics (via *iOS Human Interface Guidelines*)
* **Bottom Sheets & Drawer Modals**:
  * Critical singer workflows (confirming a request, changing pitch, reviewing queue position) open as bottom-anchored modal sheets that can be dismissed with a single downward swipe.
* **Zero Login Wall (Frictionless GoodUI Principle)**:
  * Never force user registration or password creation before a singer can view the book, favorite a song, or submit a request. Repertoire identity is tied to device LocalStorage with seamless cross-show hash matching.

### 9.4 Typography Scaling & Readability (via *Type Scale* & *Typewolf*)
* **Modular Scale**:
  * Built on a **Minor Third (1.200)** modular scale to guarantee harmonious visual cadence between micro metadata (`12px`) and stage headlines (`48px`).
* **Tabular Figure Alignment**:
  * All countdown clocks, queue positions (`#01`, `#02`), and musical semitones (`-1♭`, `+2♯`) use monospaced tabular figures (`font-variant-numeric: tabular-nums`) via **JetBrains Mono** to prevent jitter during live updates.

### 9.5 Color Contrast & Accessibility (WCAG 2.1 AA / AAA in Low Ambient Light)
* **High-Contrast Nightlife Calibration**:
  * Standard accessibility tools test against bright 300-lux office lighting. In a 1-lux bar environment, low-contrast grays become illegible.
  * *Standard*:
    * Primary text (`#f8fafc`) on Obsidian (`#0b0b10`): **18.4:1 contrast ratio** (Exceeds WCAG AAA).
    * Stage Silver (`#a6b4c9`) on Surface (`#13131c`): **8.7:1 contrast ratio** (Exceeds WCAG AA).
    * Hot Mic Accent (`#ff2a5f`) on Surface (`#13131c`): **5.2:1 contrast ratio** for graphical controls and large action targets.

---

## 10. Component & State Matrix

This matrix formalizes the canonical UI primitives, responsive viewport behaviors, and feedback states implemented across MicUp.live.

### 10.1 Canonical UI Primitives

| Component | Variants | Sizes | Minimum Touch Target | Key Tokens & ARIA Rules |
| :--- | :--- | :--- | :--- | :--- |
| **`Button`** | `primary` (Coral glow)<br>`secondary` (Surface border)<br>`danger` (Red/border)<br>`chorus` (Gold glow)<br>`info` (Cyan border)<br>`ghost` (Transparent) | `sm` (32px visual, 48px target)<br>`md` (40px visual, 48px target)<br>`lg` (48px visual/target) | **48px × 48px** (`var(--touch-target-min)`) via pseudo-element padding or minimum dimensions | • `font-family: var(--font-sans)`<br>• `font-weight: var(--font-semibold)`<br>• Focus: `box-shadow: 0 0 0 2px var(--color-accent)`<br>• Disabled: `opacity: 0.5; cursor: not-allowed` |
| **`IconButton`** | `ghost`, `secondary`, `primary`, `danger` | `sm` (32px), `md` (40px), `lg` (48px) | **48px × 48px** always enforced | • Requires explicit `aria-label`<br>• Centered SVG / icon alignment<br>• `border-radius: var(--radius-md)` or `var(--radius-full)` |
| **`Badge`** | `default`, `accent`, `chorus`, `info`, `success`, `warning`, `danger` | `sm` (18px height, 11-12px type)<br>`md` (24px height, 12-13px type) | N/A (informational/status) | • `font-family: var(--font-mono)` for metrics/codes<br>• Semantic background tint (15% opacity) + 30% border stroke<br>• Never uses sub-12px typography |
| **`Modal` / Bottom Sheet** | `dialog` (center popup)<br>`sheet` (bottom drawer on mobile) | `sm` (380px max)<br>`md` (480px max)<br>`lg` (640px max)<br>`xl` (800px max) | Close button: **48px × 48px** | • `role="dialog"` + `aria-modal="true"`<br>• `aria-labelledby="[id]"` on dialog title<br>• Backdrop click & `Escape` key dismissal<br>• Focus trap & return-on-close<br>• Mobile: slides up from bottom edge |
| **Form Inputs** | Text, search, password, select | `md` (44px), `lg` (48px) | **48px** height on touch viewports | • `background: var(--color-surface)`<br>• `border: 1px solid var(--color-border)`<br>• Focus: `border-color: var(--color-accent)` |

### 10.2 Responsive Viewport Specifications

MicUp.live tailors its presentation to four distinct hardware viewports:

```
+-------------------+---------------------+-------------------------+-------------------------------+
| Mobile (390px)    | Tablet (768px)      | Desktop (1280px)        | Stage TV (1920px)             |
| Singer Web App    | Kiosk & DJ Sidecar  | KJ Command Center       | Ambient Screen                |
+-------------------+---------------------+-------------------------+-------------------------------+
```

#### 1. Mobile Singer App (390px portrait standard)
* **Target Device**: Handheld smartphones (iPhone 13/14/15/16, Pixel, Galaxy).
* **Ergonomic Profile**: One-thumb lower-screen quadrant reach.
* **Layout Adaptations**:
  * Modals convert to bottom-anchored sheets (`border-radius: var(--radius-2xl) var(--radius-2xl) 0 0`).
  * Alphabet scrubber pinned to right edge with high-contrast active indicator.
  * Search bar stays sticky at top or bottom thumb reach with instant 30ms worker query.
  * Floating Queue status bar anchored to bottom edge (`height: var(--queue-bar-height, 56px)`).
  * Interactive controls maintain minimum 48px touch margins with zero overlapping hitboxes.

#### 2. Walk-Up Kiosk & Sidecar (768px portrait/landscape tablet)
* **Target Device**: iPad 10.2", iPad Air, Android kiosk mounted at KJ table or bar corner.
* **Ergonomic Profile**: Arm-length touch interface without keyboard or mouse.
* **Layout Adaptations**:
  * Split navigation: Letter bar on left, virtualized category/artist grid on right.
  * Auto-reset countdown timer: 45–60 seconds of touch inactivity resets view to catalog home.
  * Touch target spacing expanded to 56px to eliminate mis-taps by bar patrons.
  * Virtual keyboard protection: Fullscreen lock to prevent OS address bar escape.

#### 3. KJ Command Center (1280px+ desktop/laptop)
* **Target Device**: MacBook, Dell XPS, or venue laptop running KJ console.
* **Ergonomic Profile**: High-density operational cockpit for rapid stage management.
* **Layout Adaptations**:
  * Dual-column layout: Live queue table (drag/bump reordering) + Singer detail panel.
  * Monospaced wait timers and queue versions using `JetBrains Mono` with `tabular-nums`.
  * Keyboard shortcuts (`Space` to advance singer, `Esc` to close drawer, `↑/↓` to navigate queue).
  * Real-time polling counters showing monotonic version sync (`?since=...`).

#### 4. Stage TV Display (1920px 1080p/4K TV)
* **Target Device**: Wall-mounted flat screens visible from 40+ feet.
* **Ergonomic Profile**: Passive visual anchor, high-luminance dark mode.
* **Layout Adaptations**:
  * Default QR code enlarged to **≥240px** (up to 320px in enlarged overlay) with 30% error correction.
  * Headline typography (`Syne` 800) rendered at 48px–72px for maximum legibility through haze.
  * High-contrast glowing borders for "NOW SINGING" (`Hot Mic Coral`) and "UP NEXT" (`Chorus Gold`).
  * Live marquee ticker banner along bottom for crowd announcements.

### 10.3 Feedback & Lifecycle State Matrix

Every view and component implements explicit states across all phases:

| State | Visual Treatment | Ergonomic / Behavioral Rules | Screen Reader Affordance |
| :--- | :--- | :--- | :--- |
| **Loading / Initializing** | Pulsing subtle skeleton cards (`var(--color-surface)` shimmer) or centered spinner badge. | Non-blocking; retains previous view content during background delta polling (`204 No Content`). | `aria-busy="true"`, `aria-live="polite"` with `"Loading catalog..."` |
| **Empty Repertoire / Search** | High-contrast icon badge, clear descriptive copy (`"No tracks found"`), and instant action button. | 1-click `"Clear Filter"` or `"Browse Repertoire"` button directly under the empty prompt. | Clear semantic headline (`<h2>No songs found</h2>`) |
| **Submitting / Optimistic** | Immediate badge pulse and local state append; button displays inline spinner with disabled pointer. | Double-submit lockout; rolls back gracefully with toast if server rejects request. | Button text updates to `"Submitting request..."` |
| **On Deck / Up Next (Singer)** | Glowing banner at top of viewport; device triggers 200ms haptic vibration and notification badge. | Bottom queue bar illuminates in `Chorus Gold` (`#f59e0b`) with remaining wait estimate. | High-priority announcement: `"You're on deck! Head toward the stage."` |
| **Live Stage Rotation** | Visual pulse transition on Stage TV; "NOW SINGING" spotlight updates with singer name and track title. | Smooth CSS transform transition without page reloads or layout jump. | Announce next singer turn in live console logs |
| **Network Interruption / Error** | Amber reconnect banner at top (`ConnectionStatus`); polling gracefully backs off without flashing UI. | User requests queue offline or retry automatically when back online. | Alert banner with `role="alert"`: `"Connection lost. Retrying..."` |
| **Action Confirmation / Toast** | Auto-dismissing 4s toast banner anchored above bottom navigation bar. | Swipe-to-dismiss or auto-fade; pause timer on hover/focus. | `aria-live="polite"` toast container |

