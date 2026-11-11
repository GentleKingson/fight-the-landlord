# Lobby logo

- `landlord-logo.png` is the original, unmodified 1254 × 1254 transparent design source.
- `landlord-logo-176.webp` is the runtime asset: 176 × 176 with lossless WebP encoding and transparency. This supports up to 4× pixel density at the largest 44px display size. The header also uses 36px and 32px sizes on smaller viewports.
- Only import the WebP in application code, so Vite does not include the full-size source in the production assets.

Regenerate from the repository root with ImageMagick:

```sh
magick web/src/assets/landlord-logo.png -filter Lanczos -resize 176x176 -strip -define webp:lossless=true web/src/assets/landlord-logo-176.webp
```

The runtime asset is 26,300 bytes versus 1,073,837 bytes for the source (97.6% smaller). The browser regression test enforces a 32 KiB payload budget and checks its decoded dimensions and responsive layout.

## Neo-Chinese scene backgrounds

The outer `.app-shell` switches `data-scene` automatically across all web screens so every stage of a match has a dedicated Neo-Chinese atmosphere while preserving the opaque playing cards, clean green `.table-arena` felt, and zero-scroll responsive layouts:

- **`lobby` (`scene-lobby-*`) — 暖灯茶馆大厅**: carved rosewood lattice screens, warm silk palace lanterns with crimson tassels, and misty bamboo beyond the windows. Used across lobby home, leaderboard, personal stats, lobby chat, rules, matchmaking, and room waiting (`connecting | lobby | matching | waiting`), with fallback `#162921`.
- **`table` (`scene-table-*`) — 竹庭夜景牌桌**: moonlit emerald bamboo stalks, carved ebony pavilion eaves with antique gold fretwork, and soft warm lantern bokeh framing the card table during dealing, bidding, and active play (`dealing | bidding | playing`), with fallback `#082820`.
- **`settlement` (`scene-settlement-*`) — 祥云金辉结算**: sculpted golden ruyi clouds, crimson-and-gold festival palace lanterns, and imperial emerald brocade used for end-of-round hand reveal, unlimited board review, and victory/defeat score settlement (`game_over`), with fallback `#17261f`.

Each scene provides three metadata-free lossy VP8 WebP runtime variants:

- `scene-*-960.webp`: 960 × 540 (~29–37 KiB, 40 KiB budget), for landscape viewports up to 960 CSS pixels wide.
- `scene-*-1600.webp`: 1600 × 900 (~55–68 KiB, 72 KiB budget), for wider desktop/landscape viewports.
- `scene-*-portrait-900.webp`: 900 × 1600 (~39–56 KiB, 60 KiB budget), for portrait viewports (`orientation: portrait`).

`.app-shell::before` adds an isolated, non-interactive (`pointer-events: none; z-index: -1`) edge-contrast veil behind content so headers, navigation bars, and panels remain legible across aspect ratios without layout boxes or input interception. Each viewport requests only the single variant matching the active scene and orientation; Vite emits hashed `/assets/` URLs compatible with the Go static server.

To regenerate any scene's runtime variants from its landscape and portrait masters with ImageMagick:

```sh
convert "$LANDSCAPE_SRC" -filter Lanczos -resize 960x540^ -gravity center -extent 960x540 -strip -quality 80 -define webp:method=6 web/src/assets/scene-NAME-960.webp
convert "$LANDSCAPE_SRC" -filter Lanczos -resize 1600x900^ -gravity center -extent 1600x900 -strip -quality 80 -define webp:method=6 web/src/assets/scene-NAME-1600.webp
convert "$PORTRAIT_SRC" -filter Lanczos -resize 900x1600^ -gravity center -extent 900x1600 -strip -quality 80 -define webp:method=6 web/src/assets/scene-NAME-portrait-900.webp
```

`tests/backgroundAssets.test.ts` and `tests/e2e/background.spec.ts` check VP8 headers, decoded dimensions, payload budgets, single-variant network loading, scene transitions, viewport rotation, image-failure fallbacks, intact felt, and interactive drawers/cards on desktop and mobile viewports.

## Bot portraits

`bot-avatars/` contains the six selected emerald-and-gold portraits: scholar,
jade, uncle, blossom, sunshine and grandma. Each is a 192 × 192, metadata-free
WebP, sufficient for the largest 42px avatar at 4× density. Together they are
58,888 bytes (99.5% smaller than the six 1254 × 1254 PNG sources, 12,081,886 bytes).
No full-size sources are imported or shipped. Only the two assigned portraits
are fetched in a normal practice match, with a 12 KiB per-image budget.

The server explicitly sends `PlayerInfo.is_bot` in the additive protobuf field 8
on game-start and reconnect. Do not infer bots from their display names. The
web client hashes the bots' random UUIDs, sorts their IDs and draws without
replacement, so opponents never share a portrait in a match. The pair stays
stable through refresh, reconnect, role changes and redeals without browser
storage. A new practice match creates fresh bot UUIDs and a fresh selection;
it may legitimately repeat a prior portrait.

Avatars are 42px on desktop, 30px on compact widths, 28px on phone portrait and
26px on short landscape screens. A circular frame reserves the same space while
loading; a failed image falls back to the player's first full Unicode character.
Images are decorative because the adjacent name identifies the player. Humans
keep their initials. Names truncate, with full names in a title, while role and
remaining-card count stay visible.

Regenerate each runtime file from its selected original with ImageMagick:

```sh
magick "$SOURCE_PNG" -filter Lanczos -resize 192x192 -strip -quality 82 -define webp:method=6 web/src/assets/bot-avatars/NAME.webp
```

`?demo=practice` previews the two bot portraits without a backend. Unit tests
cover deterministic assignment, uniqueness, Unicode/error fallbacks and protocol
round trips. `tests/e2e/botAvatars.spec.ts` covers payload/decoded dimensions,
reload, image failures, human opponents, repeated interactions and live resizing
from 320px portrait to desktop. `live.spec.ts` checks real practice/reconnect when
`DDZ_LIVE_URL` is set. The server and web should be updated together; older servers
that omit `is_bot` safely retain initials rather than guessing robot identities.
