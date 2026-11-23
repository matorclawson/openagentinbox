# Shared visual language

The canonical source for this release is the supplied Dreamhuggers visual-language kit at revision `83f1d48289dc3377f61bf321bad8f1456235c577`. Its MIT notice is in web/brand/LICENSE. web/brand/tokens.ts is an unmodified copy of the kit’s source of truth. Use its HSL triples where the descriptive README or sample site stylesheet differs.

The app uses Tailwind 4. From web/, run `node brand/theme.mjs` to regenerate brand/theme.css and `node brand/theme.mjs --check` to verify it. The adapter exports the kit’s color, type, shadow and gradient values as native Tailwind theme variables and applies the preset’s radius scale. It does not import the example site or a second CSS framework.

Inbox, Sent and conversation headings use the shared listing-hero vocabulary: a light 40px grid, a kidney shape, a boomerang, a twelve-ray starburst and the supplied Mator artwork. The sidebar follows the kit’s light card-surface treatment. Orange marks primary actions, teal identifies selections, and receipt text remains explicit. Font files are served locally.

## Character assets

The seven WebP derivatives in web/public/brand come from the like-named 1024px SVG poses in src/assets-local/assets/mator in the supplied kit. The small product icon derives from public/favicon.svg. The inbox uses reading, Sent uses rocket, conversations use chatting, onboarding uses greeting, the empty inbox uses searching, compose uses working and mail setup uses integrating. The illustrations are decorative; buttons and state indicators retain text labels. No continuously running animation is required.

To reproduce the raster derivatives with the app’s installed sharp version, resize each source SVG to 380 by 380, trim with threshold 8 and encode WebP at quality 90. For the product icon, resize the original favicon to 64 by 64 and encode PNG. The public name, installer and routes remain OpenAgentInbox.
