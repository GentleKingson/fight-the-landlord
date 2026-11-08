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

- `emerald-table-960.webp`: 960 × 540, 7,992 bytes, for viewports up to 960 CSS pixels wide.
- `emerald-table-1600.webp`: 1600 × 900, 22,764 bytes, for wider viewports.
- The 1672 × 941 source PNG was 2,033,166 bytes. Runtime files are 98.9–99.6% smaller;
  the full-size source is not shipped or imported by the app.
- `cover` and centered positioning preserve the design's proportions without tiling
  or stretching. Narrow portrait screens intentionally crop the decorative corners.
- A solid `#003b29` background remains readable during loading or if the image fails.
  The background is CSS-only, adds no layout box, and does not intercept input.
- Vite emits hashed `/assets/` URLs supported by the existing Go static server.
  Each viewport initially requests just its matching size; no preload fetches both.

To regenerate from the selected original PNG, with ImageMagick:

```sh
magick "$SOURCE_PNG" -filter Lanczos -resize 960x -strip -quality 82 -define webp:method=6 web/src/assets/emerald-table-960.webp
magick "$SOURCE_PNG" -filter Lanczos -resize 1600x -strip -quality 82 -define webp:method=6 web/src/assets/emerald-table-1600.webp
```

`tests/e2e/background.spec.ts` checks payload budgets (10 / 24 KiB), decoded sizes,
responsive selection, navigation/reload, image-failure fallback, intact felt and
repeated drawer/card interactions on desktop and mobile viewports.
