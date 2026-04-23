// Mermaid initialization for mkdocs-material.
//
// mkdocs-material 9.x has a built-in mermaid auto-loader, but its bundle
// bakes in an absolute URL based on `site_url`, which can fail to fetch in
// local-dev / preview environments. Loading mermaid directly via
// `extra_javascript` keeps the path relative to the current site and works
// regardless of where the docs are served from.
//
// The pymdownx.superfences fence emits:
//   <pre class="mermaid"><code>graph LR...</code></pre>
// Mermaid's default v11 scanner looks for elements with class "mermaid" and
// reads their textContent, so this structure works out of the box.

(function () {
  function initMermaid() {
    if (typeof window.mermaid === 'undefined') return;
    var prefersDark =
      document.body.getAttribute('data-md-color-scheme') === 'slate' ||
      window.matchMedia('(prefers-color-scheme: dark)').matches;
    window.mermaid.initialize({
      startOnLoad: false,
      theme: prefersDark ? 'dark' : 'default',
      securityLevel: 'loose',
    });
    // Find unrendered diagrams and run them.
    var nodes = document.querySelectorAll('pre.mermaid:not([data-processed])');
    if (nodes.length === 0) return;
    window.mermaid.run({ nodes: Array.prototype.slice.call(nodes) });
  }

  // Initial load.
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initMermaid);
  } else {
    initMermaid();
  }

  // mkdocs-material's `navigation.instant` swaps page content without
  // re-running inline scripts. Subscribe to `document$` so we re-init on
  // every page navigation.
  if (typeof window.document$ !== 'undefined' &&
      typeof window.document$.subscribe === 'function') {
    window.document$.subscribe(function () { initMermaid(); });
  }
})();
