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
