// Homepage explainer — scroll-driven morphing diagram with bottom-pinned panel.
//
// Layout:
//   ┌────────────────────────────────────────────────────┐
//   │                                                    │
//   │   [ Intro text, scrolls normally ]                 │
//   │                                                    │
//   │   [ Trigger section: feature 01 ]                  │
//   │   [ Trigger section: feature 02 ]                  │
//   │   [ ... 8 total trigger sections ]                 │
//   │                                                    │
//   ├────────────────────────────────────────────────────┤ <- pinned
//   │ [ LiveDiagram ]   [ FeatureHead + FeatureBody ]    │ 40vh,
//   └────────────────────────────────────────────────────┘ position:fixed
//
// Active index is determined by whichever trigger section crosses the reading
// band (top 20% to 45% of viewport height — above where the bottom panel sits).

const FEATURES = [
  // Index 0 is the intro block (no content; BottomPanel shows idle state).
  null,
  {
    title: 'Use the running host as the base',
    cmd: 'sandal run -lw / -- sh',
    tone: 'wood',
    lede: 'No pull, no archive — Sandal just binds the live host as the lower layer. Your container sees the exact tools already on disk.',
    bullets: [
      'Ideal for quick experiments on the machine you are already sitting on.',
      'Writes go into an upper dir so the host stays untouched.',
      'Good pair with snapshot — capture the changedir when you are done.',
    ],
  },
  {
    title: 'Mount a local image file',
    cmd: 'sandal run -lw ./alpine.sqfs -- sh',
    tone: 'wood',
    lede: 'Point at any squashfs, rootfs tarball, or directory on disk — useful for air-gapped machines and reproducible base images.',
    bullets: [
      'Squashfs is compressed and mounted read-only — fast boot, small disk footprint.',
      'Share one image across dozens of dimensions; each gets its own upper dir.',
      'Works without a network at all.',
    ],
  },
  {
    title: 'Mount a USB or NVMe drive',
    cmd: 'sandal run -lw /dev/nvme1n1 -- sh',
    tone: 'wood',
    lede: 'Plug in a block device and use it directly as a lower layer. Great for field rescue, portable toolchains, or recovering a broken install from a known-good USB.',
    bullets: [
      'Any mountable filesystem works — ext4, xfs, btrfs, squashfs on a partition.',
      'Hot-pluggable: unplug to release; re-plug to run again, upper dir is preserved.',
      'The block device is attached read-only; your overlay writes stay in the upper dir.',
    ],
  },
  {
    title: 'Pull from a registry',
    cmd: 'sandal run -lw python:3.12 -- python',
    tone: 'water',
    lede: 'Any OCI or Docker registry works. Pulled layers get flattened into a squashfs and cached — subsequent runs start instantly.',
    bullets: [
      'docker.io, ghcr.io, quay.io, self-hosted — all the usual places.',
      'Pass the -lw flag multiple times to stack images in order.',
      'No daemon runs in the background; pulls happen on demand.',
    ],
  },
  {
    title: 'Resume with a persistent snapshot',
    cmd: 'sandal snapshot myapp  #  then  sandal run -lw / -name myapp',
    tone: 'water',
    lede: 'sandal snapshot captures just the upperdir (your writes from the last run) as a squashfs. The next time you sandal run with the same -name, that snapshot auto-mounts as the lowest lower — so you resume exactly where you left off.',
    bullets: [
      'Only the upperdir is captured — the base image stays external and swappable.',
      'Use -i / -e to include or exclude paths and keep caches out of the image.',
      'Chains: successive snapshots merge the previous .sqfs with fresh writes.',
    ],
  },
  {
    title: 'Wrap it all in a VM',
    cmd: 'sandal run --vm -lw python:3.12 -- python',
    tone: 'water',
    lede: 'For workloads that need hardware isolation, Sandal can boot the same overlay stack inside a Firecracker microVM. Your dimension sits inside a disposable kernel — same command, stronger boundary.',
    bullets: [
      'Fresh kernel + initrd per run — no shared host kernel surface.',
      'overlayfs still assembles the lower layers the usual way, just inside the guest.',
      'Good for running untrusted code, multi-tenant builds, or kernel-module experiments.',
    ],
  },
  {
    title: 'Share a host directory with -v',
    cmd: 'sandal run -lw python:3.12 -v ./data:/data -- python app.py',
    tone: 'wood',
    lede: 'The -v flag bind-mounts a host path straight through into the dimension — no lower, no upperdir, no copy. Writes land on the host immediately.',
    bullets: [
      'Keep source code, model weights, or datasets editable on the host.',
      'Format is the familiar HOST:GUEST — append :ro to make it read-only.',
      'Mix freely with any number of -lw layers; -v is orthogonal to the stack.',
    ],
  },
  {
    title: 'Combine any of them',
    cmd: 'sandal run -lw / -lw python:3.12 -v ./data:/data -snapshot ./myapp.sqfs -- python',
    tone: 'water',
    lede: 'Stack any sources with -lw, pass through host paths with -v, persist writes with -snapshot, and add --vm for hardware isolation. One command, one process tree, every composition you need.',
    bullets: [
      'Put a fast-changing app layer above a stable base.',
      'Pin a vendored binary by dropping it into the top-most lower.',
      'Bind mount hot-reload data or large datasets that would be wasteful in a lower.',
    ],
  },
];

function useActiveFeature(refs) {
  // IntersectionObserver-based detection. Each trigger section (including the
  // intro at index 0) fires callbacks as it enters/leaves the "reading band"
  // — the slice of viewport above where the BottomPanel sits.
  //
  // rootMargin '-20% 0px -55% 0px' = trigger band from 20% to 45% of viewport
  // height. The bottom 55% is ignored so sections scrolling under the pinned
  // BottomPanel don't drive state.
  const [active, setActive] = React.useState(0);
  React.useEffect(() => {
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
      if (bestRatio > 0) setActive(best);
    };
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) ratios.set(e.target, e.intersectionRatio);
        recompute();
      },
      {
        rootMargin: '-20% 0px -55% 0px',
        threshold: [0, 0.25, 0.5, 0.75, 1],
      }
    );
    refs.current.forEach(el => el && io.observe(el));
    return () => io.disconnect();
  }, []);
  return active;
}

function FeatureHead({ title, cmd }) {
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', alignItems: 'flex-start',
      gap: 10,
      padding: '0 0 10px', marginBottom: 14,
      borderBottom: '1px solid var(--wood-light)',
    }}>
      <h3 style={{
        margin: 0, fontFamily: 'var(--font-sans)', fontSize: 20,
        fontWeight: 500, color: 'var(--ink)',
        lineHeight: 1.2, textWrap: 'balance',
      }}>{title}</h3>
      {cmd ? (
        <code style={{
          fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--ink)',
          background: 'color-mix(in srgb, var(--oar) 40%, var(--paper))',
          border: '1px solid var(--wood-light)',
          padding: '4px 8px', borderRadius: 2,
          maxWidth: '100%',
          overflowX: 'auto',
          whiteSpace: 'nowrap',
        }}>{cmd}</code>
      ) : null}
    </div>
  );
}

function FeatureBody({ lede, bullets, tone }) {
  return (
    <div>
      <p style={{
        margin: '0 0 14px', fontFamily: 'var(--font-sans)', fontSize: 14,
        color: 'var(--ink-soft)', lineHeight: 1.5, textWrap: 'pretty',
      }}>{lede}</p>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        {bullets.map((b, i) => (
          <div key={i} style={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: 10,
            fontFamily: 'var(--font-sans)', fontSize: 13,
            color: 'var(--ink-soft)', lineHeight: 1.5,
          }}>
            <span style={{
              flexShrink: 0,
              marginTop: 7, width: 5, height: 5, borderRadius: 1,
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
    <section ref={innerRef}
      style={{ minHeight: '40vh', paddingBottom: 32, scrollMarginTop: 72 }}
      data-screen-label="00 Intro">
      <div style={{
        fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: '0.12em',
        color: 'var(--wood-deep)', textTransform: 'uppercase', marginBottom: 10,
      }}>How it works</div>
      <h2 style={{
        fontFamily: 'var(--font-sans)', fontSize: 30, lineHeight: 1.15,
        color: 'var(--ink)', margin: '0 0 12px', maxWidth: 560, textWrap: 'balance',
      }}>
        A <em style={{ fontStyle: 'normal', color: 'var(--wood-deep)' }}>dimension</em> is
        an isolated filesystem — assembled from whichever layers you choose.
      </h2>
      <p style={{
        maxWidth: 560, fontFamily: 'var(--font-sans)', fontSize: 15,
        color: 'var(--ink-soft)', lineHeight: 1.6, margin: '0 0 16px',
      }}>
        Scroll to watch the diagram at the bottom swap its source, restack its
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

function TriggerSection({ innerRef, index, feature, active, past }) {
  // Each feature gets a trigger section that takes up scroll real estate and
  // shows a large visual marker — a numbered label plus the feature title. The
  // deep explanation lives in the BottomPanel below. This keeps the main
  // content scrollable for users who prefer reading in flow, and the pinned
  // panel in sync with whichever feature sits in the reading band.
  const palette = feature.tone === 'water' ? 'var(--water-deep)' : 'var(--wood-deep)';
  return (
    <section
      ref={innerRef}
      data-feature-index={index}
      style={{
        minHeight: '60vh',
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        paddingBottom: 24,
        scrollMarginTop: 72,
        opacity: active ? 1 : (past ? 0.55 : 0.85),
        filter: active ? 'none' : 'saturate(0.85)',
        transition: 'opacity 500ms, filter 500ms',
      }}>
      <div style={{
        fontFamily: 'var(--font-mono)', fontSize: 12, letterSpacing: '0.12em',
        color: palette, textTransform: 'uppercase', marginBottom: 10,
      }}>
        Feature {String(index).padStart(2, '0')}
      </div>
      <h3 style={{
        margin: 0, fontFamily: 'var(--font-sans)', fontSize: 32,
        fontWeight: 500, color: 'var(--ink)',
        lineHeight: 1.2, maxWidth: 640, textWrap: 'balance',
      }}>
        {feature.title}
      </h3>
    </section>
  );
}

function BottomPanel({ active }) {
  // Fixed-bottom panel. Always visible; morphs contents as `active` changes.
  // For the intro state (active === 0) the diagram shows its idle frame and
  // the copy side is a brief prompt.
  const feat = FEATURES[active];
  return (
    <aside className="explainer-bottom-panel" aria-live="polite">
      <div className="explainer-bottom-inner">
        <div className="explainer-bottom-diagram">
          <LiveDiagram feature={active}/>
        </div>
        <div className="explainer-bottom-copy">
          {feat ? (
            <>
              <FeatureHead title={feat.title} cmd={feat.cmd}/>
              <FeatureBody lede={feat.lede} bullets={feat.bullets} tone={feat.tone}/>
            </>
          ) : (
            <div style={{
              fontFamily: 'var(--font-sans)', fontSize: 15, color: 'var(--mute)',
              lineHeight: 1.5, maxWidth: 420, textWrap: 'pretty',
            }}>
              <div style={{
                fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: '0.12em',
                color: 'var(--wood-deep)', textTransform: 'uppercase', marginBottom: 8,
              }}>
                Idle
              </div>
              Scroll down to activate each feature. The diagram swaps its source,
              overlay stack, and resulting dimension in sync with the feature
              under the reading band.
            </div>
          )}
        </div>
      </div>
    </aside>
  );
}

function SandalExplainer() {
  const refs = React.useRef([]);
  const setRef = (i) => (el) => { refs.current[i] = el; };
  const active = useActiveFeature(refs);

  return (
    <div className="explainer" data-screen-label="00 How-it-works">
      <div className="explainer-scroll">
        <IntroBlock innerRef={setRef(0)}/>
        {FEATURES.slice(1).map((feature, i) => {
          const index = i + 1;
          return (
            <TriggerSection
              key={index}
              innerRef={setRef(index)}
              index={index}
              feature={feature}
              active={active === index}
              past={active > index}
            />
          );
        })}
      </div>
      <BottomPanel active={active}/>
    </div>
  );
}

window.SandalExplainer = SandalExplainer;
