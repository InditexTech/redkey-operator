# Redkey professional brand kit

Final approved identity based on the L2 direction: cobalt pod outline, compact signal-red key, two equal close-set teeth, and a shortened lower stem.

## Package structure

- `01-master-svg/` — production master logos with outlined lettering.
- `02-png-transparent/` — transparent PNG exports at practical 1x/2x sizes.
- `03-web-icons/` — favicon, Apple touch, Android/PWA, maskable icon, web manifest, and ICO.
- `04-social/` — avatars and Open Graph cards on light and dark backgrounds.
- `05-guidelines/` — overview, color palette, and clear-space artwork in SVG and PNG.

## Logo configurations

- Icon only: outline and solid small-size version.
- Wordmark only.
- Horizontal: icon left plus wordmark.
- Horizontal with `Operator` descriptor.
- Stacked: icon above wordmark.
- Stacked with `Operator` descriptor.
- Full color, one-color black, and reverse-on-dark versions.

## Core colors

| Name | Hex | Usage |
| --- | --- | --- |
| Signal Red | `#E5392D` | Key symbol and `Red` |
| Kubernetes Blue | `#326CE5` | Pod outline, `key`, and `Operator` descriptor |
| Deep Navy | `#0B1F3A` | Dark backgrounds and reverse panels |
| Charcoal | `#263238` | One-color and monochrome artwork |
| White | `#FFFFFF` | Light backgrounds and reverse artwork |

## Usage rules

- Use the outline icon at 32 px or larger.
- Use `favicon.svg` or the solid icon below 32 px.
- Preserve clear space equal to the circular key counter on every side.
- Prefer the horizontal logo for navigation, documentation headers, and repository pages.
- Prefer the stacked logo for square placements and event graphics.
- Use reverse artwork only on dark navy, cobalt, or similarly dark backgrounds.
- Do not rotate the mark, alter the tooth spacing, recolor the key white, add effects, or place the logo inside another badge.

## Technical notes

- Master SVG wordmarks are converted to paths; no font installation is required.
- PNG exports use transparent backgrounds unless the filename explicitly says `white`, `blue`, or `dark`.
- Browser and app icons include safe-area-aware artwork.
- The `redkey-brand.css` file provides reusable color tokens.

