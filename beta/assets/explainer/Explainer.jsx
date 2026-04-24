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
    lede: 'Sandal binds the live host as the base. The container sees the tools already on disk.',
    bullets: [
      'Quick experiments on the machine you are already sitting on.',
      'Changes stay separate so the host is untouched.',
      'Pairs with snapshot — save your changes when you are done.',
    ],
  },
  {
    title: 'Mount a local image file',
    cmd: 'sandal run -lw ./alpine.sqfs -- sh',
    tone: 'wood',
    lede: 'Point at any image file on disk — useful for air-gapped machines and reproducible base images.',
    bullets: [
      'Compressed and read-only — fast boot, small footprint.',
      'Share one image across many containers.',
      'Works without a network.',
    ],
  },
  {
    title: 'Mount a USB or NVMe drive',
    cmd: 'sandal run -lw /dev/nvme1n1 -- sh',
    tone: 'wood',
    lede: 'Use a block device directly as the base. Great for field rescue, portable toolchains, or recovering a broken install from a known-good USB.',
    bullets: [
      'Any standard filesystem works.',
      'Hot-pluggable: unplug to release; re-plug to run again, changes are preserved.',
      'Drive is read-only; your changes stay separate.',
    ],
  },
  {
    title: 'Pull from a registry',
    cmd: 'sandal run -lw python:3.12 -- python',
    tone: 'water',
    lede: 'Any OCI or Docker registry works. Layers are cached — subsequent runs start instantly.',
    bullets: [
      'docker.io, ghcr.io, quay.io, self-hosted — all the usual places.',
      'Pass -lw multiple times to stack images in order.',
      'No background daemon; pulls happen on demand.',
    ],
  },
  {
    title: 'Resume with a persistent snapshot',
    cmd: 'sandal snapshot myapp  #  then  sandal run -lw / -name myapp',
    tone: 'water',
    lede: 'sandal snapshot captures your changes. Run again with the same -name and they are mounted back — you resume where you left off.',
    bullets: [
      'Only your changes are captured; the base image stays swappable.',
      'Include or exclude paths with -i / -e.',
      'Chainable: each snapshot merges with fresh changes.',
    ],
  },
  {
    title: 'Wrap it all in a VM',
    cmd: 'sandal run --vm -lw python:3.12 -- python',
    tone: 'water',
    lede: 'Boot the same setup inside a microVM when you need hardware isolation. Same command, stronger boundary.',
    bullets: [
      'Fresh kernel per run — no shared host kernel.',
      'Layers assemble the usual way, just inside the guest.',
      'Good for untrusted code, multi-tenant builds, or kernel experiments.',
    ],
  },
  {
    title: 'Share a host directory with -v',
    cmd: 'sandal run -lw python:3.12 -v ./data:/data -- python app.py',
    tone: 'wood',
    lede: 'The -v flag connects a host path straight into the container. No copy — changes land on the host immediately.',
    bullets: [
      'Keep source code, model weights, or datasets editable on the host.',
      'Format is HOST:GUEST — append :ro for read-only.',
      'Mix freely with any number of -lw layers.',
    ],
  },
  {
    title: 'Listen on the host, forward to the container',
    cmd: 'sandal run -lw code-server -p tls://0.0.0.0:8080:unix://vscode -- code-server',
    tone: 'water',
    lede: 'The -p flag binds a listener on the host and forwards traffic in. TLS terminates on the host; the container sees plain traffic.',
    bullets: [
      'Host side: TCP, UDP, TLS, or a unix socket.',
      'Container side: port or unix socket — cross-protocol works.',
      'Same flag in VM mode; traffic tunnels over vsock.',
    ],
  },
  {
    title: 'Combine any of them',
    cmd: 'sandal run -lw / -lw python:3.12 -v ./data:/data -snapshot ./myapp.sqfs -- python',
    tone: 'water',
    lede: 'Stack sources with -lw, share paths with -v, persist with -snapshot, add --vm for isolation. One command, every composition you need.',
    bullets: [
      'Put a fast-changing app layer above a stable base.',
      'Pin a vendored binary by dropping it into the top layer.',
      'Share hot-reload data or large datasets without building them into layers.',
    ],
  },
];

function useActiveFeature(refs) {
  // Scroll-position-based detection. Active = the LAST section whose top has
  // scrolled past a reading line ~30% from the top of the viewport. Simpler
  // and more predictable than IntersectionObserver ratios — notably, the
  // final section reliably becomes active at page bottom even when padding
  // can't push its top any higher into the reading band.
  const [active, setActive] = React.useState(0);
  React.useEffect(() => {
    const recompute = () => {
      const line = window.innerHeight * 0.3;
      let best = 0;
      for (let i = 0; i < refs.current.length; i++) {
        const el = refs.current[i];
        if (!el) continue;
        if (el.getBoundingClientRect().top <= line) best = i;
      }
      setActive(best);
    };
    recompute();
    window.addEventListener('scroll', recompute, { passive: true });
    window.addEventListener('resize', recompute);
    return () => {
      window.removeEventListener('scroll', recompute);
      window.removeEventListener('resize', recompute);
    };
  }, []);
  return active;
}

function useScrolled(threshold) {
  // True once the user has scrolled past `threshold` pixels from the top.
  // Gates the bottom-panel reveal and the scroll-cue fade — a first-time
  // visitor sees a clean hero, the panel slides up as they start exploring.
  const [scrolled, setScrolled] = React.useState(false);
  React.useEffect(() => {
    const check = () => setScrolled(window.scrollY > threshold);
    check();
    window.addEventListener('scroll', check, { passive: true });
    return () => window.removeEventListener('scroll', check);
  }, [threshold]);
  return scrolled;
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

function IntroBlock({ innerRef, scrolled, onCueClick }) {
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
      <button
        type="button"
        onClick={onCueClick}
        aria-label="Scroll down to explore"
        className={'explainer-scroll-cue' + (scrolled ? ' is-hidden' : '')}
      >
        <span>scroll to explore</span>
        <span className="explainer-scroll-cue-arrow" aria-hidden="true">↓</span>
      </button>
    </section>
  );
}

function TriggerSection({ innerRef, index, feature, active, past }) {
  // Each trigger section contains the full feature description (title, cmd,
  // lede, bullets). The BottomPanel below shows only the morphing diagram.
  const palette = feature.tone === 'water' ? 'var(--water-deep)' : 'var(--wood-deep)';
  return (
    <section
      ref={innerRef}
      data-feature-index={index}
      style={{
        minHeight: '60vh',
        paddingTop: 32,
        paddingBottom: 32,
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
        margin: '0 0 18px',
        fontFamily: 'var(--font-sans)', fontSize: 32,
        fontWeight: 500, color: 'var(--ink)',
        lineHeight: 1.2, maxWidth: 680, textWrap: 'balance',
      }}>
        {feature.title}
      </h3>
      {feature.cmd ? (
        <code style={{
          display: 'inline-block',
          fontFamily: 'var(--font-mono)', fontSize: 13, color: 'var(--ink)',
          background: 'color-mix(in srgb, var(--oar) 40%, var(--paper))',
          border: '1px solid var(--wood-light)',
          padding: '5px 10px', borderRadius: 2,
          marginBottom: 16,
          maxWidth: '100%', overflowX: 'auto', whiteSpace: 'nowrap',
        }}>{feature.cmd}</code>
      ) : null}
      <div style={{ maxWidth: 640 }}>
        <FeatureBody lede={feature.lede} bullets={feature.bullets} tone={feature.tone}/>
      </div>
    </section>
  );
}

function BottomPanel({ active, revealed }) {
  // Fixed-bottom panel — contains only the morphing diagram. Text
  // descriptions for each feature live inline in the trigger sections.
  // `revealed` toggles a slide-up reveal so the panel stays offscreen
  // until the visitor starts scrolling, keeping the hero clean.
  return (
    <aside
      className={'explainer-bottom-panel' + (revealed ? ' is-revealed' : '')}
      aria-hidden={!revealed}
    >
      <div className="explainer-bottom-inner">
        <LiveDiagram feature={active}/>
      </div>
    </aside>
  );
}

function SandalExplainer() {
  const refs = React.useRef([]);
  const setRef = (i) => (el) => { refs.current[i] = el; };
  const active = useActiveFeature(refs);
  const scrolled = useScrolled(80);

  const handleCueClick = () => {
    const target = refs.current[1];
    if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  return (
    <div className="explainer" data-screen-label="00 How-it-works">
      <div className="explainer-scroll">
        <IntroBlock innerRef={setRef(0)} scrolled={scrolled} onCueClick={handleCueClick}/>
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
      <BottomPanel active={active} revealed={scrolled}/>
    </div>
  );
}

window.SandalExplainer = SandalExplainer;
