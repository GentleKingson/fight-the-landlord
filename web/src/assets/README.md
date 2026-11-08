# Lobby logo

- `landlord-logo.png` is the original, unmodified 1254 × 1254 transparent design source.
- `landlord-logo-176.webp` is the runtime asset: 176 × 176 with lossless WebP encoding and transparency. This supports up to 4× pixel density at the largest 44px display size. The header also uses 36px and 32px sizes on smaller viewports.
- Only import the WebP in application code, so Vite does not include the full-size source in the production assets.

Regenerate from the repository root with ImageMagick:

```sh
magick web/src/assets/landlord-logo.png -filter Lanczos -resize 176x176 -strip -define webp:lossless=true web/src/assets/landlord-logo-176.webp
```

The runtime asset is 26,300 bytes versus 1,073,837 bytes for the source (97.6% smaller). The browser regression test enforces a 32 KiB payload budget and checks its decoded dimensions and responsive layout.
