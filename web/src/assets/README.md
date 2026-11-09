# Lobby logo

- `landlord-logo.png` is the original, unmodified 1254 × 1254 transparent design source.
- `landlord-logo-176.webp` is the runtime asset: 176 × 176 with lossless WebP encoding and transparency. This supports up to 4× pixel density at the largest 44px display size. The header also uses 36px and 32px sizes on smaller viewports.
- Only import the WebP in application code, so Vite does not include the full-size source in the production assets.

Regenerate from the repository root with ImageMagick:

```sh
magick web/src/assets/landlord-logo.png -filter Lanczos -resize 176x176 -strip -define webp:lossless=true web/src/assets/landlord-logo-176.webp
```

The runtime asset is 26,300 bytes versus 1,073,837 bytes for the source (97.6% smaller). The browser regression test enforces a 32 KiB payload budget and checks its decoded dimensions and responsive layout.

## Emerald page background

The selected emerald-and-gold design is used only on the outer `.app-shell`.
The original green felt, cards and opaque panels are unchanged. The table screen
is transparent so its former gradients and decorative wood layer cannot hide the image.

- `emerald-table-960.webp`: 960 × 540, 7,992 bytes, for landscape viewports up to 960 CSS pixels wide.
- `emerald-table-1600.webp`: 1600 × 900, 22,764 bytes, for wider landscape viewports.
- `emerald-table-portrait-900.webp`: 900 × 1600, 17,254 bytes, for portrait viewports.
  Its separate 941 × 1672 source PNG was 1,828,494 bytes (99.1% smaller at runtime).
- The 1672 × 941 source PNG was 2,033,166 bytes. Runtime files are 98.9–99.6% smaller;
  the full-size source is not shipped or imported by the app.
- Landscape uses centered `cover`. Portrait uses a decorative pseudo-element with
  nine-slice `border-image`: all four square gold corners remain uncropped and
  unstretched, even on tall 19.5:9 phones. Only the quiet felt center and edge strips
  stretch. The 120px source corners render at 40–100 CSS pixels to match the viewport.
  An isolated stacking context keeps the decoration behind content, with no pointer
  events or layout footprint. Returning to landscape removes the portrait layer.
- A solid `#003b29` background remains readable during loading or if the image fails.
  The background is CSS-only, adds no layout box, and does not intercept input.
- Vite emits hashed `/assets/` URLs supported by the existing Go static server.
  Each viewport initially requests just its matching orientation/size; no preload
  fetches the other variants. Rotation updates the asset without reloading the page.

To regenerate from the selected original PNG, with ImageMagick:

```sh
magick "$SOURCE_PNG" -filter Lanczos -resize 960x -strip -quality 82 -define webp:method=6 web/src/assets/emerald-table-960.webp
magick "$SOURCE_PNG" -filter Lanczos -resize 1600x -strip -quality 82 -define webp:method=6 web/src/assets/emerald-table-1600.webp
magick "$PORTRAIT_SOURCE_PNG" -filter Lanczos -resize x1600 -strip -quality 82 -define webp:method=6 web/src/assets/emerald-table-portrait-900.webp
```

`tests/e2e/background.spec.ts` checks payload budgets (10 / 24 / 20 KiB), decoded
sizes, portrait-frame layering, live rotation/viewport changes, navigation/reload,
image-failure fallback, intact felt and repeated drawer/card interactions on
desktop and mobile viewports.

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
