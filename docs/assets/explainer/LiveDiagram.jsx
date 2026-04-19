// LiveDiagram — a SINGLE sticky visualization that morphs as you scroll.
// It shows one source → overlay stack → dimension, and swaps its
// contents when the active feature changes.
//
// States (feature index):
//   0  intro      — idle: neutral prompt, "choose a source"
//   1  host       — source: host /, overlay with 1 lower
//   2  image      — source: .sqfs file, overlay with 1 lower
//   3  registry   — source: 2 registry discs, overlay with 2 lowers
//   4  combine    — source: host + sqfs + registry, overlay with 3 lowers

function LiveDiagram({ feature }) {
  const state = DIAGRAM_STATES[feature] || DIAGRAM_STATES[0];
  const vm = !!state.vm;

  return (
    <div style={diagStyles.frame}>
      {/* sky / caption strip */}
      <div style={diagStyles.caption}>
        <span style={diagStyles.captionLabel}>{state.label}</span>
        <span style={diagStyles.captionCmd}>{state.cmd}</span>
      </div>

      <div style={diagStyles.canvas}>
        {/* LEFT: source panel — swaps based on state.source */}
        <div style={diagStyles.col}>
          <div style={diagStyles.colHead}>source</div>
          <SourcePanel kind={state.source.kind} items={state.source.items}/>
        </div>

        {/* MIDDLE: overlay stack — grows layers based on state.lowers */}
        <div style={diagStyles.col}>
          <div style={diagStyles.colHead}>overlayfs</div>
          <OverlayStack lowers={state.lowers} binds={state.binds || []}/>
        </div>

        {/* RIGHT: dimension — cube + label, optionally wrapped in VM shroud */}
        <div style={{ ...diagStyles.col, position: 'relative' }}>
          <div style={diagStyles.colHead}>dimension</div>

          {/* VM shroud — only wraps the dimension column */}
          <div style={{
            position: 'absolute',
            top: 18, // below colHead
            left: vm ? -10 : 0,
            right: vm ? -10 : 0,
            bottom: vm ? -10 : 0,
            border: vm ? '1.5px dashed #3e7d8c' : '1.5px dashed transparent',
            borderRadius: 8,
            background: vm
              ? 'color-mix(in srgb, var(--water) 14%, transparent)'
              : 'transparent',
            boxShadow: vm
              ? '0 0 0 1px rgba(62,125,140,0.12) inset, 0 6px 18px rgba(62,125,140,0.14)'
              : 'none',
            transition: 'inset 450ms, background 450ms, border-color 450ms, box-shadow 450ms, left 450ms, right 450ms, bottom 450ms',
            pointerEvents: 'none',
            zIndex: 0,
          }} aria-hidden="true"/>

          {/* VM pill — top-left of the shroud */}
          <div style={{
            position: 'absolute',
            top: vm ? 2 : 18,
            left: vm ? -4 : 0,
            fontFamily: 'var(--font-mono)', fontSize: 10,
            letterSpacing: '0.1em', textTransform: 'uppercase',
            padding: '3px 7px',
            background: '#2a5560',
            color: 'var(--bone)',
            borderRadius: 2,
            opacity: vm ? 1 : 0,
            transform: vm ? 'translateY(0)' : 'translateY(6px)',
            transition: 'opacity 350ms, transform 350ms, top 450ms, left 450ms',
            pointerEvents: 'none',
            whiteSpace: 'nowrap',
            zIndex: 2,
          }} aria-hidden={!vm}>
            VM
          </div>

          <div style={{ position: 'relative', zIndex: 1 }}>
            <DimensionBox name={state.dimensionName} lit={feature > 0}/>
          </div>
        </div>

        {/* Connector arrows laid over with grid-column-spanning */}
        <svg style={diagStyles.connectors} viewBox="0 0 400 200" preserveAspectRatio="none" aria-hidden="true">
          <defs>
            <marker id="livearrow" viewBox="0 0 10 10" refX="8" refY="5"
              markerWidth="7" markerHeight="7" orient="auto">
              <path d="M0,0 L10,5 L0,10 z" fill="var(--mute)"/>
            </marker>
          </defs>
        </svg>
      </div>

      {/* explanation chip row */}
      <div style={diagStyles.chips}>
        {state.chips.map((c, i) => (
          <span key={i} style={{
            fontFamily: 'var(--font-mono)', fontSize: 11,
            color: c.dim ? 'var(--mute)' : 'var(--wood-deep)',
            padding: '3px 8px',
            background: c.dim ? 'var(--paper)' : 'color-mix(in srgb, var(--oar) 40%, var(--paper))',
            border: `1px solid ${c.dim ? 'var(--rule)' : 'var(--wood-light)'}`,
            borderRadius: 2, whiteSpace: 'nowrap',
            transition: 'all 400ms',
          }}>{c.text}</span>
        ))}
      </div>
    </div>
  );
}

// ────────────────────────────────────────────────────────────────
function SourcePanel({ kind, items }) {
  // kind: 'idle' | 'single' | 'multi'
  if (kind === 'idle') {
    return (
      <div style={{...diagStyles.sourceBox, border: '1.5px dashed var(--rule)', color: 'var(--mute)', gap: 6}}>
        <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, lineHeight: 1 }}>↓ scroll</div>
        <div style={{ fontFamily: 'var(--font-sans)', fontSize: 12, lineHeight: 1.2 }}>pick a source</div>
      </div>
    );
  }
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', gap: 8, alignItems: 'stretch',
      width: '100%',
    }}>
      {items.map((it, i) => (
        <div key={it.title + i} style={{
          display: 'flex', alignItems: 'center', gap: 10,
          padding: '10px 12px',
          background: it.tone === 'wood'
            ? 'color-mix(in srgb, var(--oar) 35%, var(--paper))'
            : it.tone === 'water'
              ? 'color-mix(in srgb, var(--water) 22%, var(--paper))'
              : 'var(--paper)',
          border: `1.5px solid ${
            it.tone === 'wood' ? 'var(--wood)' :
            it.tone === 'water' ? 'var(--water-deep)' : 'var(--ink)'
          }`,
          borderRadius: 4,
          boxShadow: '0 1px 0 rgba(26,35,50,0.04), 0 4px 12px rgba(26,35,50,0.04)',
        }}>
          <div style={{ color: it.tone === 'wood' ? 'var(--wood-deep)' : it.tone === 'water' ? '#2a5560' : 'var(--ink)', flexShrink: 0 }}>
            {it.icon === 'host' ? <IconHost size={32}/> :
             it.icon === 'image' ? <IconImage size={32}/> :
             it.icon === 'drive' ? <IconDrive size={32}/> :
             it.icon === 'vm' ? <IconVM size={32}/> :
             <IconRegistry size={32}/>}
          </div>
          <div style={{ minWidth: 0, flex: 1 }}>
            <div style={{ fontFamily: 'var(--font-sans)', fontSize: 13, fontWeight: 500, color: 'var(--ink)' }}>{it.title}</div>
            <div style={{ fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--mute)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{it.ref}</div>
          </div>
        </div>
      ))}
    </div>
  );
}

// ────────────────────────────────────────────────────────────────
function OverlayStack({ lowers, binds = [] }) {
  // lowers: array, top of array = top of stack (upperdir always first)
  // binds:  pass-through mounts (-v), shown below the stack as side-by-side
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8, width: '100%' }}>
      <div style={{
        display: 'flex', flexDirection: 'column', gap: 3, width: '100%',
        padding: 10, background: 'color-mix(in srgb, var(--water) 18%, var(--paper))',
        border: '1.5px solid var(--water-deep)', borderRadius: 4,
      }}>
        <div style={{
          background: 'var(--wood-light)', color: 'var(--wood-deep)',
          fontFamily: 'var(--font-mono)', fontSize: 11, lineHeight: 1.35,
          padding: '7px 10px', borderRadius: 2,
          whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
        }} title="upperdir · writes">upperdir · writes</div>

        {lowers.length === 0 ? (
          <div style={{
            background: 'transparent', color: 'var(--mute)',
            fontFamily: 'var(--font-mono)', fontSize: 11,
            padding: '7px 10px', border: '1px dashed var(--rule)', borderRadius: 2,
            textAlign: 'center',
          }}>awaiting lower…</div>
        ) : null}

        {lowers.map((l, i) => (
          <div key={l.key} style={{
            background: l.tone === 'water' ? 'var(--water)' : 'var(--ink)',
            color: l.tone === 'water' ? '#1a4049' : 'var(--bone)',
            fontFamily: 'var(--font-mono)', fontSize: 11, lineHeight: 1.35,
            padding: '7px 10px', borderRadius: 2,
            display: 'flex', alignItems: 'center', gap: 8,
            whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
            border: l.auto ? '1.5px dashed var(--oar)' : 'none',
            outlineOffset: 1,
          }} title={l.label}>
            <span style={{
              flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis',
            }}>{l.label}</span>
            {l.auto ? (
              <span style={{
                flexShrink: 0,
                fontSize: 9, letterSpacing: '0.1em', textTransform: 'uppercase',
                padding: '1px 6px',
                background: 'var(--oar)',
                color: 'var(--wood-deep)',
                borderRadius: 2,
                fontWeight: 600,
              }}>auto</span>
            ) : null}
          </div>
        ))}
      </div>

      {/* Bind mounts — separate container below the overlay stack */}
      {binds.length > 0 ? (
        <div style={{
          display: 'flex', flexDirection: 'column', gap: 3, width: '100%',
          padding: 10,
          background: 'color-mix(in srgb, var(--oar) 18%, var(--paper))',
          border: '1.5px dashed var(--wood)', borderRadius: 4,
        }}>
          <div style={{
            fontFamily: 'var(--font-mono)', fontSize: 10,
            color: 'var(--wood-deep)', letterSpacing: '0.08em',
            textTransform: 'uppercase', marginBottom: 2,
          }}>bind mounts · pass-through</div>
          {binds.map((b, i) => (
            <div key={b.key} style={{
              display: 'flex', alignItems: 'center', gap: 6,
              background: 'var(--paper)',
              color: 'var(--wood-deep)',
              fontFamily: 'var(--font-mono)', fontSize: 11, lineHeight: 1.35,
              padding: '6px 10px', borderRadius: 2,
              border: '1px solid var(--wood-light)',
              whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
            }} title={`${b.host} → ${b.guest}`}>
              <span style={{ color: 'var(--mute)' }}>host</span>
              <span style={{ color: 'var(--ink)' }}>{b.host}</span>
              <span style={{ color: 'var(--mute)' }}>→</span>
              <span style={{ color: 'var(--ink)' }}>{b.guest}</span>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

// ────────────────────────────────────────────────────────────────
function DimensionBox({ name, lit }) {
  return (
    <div style={{
      display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8,
      padding: 12, width: '100%',
      border: `1.5px solid ${lit ? 'var(--wood)' : 'var(--rule)'}`,
      background: lit ? 'color-mix(in srgb, var(--oar) 20%, var(--paper))' : 'var(--paper)',
      borderRadius: 4,
      transition: 'border-color 500ms, background 500ms',
      opacity: lit ? 1 : 0.55,
    }}>
      <div style={{ color: lit ? 'var(--wood-deep)' : 'var(--mute)' }}>
        <IconCube size={72}/>
      </div>
      <Tag tone="wood">{name}</Tag>
    </div>
  );
}

// ────────────────────────────────────────────────────────────────
// State table — one row per feature index
const DIAGRAM_STATES = [
  {
    label: 'idle',
    cmd: 'sandal run -lw <source> -- …',
    source: { kind: 'idle' },
    lowers: [],
    dimensionName: 'dimension',
    chips: [
      { text: 'overlayfs', dim: true },
      { text: 'chroot + namespaces', dim: true },
      { text: 'no daemon', dim: true },
    ],
  },
  {
    label: 'host root',
    cmd: 'sandal run -lw / -- sh',
    source: { kind: 'single', items: [
      { icon: 'host', title: 'Host root', ref: '/ (running system)', tone: 'ink' },
    ]},
    lowers: [
      { key: 'host', label: 'lower · host /', tone: 'ink' },
    ],
    dimensionName: 'host-derived',
    chips: [
      { text: 'no pull' },
      { text: 'no archive' },
      { text: 'lower = live rootfs' },
    ],
  },
  {
    label: 'local image file',
    cmd: 'sandal run -lw ./alpine.sqfs -- sh',
    source: { kind: 'single', items: [
      { icon: 'image', title: 'Local image', ref: './alpine.sqfs', tone: 'ink' },
    ]},
    lowers: [
      { key: 'alpine', label: 'lower · alpine.sqfs', tone: 'ink' },
    ],
    dimensionName: 'alpine',
    chips: [
      { text: 'squashfs or tarball' },
      { text: 'air-gap friendly' },
      { text: 'reproducible' },
    ],
  },
  {
    label: 'external drive',
    cmd: 'sandal run -lw /dev/nvme1n1 -- sh',
    source: { kind: 'single', items: [
      { icon: 'drive', title: 'USB / NVMe', ref: '/dev/nvme1n1', tone: 'wood' },
    ]},
    lowers: [
      { key: 'dev', label: 'lower · /dev/nvme1n1', tone: 'wood' },
    ],
    dimensionName: 'field-rescue',
    chips: [
      { text: 'block device' },
      { text: 'hot-pluggable' },
      { text: 'read-only mount' },
    ],
  },
  {
    label: 'registry images',
    cmd: 'sandal run -lw busybox -lw python:3.12 -- python',
    source: { kind: 'multi', items: [
      { icon: 'registry', title: 'busybox:latest',    ref: 'docker.io',    tone: 'wood' },
      { icon: 'registry', title: 'python:3.12-slim',  ref: 'ghcr.io',      tone: 'wood' },
    ]},
    lowers: [
      { key: 'py',   label: 'lower · python:3.12-slim', tone: 'water' },
      { key: 'bb',   label: 'lower · busybox:latest',   tone: 'ink' },
    ],
    dimensionName: 'py-on-busybox',
    chips: [
      { text: 'OCI / Docker' },
      { text: '-lw × N' },
      { text: 'last lower wins' },
    ],
  },
  {
    label: 'snapshot · resume',
    cmd: 'sandal run -lw / -name myapp -- sh',
    source: { kind: 'single', items: [
      { icon: 'host', title: 'Host root', ref: '/', tone: 'ink' },
    ]},
    lowers: [
      { key: 'host',    label: 'lower · host /',             tone: 'ink' },
      { key: 'snap',    label: 'lower · myapp.sqfs  (auto)', tone: 'wood', auto: true },
    ],
    dimensionName: 'myapp · resumed',
    chips: [
      { text: 'upperdir \u2192 squashfs' },
      { text: 'auto-mount by -name' },
      { text: 'chainable' },
    ],
  },
  {
    label: 'VM environment',
    cmd: 'sandal run --vm -lw python:3.12 -- python',
    vm: true,  // flag — render blue VM shroud around the dimension only
    source: { kind: 'single', items: [
      { icon: 'registry', title: 'python:3.12-slim', ref: 'ghcr.io', tone: 'wood' },
    ]},
    lowers: [
      { key: 'py',  label: 'lower · python:3.12-slim', tone: 'water' },
    ],
    dimensionName: 'vm-wrapped',
    chips: [
      { text: 'hardware isolation' },
      { text: 'kernel per run' },
      { text: '--vm flag' },
    ],
  },
  {
    label: 'bind mount · -v',
    cmd: 'sandal run -lw python:3.12 -v ./data:/data -- python app.py',
    source: { kind: 'single', items: [
      { icon: 'registry', title: 'python:3.12-slim', ref: 'ghcr.io', tone: 'wood' },
    ]},
    lowers: [
      { key: 'py',  label: 'lower · python:3.12-slim', tone: 'water' },
    ],
    binds: [
      { key: 'data', host: './data', guest: '/data' },
    ],
    dimensionName: 'reads-host-data',
    chips: [
      { text: '-v HOST:GUEST' },
      { text: 'pass-through, not layered' },
      { text: 'read-write by default' },
    ],
  },
  {
    label: 'combined',
    cmd: 'sandal run -lw / -lw python:3.12 -v ./data:/data -snapshot ./myapp.sqfs -- python',
    source: { kind: 'multi', items: [
      { icon: 'host',     title: 'Host root',         ref: '/',             tone: 'ink' },
      { icon: 'image',    title: 'Local image',       ref: './app.sqfs',    tone: 'ink' },
      { icon: 'registry', title: 'python:3.12-slim',  ref: 'ghcr.io',       tone: 'wood' },
    ]},
    lowers: [
      { key: 'py3',  label: 'lower · python:3.12-slim',  tone: 'water' },
      { key: 'app',  label: 'lower · app.sqfs',          tone: 'ink' },
      { key: 'hst',  label: 'lower · host /',            tone: 'ink' },
      { key: 'snap', label: 'lower · myapp.sqfs  (auto)', tone: 'wood', auto: true },
    ],
    binds: [
      { key: 'data', host: './data', guest: '/data' },
    ],
    dimensionName: 'composed',
    chips: [
      { text: '-lw · any source, any order' },
      { text: '-v · pass-through data' },
      { text: '-snapshot · persists writes' },
      { text: 'one process tree' },
    ],
  },
];

const diagStyles = {
  frame: {
    background: 'var(--paper)',
    border: '1px solid var(--rule)',
    borderRadius: 6,
    padding: 18,
    display: 'flex', flexDirection: 'column', gap: 14,
    boxShadow: '0 1px 0 rgba(26,35,50,0.03), 0 10px 28px rgba(26,35,50,0.06)',
  },
  caption: {
    display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap',
    padding: '0 0 12px', borderBottom: '1px solid var(--rule)',
  },
  captionLabel: {
    fontFamily: 'var(--font-mono)', fontSize: 11, letterSpacing: '0.1em',
    textTransform: 'uppercase', color: 'var(--wood-deep)',
    padding: '2px 7px', border: '1px solid var(--wood-light)',
    background: 'color-mix(in srgb, var(--oar) 40%, var(--paper))',
    borderRadius: 2,
  },
  captionCmd: {
    fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--ink)',
    flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
  },
  canvas: {
    position: 'relative',
    display: 'grid',
    gridTemplateColumns: '1fr 1fr 1fr',
    gap: 14,
    alignItems: 'start',
  },
  col: { display: 'flex', flexDirection: 'column', gap: 6, minHeight: 200 },
  colHead: {
    fontFamily: 'var(--font-mono)', fontSize: 10, letterSpacing: '0.1em',
    textTransform: 'uppercase', color: 'var(--mute)',
  },
  connectors: {
    position: 'absolute', inset: 0, width: '100%', height: '100%', pointerEvents: 'none',
  },
  sourceBox: {
    background: 'var(--paper)', padding: '18px 14px', borderRadius: 4,
    display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6,
    minHeight: 90, justifyContent: 'center',
  },
  chips: {
    display: 'flex', gap: 6, flexWrap: 'wrap',
    paddingTop: 12, borderTop: '1px solid var(--rule)',
  },
};

window.LiveDiagram = LiveDiagram;
