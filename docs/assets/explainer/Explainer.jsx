// Homepage explainer — scroll-driven morphing diagram.
//
// Layout:
//   ┌─── left col (sticky) ───┬─── right col (scrolling) ───┐
//   │  [ LiveDiagram          │  Intro                       │
//   │    morphing as you      │                              │
//   │    scroll past the      │  ── Host as base             │
//   │    features on right ]  │  ── Local image file         │
//   │                         │  ── Registry pulls           │
//   │                         │  ── Combine                  │
//   └─────────────────────────┴──────────────────────────────┘
//
// The left pane is position:sticky and rerenders whenever the
// currently-active feature on the right changes. Active index is
// determined by whichever feature-section crosses mid-viewport.

function useActiveFeature(refs) {
  // IntersectionObserver-based detection. The browser fires callbacks whenever
  // a section enters/leaves a narrow horizontal "reading band" in the middle
  // of the viewport. We pick whichever section currently has the most of its
  // area inside that band. Extensible: to add a new feature, just register a
  // ref — no math changes needed.
  const [active, setActive] = React.useState(0);
  React.useEffect(() => {
    // Track each section's current intersection ratio. We recompute the
    // active section from this map every time an entry changes.
    const ratios = new Map();
    const recompute = () => {
      let best = 0;
      let bestRatio = 0;
      for (let i = 0; i < refs.current.length; i++) {
        const el = refs.current[i];
        if (!el) continue;
        const r = ratios.get(el) ?? 0;
        if (r > bestRatio) { bestRatio = r; best = i; }
      }
      // Only commit if SOMETHING is in the band (ratio > 0). Otherwise leave
      // the previous active unchanged so the diagram doesn't flicker to 0
      // between sections.
      if (bestRatio > 0) setActive(best);
    };
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) ratios.set(e.target, e.intersectionRatio);
        recompute();
      },
      {
        // Reading band: middle ~25% of the viewport (30% top margin,
        // 45% bottom margin). Sits cleanly above the bottom-pinned diagram.
        rootMargin: '-30% 0px -45% 0px',
        threshold: [0, 0.25, 0.5, 0.75, 1],
      }
    );
    refs.current.forEach(el => el && io.observe(el));
    return () => io.disconnect();
  }, []);
  return active;
}

function FeatureHead({ title, cmd, active, past }) {
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', alignItems: 'flex-start',
      gap: 10,
      padding: '0 0 10px', marginBottom: 14,
      borderBottom: `1px solid ${active ? 'var(--wood-light)' : 'var(--rule)'}`,
      transition: 'border-color 400ms',
    }}>
      <h3 style={{
        margin: 0, fontFamily: 'var(--font-sans)', fontSize: 22,
        fontWeight: 500, color: 'var(--ink)',
        lineHeight: 1.2, textWrap: 'balance',
      }}>{title}</h3>
      {cmd ? (
        <code style={{
          fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--ink)',
          background: active ? 'color-mix(in srgb, var(--oar) 40%, var(--paper))' : 'var(--paper)',
          border: `1px solid ${active ? 'var(--wood-light)' : 'var(--rule)'}`,
          padding: '4px 8px', borderRadius: 2,
          transition: 'background 400ms, border-color 400ms',
          maxWidth: '100%',
          overflowX: 'auto',
          whiteSpace: 'nowrap',
        }}>{cmd}</code>
      ) : null}
    </div>
  );
}

function Feature({ innerRef, active, past, title, cmd, children }) {
  return (
    <section
      ref={innerRef}
      style={{
        display: 'flex', flexDirection: 'column',
        minHeight: '70vh',
        justifyContent: 'center',
        paddingBottom: 48,
        scrollMarginTop: 72,
        opacity: past && !active ? 0.55 : 1,
        filter: past && !active ? 'saturate(0.7)' : 'none',
        transition: 'opacity 500ms, filter 500ms',
      }}>
      <FeatureHead title={title} cmd={cmd} active={active} past={past}/>
      <div>{children}</div>
    </section>
  );
}

function FeatureBody({ lede, bullets, tone }) {
  return (
    <div style={{ maxWidth: 460 }}>
      <p style={{
        margin: '0 0 20px', fontFamily: 'var(--font-sans)', fontSize: 15,
        color: 'var(--ink-soft)', lineHeight: 1.6, textWrap: 'pretty',
      }}>{lede}</p>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
        {bullets.map((b, i) => (
          <div key={i} style={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: 12,
            fontFamily: 'var(--font-sans)', fontSize: 14,
            color: 'var(--ink-soft)', lineHeight: 1.55,
          }}>
            <span style={{
              flexShrink: 0,
              marginTop: 8, width: 6, height: 6, borderRadius: 1,
              background: tone === 'water' ? 'var(--water-deep)' : 'var(--wood)',
            }}/>
            <span style={{ flex: 1, minWidth: 0 }}>{b}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function IntroBlock({ innerRef }) {
  return (
    <section ref={innerRef} style={{ minHeight: '60vh', paddingBottom: 64, scrollMarginTop: 72 }}
      data-screen-label="00 Intro">
      <div style={{
        fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: '0.12em',
        color: 'var(--wood-deep)', textTransform: 'uppercase', marginBottom: 10,
      }}>How it works</div>
      <h2 style={{
        fontFamily: 'var(--font-sans)', fontSize: 30, lineHeight: 1.15,
        color: 'var(--ink)', margin: '0 0 12px', maxWidth: 480, textWrap: 'balance',
      }}>
        A <em style={{ fontStyle: 'normal', color: 'var(--wood-deep)' }}>dimension</em> is
        an isolated filesystem — assembled from whichever layers you choose.
      </h2>
      <p style={{
        maxWidth: 460, fontFamily: 'var(--font-sans)', fontSize: 15,
        color: 'var(--ink-soft)', lineHeight: 1.6, margin: '0 0 16px',
      }}>
        Scroll to watch the diagram on the left swap its source, restack its
        lower layers, and light up the resulting dimension. Stack any sources
        with <code>-lw</code>, pass through host paths with <code>-v</code>,
        persist writes with <code>snapshot</code>, and add <code>--vm</code>
        for hardware isolation.
      </p>
      <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--mute)' }}>
        ↓ scroll to explore
      </div>
    </section>
  );
}

function SandalExplainer() {
  const refs = React.useRef([]);
  const setRef = (i) => (el) => { refs.current[i] = el; };
  const active = useActiveFeature(refs);

  return (
    <div className="explainer" data-screen-label="00 How-it-works">
      <div style={layoutStyles.grid}>
        {/* LEFT column: sticky diagram */}
        <aside style={layoutStyles.diagramCol}>
          <div style={layoutStyles.stickyInner}>
            <LiveDiagram feature={active}/>
          </div>
        </aside>

        {/* RIGHT column: scrolling feature sections */}
        <div style={layoutStyles.scrollCol}>
          <IntroBlock innerRef={setRef(0)}/>

          <Feature innerRef={setRef(1)} active={active === 1} past={active > 1}
            title="Use the running host as the base"
            cmd="sandal run -lw / -- sh">
            <FeatureBody tone="wood"
              lede="No pull, no archive — Sandal just binds the live host as the lower layer. Your container sees the exact tools already on disk."
              bullets={[
                'Ideal for quick experiments on the machine you are already sitting on.',
                'Writes go into an upper dir so the host stays untouched.',
                'Good pair with snapshot — capture the changedir when you are done.',
              ]}/>
          </Feature>

          <Feature innerRef={setRef(2)} active={active === 2} past={active > 2}
            title="Mount a local image file"
            cmd="sandal run -lw ./alpine.sqfs -- sh">
            <FeatureBody tone="wood"
              lede="Point at any squashfs, rootfs tarball, or directory on disk — useful for air-gapped machines and reproducible base images."
              bullets={[
                'Squashfs is compressed and mounted read-only — fast boot, small disk footprint.',
                'Share one image across dozens of dimensions; each gets its own upper dir.',
                'Works without a network at all.',
              ]}/>
          </Feature>

          <Feature innerRef={setRef(3)} active={active === 3} past={active > 3}
            title="Mount a USB or NVMe drive"
            cmd="sandal run -lw /dev/nvme1n1 -- sh">
            <FeatureBody tone="wood"
              lede="Plug in a block device and use it directly as a lower layer. Great for field rescue, portable toolchains, or recovering a broken install from a known-good USB."
              bullets={[
                'Any mountable filesystem works — ext4, xfs, btrfs, squashfs on a partition.',
                'Hot-pluggable: unplug to release; re-plug to run again, upper dir is preserved.',
                'The block device is attached read-only; your overlay writes stay in the upper dir.',
              ]}/>
          </Feature>

          <Feature innerRef={setRef(4)} active={active === 4} past={active > 4}
            title="Pull from a registry"
            cmd="sandal run -lw python:3.12 -- python">
            <FeatureBody tone="water"
              lede="Any OCI or Docker registry works. Pulled layers get flattened into a squashfs and cached — subsequent runs start instantly."
              bullets={[
                'docker.io, ghcr.io, quay.io, self-hosted — all the usual places.',
                'Pass the -lw flag multiple times to stack images in order.',
                'No daemon runs in the background; pulls happen on demand.',
              ]}/>
          </Feature>

          <Feature innerRef={setRef(5)} active={active === 5} past={active > 5}
            title="Resume with a persistent snapshot"
            cmd="sandal snapshot myapp  #  then  sandal run -lw / -name myapp">
            <FeatureBody tone="water"
              lede="sandal snapshot captures just the upperdir (your writes from the last run) as a squashfs. The next time you sandal run with the same -name, that snapshot auto-mounts as the lowest lower — so you resume exactly where you left off."
              bullets={[
                'Only the upperdir is captured — the base image stays external and swappable.',
                'Use -i / -e to include or exclude paths and keep caches out of the image.',
                'Chains: successive snapshots merge the previous .sqfs with fresh writes.',
              ]}/>
          </Feature>

          <Feature innerRef={setRef(6)} active={active === 6} past={active > 6}
            title="Wrap it all in a VM"
            cmd="sandal run --vm -lw python:3.12 -- python">
            <FeatureBody tone="water"
              lede="For workloads that need hardware isolation, Sandal can boot the same overlay stack inside a Firecracker microVM. Your dimension sits inside a disposable kernel — same command, stronger boundary."
              bullets={[
                'Fresh kernel + initrd per run — no shared host kernel surface.',
                'overlayfs still assembles the lower layers the usual way, just inside the guest.',
                'Good for running untrusted code, multi-tenant builds, or kernel-module experiments.',
              ]}/>
          </Feature>

          <Feature innerRef={setRef(7)} active={active === 7} past={active > 7}
            title="Share a host directory with -v"
            cmd="sandal run -lw python:3.12 -v ./data:/data -- python app.py">
            <FeatureBody tone="wood"
              lede="The -v flag bind-mounts a host path straight through into the dimension — no lower, no upperdir, no copy. Writes land on the host immediately."
              bullets={[
                'Keep source code, model weights, or datasets editable on the host.',
                'Format is the familiar HOST:GUEST — append :ro to make it read-only.',
                'Mix freely with any number of -lw layers; -v is orthogonal to the stack.',
              ]}/>
          </Feature>

          <Feature innerRef={setRef(8)} active={active === 8} past={active > 8}
            title="Combine any of them"
            cmd="sandal run -lw / -lw python:3.12 -v ./data:/data -snapshot ./myapp.sqfs -- python">
            <FeatureBody tone="water"
              lede="Stack any sources with -lw, pass through host paths with -v, persist writes with -snapshot, and add --vm for hardware isolation. One command, one process tree, every composition you need."
              bullets={[
                'Put a fast-changing app layer above a stable base.',
                'Pin a vendored binary by dropping it into the top-most lower.',
                'Bind mount hot-reload data or large datasets that would be wasteful in a lower.',
              ]}/>
          </Feature>
        </div>
      </div>
    </div>
  );
}

const layoutStyles = {
  grid: {
    display: 'grid',
    gridTemplateColumns: 'minmax(360px, 1fr) minmax(440px, 1fr)',
    gap: 56,
    alignItems: 'stretch', // IMPORTANT: both columns must span the same height
                            // so the sticky child of the left aside has a tall
                            // scroll container to pin against.
    maxWidth: 1200, margin: '0 auto', padding: '24px 28px 120px',
  },
  diagramCol: {
    // The sticky element itself must not be the grid item — it needs a
    // grid cell above it to anchor against. So the column is a normal
    // block, and its child becomes sticky. Height:100% ensures the aside
    // stretches to match the scrollCol's height so sticky works past the
    // first feature section.
    //
    // IMPORTANT: do NOT set overflow:hidden here — any clipping ancestor
    // kills position:sticky on the child. Containment goes on stickyInner
    // instead.
    position: 'relative',
    height: '100%',
    minWidth: 0,
  },
  stickyInner: {
    position: 'sticky',
    top: 28,
    // Containment: clip any widest-state diagram overflow here (not on the
    // sticky ancestor, which would disable sticky entirely).
    maxWidth: 480,
    overflow: 'hidden',
  },
  scrollCol: { minWidth: 0 },
};

window.SandalExplainer = SandalExplainer;
