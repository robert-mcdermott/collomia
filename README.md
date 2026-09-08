# Collomia product site

The static site served at <https://robert-mcdermott.github.io/collomia/>.

This branch (`gh-pages`) is an orphan branch: it shares no history with `main`
and contains only the site. Nothing here is built or generated — GitHub Pages
serves these files directly, and `.nojekyll` keeps Jekyll from touching them.

## Layout

```
index.html              the whole page
assets/styles.css       all styling; palette lives in :root at the top
assets/favicon.svg
assets/collo-screenshot.png   hero image, copied from main:docs/collo-screenshot.png
.nojekyll               required — serve files verbatim
```

## Editing

Open `index.html` in a browser. There is no build step, no dependency, and no
external network request at runtime (no CDN fonts or scripts), so what you see
locally is what Pages serves.

Colors are defined once as custom properties at the top of `assets/styles.css`
and are drawn from the TUI's own palette.

## Adding feature screenshots

Drop the image in `assets/` and reuse the existing `.shot` figure markup, which
already carries the window chrome, border, and caption styling:

```html
<figure class="shot">
  <div class="shot-bar">
    <i></i><i></i><i></i>
    <span class="title">collo — permission dialog</span>
  </div>
  <img src="assets/your-image.png" width="…" height="…" alt="Describe what the image shows.">
  <figcaption>One sentence on what the reader should notice.</figcaption>
</figure>
```

Set `width`/`height` to the image's real pixel dimensions so the browser
reserves the right space while it loads, and always write a real `alt`.

## Keeping it accurate

Claims on this page are taken from `main`'s `README.md`, `docs/FEATURES.md`,
`docs/WORK_MODE.md`, `docs/COMPLETION.md`, `docs/SECURITY.md`, and `docs/BETA.md`. When a capability's status changes in
those documents — particularly anything marked experimental — update the page
to match rather than letting it drift ahead of what ships.


The current page leads with Developer and Work task profiles. Keep these separate
from Standard/Orchestrated execution and permission autonomy. Work uses Standard
execution and ordinary folders; do not add a required sample project or imply
that specialized tools are bundled. Example prompts describe possible requests,
not guaranteed outcomes on every model. Screenshot source: main's tracked
`docs/collo-screenshot.png` (3786 × 2152); the screenshot shows Developer mode.

Before publishing, check desktop and mobile layout, in-page links, the expandable
policy section, and copy buttons (including clipboard failure). Keep assets and
scripts local. A failed clipboard operation must not show a success message.
