# Shared visual language

The canonical source for this release is the supplied Dreamhuggers visual-language kit at revision `83f1d48289dc3377f61bf321bad8f1456235c577`. Its MIT notice is in web/brand/LICENSE. web/brand/tokens.ts is an unmodified copy of the kit’s source of truth. Use its HSL triples where the descriptive README or sample site stylesheet differs.

The app uses Tailwind 4. From web/, run `node brand/theme.mjs` to regenerate brand/theme.css and `node brand/theme.mjs --check` to verify it. The adapter exports the kit’s color, type, shadow and gradient values as native Tailwind theme variables and applies the preset’s radius scale. It does not import the example site or a second CSS framework.

Inbox, Sent and conversations use compact text headings. The sidebar carries the small product mark. Decorative Mator illustrations appear at onboarding and in the empty state. Sender marks derive their shape and color from the sender name. The reader groups its receipt with the timestamp and keeps message actions below the body.

Settings provides System, Light and Dark appearance choices for the current browser. Installation reads the running daemon version from /api/health. Font files are served locally.

## Character assets

The greeting and searching WebP derivatives come from the corresponding SVG poses in the supplied kit. To reproduce them, resize each source SVG to 380 by 380, trim with threshold 8 and encode WebP at quality 90. The small product icon derives from public/favicon.svg, resized to 64 by 64 and encoded as PNG.
