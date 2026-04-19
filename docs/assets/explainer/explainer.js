// Idempotent bootstrap for the Sandal homepage explainer.
//
// Why idempotent: mkdocs-material's `navigation.instant` SPA-style navigation
// can re-evaluate the homepage's inline <script> tags when a user navigates
// away and back to `/`. Without a guard, we'd call `ReactDOM.createRoot` on
// an already-mounted element, which React warns about.
//
// Why the retry loop: @babel/standalone transpiles every `type="text/babel"`
// script AFTER `DOMContentLoaded` fires, which is when this script runs.
// We poll briefly for `window.SandalExplainer` to appear (up to ~2s) before
// giving up with a console warning.
(function bootstrap(attempts) {
  attempts = attempts === undefined ? 40 : attempts;
  var el = document.getElementById('sandal-explainer');
  if (!el) return;                            // not on this page
  if (el.dataset.mounted === '1') return;     // already mounted
  if (!window.React || !window.ReactDOM || !window.SandalExplainer) {
    if (attempts > 0) {
      window.setTimeout(function () { bootstrap(attempts - 1); }, 50);
    } else {
      // eslint-disable-next-line no-console
      console.warn('[sandal] explainer: React / components failed to load in time');
    }
    return;
  }
  el.dataset.mounted = '1';
  window.ReactDOM
    .createRoot(el)
    .render(window.React.createElement(window.SandalExplainer));
})();
