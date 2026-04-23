// Idempotent bootstrap for the Sandal homepage explainer.
//
// Why idempotent: mkdocs-material's `navigation.instant` SPA-style navigation
// swaps the page's <main> content on every in-site link click. Inline <script>
// tags in the swapped-in content are NOT re-executed — but the mount div
// (#sandal-explainer) is freshly recreated in the DOM. We must re-mount on
// every page swap by subscribing to mkdocs-material's `document$` observable.
//
// Why the retry loop: @babel/standalone transpiles every `type="text/babel"`
// script AFTER `DOMContentLoaded` fires. On the initial page load, this
// script runs before the JSX components have finished transpiling, so we
// poll briefly for `window.SandalExplainer` to appear (up to ~2s).
(function () {
  function mount(attempts) {
    attempts = attempts === undefined ? 40 : attempts;
    var el = document.getElementById('sandal-explainer');
    if (!el) return;                            // not on this page
    if (el.dataset.mounted === '1') return;     // already mounted
    if (!window.React || !window.ReactDOM || !window.SandalExplainer) {
      if (attempts > 0) {
        window.setTimeout(function () { mount(attempts - 1); }, 50);
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
  }

  // Initial load: run directly.
  mount();

  // SPA re-navigations: re-run on every page swap.
  // `document$` is an RxJS Subject exposed by mkdocs-material when
  // `navigation.instant` is enabled. It emits the new <document> each time
  // content swaps. On non-instant setups, the Subject is absent and we
  // simply rely on the initial call above.
  if (typeof window.document$ !== 'undefined' &&
      typeof window.document$.subscribe === 'function') {
    window.document$.subscribe(function () { mount(); });
  }
})();
