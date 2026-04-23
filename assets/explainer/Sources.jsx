// One row per source mode, illustrating how the lowerdir is assembled.
// Visual grammar:
//   [ source ] ──▶ [ overlay stack ] ──▶ [ dimension cube ]

function Tag({ children, tone = 'ink' }) {
  const bg = {
    ink:   { bg: 'var(--paper)', fg: 'var(--ink)', bd: 'var(--rule)' },
    wood:  { bg: 'color-mix(in srgb, var(--wood-light) 30%, var(--paper))', fg: 'var(--wood-deep)', bd: 'color-mix(in srgb, var(--wood) 30%, var(--rule))' },
    water: { bg: 'color-mix(in srgb, var(--water) 40%, var(--paper))', fg: '#2a5560', bd: 'color-mix(in srgb, var(--water-deep) 40%, var(--rule))' },
    ok:    { bg: 'color-mix(in srgb, var(--ok) 12%, var(--paper))', fg: 'var(--ok)', bd: 'color-mix(in srgb, var(--ok) 30%, transparent)' },
  }[tone];
  return (
    <span style={{
      fontFamily: 'var(--font-mono)', fontSize: 11, padding: '2px 7px',
      borderRadius: 2, background: bg.bg, color: bg.fg,
      border: `1px solid ${bg.bd}`, whiteSpace: 'nowrap',
    }}>{children}</span>
  );
}

function Card({ children, kind = 'ink' }) {
  const map = {
    ink:   { bg: 'var(--paper)', bd: 'var(--ink)',       ac: 'var(--ink)' },
    wood:  { bg: 'color-mix(in srgb, var(--oar) 30%, var(--paper))', bd: 'var(--wood)', ac: 'var(--wood-deep)' },
    water: { bg: 'color-mix(in srgb, var(--water) 25%, var(--paper))', bd: 'var(--water-deep)', ac: '#2a5560' },
  }[kind];
  return (
    <div style={{
      background: map.bg, border: `1.5px solid ${map.bd}`, color: map.ac,
      borderRadius: 4, padding: 14, minWidth: 200,
      display: 'flex', flexDirection: 'column', gap: 8, alignItems: 'center',
      boxShadow: '0 1px 0 rgba(26,35,50,0.04), 0 4px 12px rgba(26,35,50,0.04)',
    }}>{children}</div>
  );
}

// The "dimension" output column — just the cube + a name plate
function DimensionOut({ name }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6 }}>
      <div style={{ color: 'var(--wood-deep)' }}><IconCube size={72}/></div>
      <Tag tone="wood">dimension · {name}</Tag>
    </div>
  );
}

// ────────────────────────────────────────────────────────────────
// Mode 1: Host system as lowerdir
function ModeHost() {
  return (
    <div style={rowStyle}>
      <div style={colStyle}>
        <Card kind="ink">
          <div style={{ color: 'var(--ink)' }}><IconHost size={56}/></div>
          <div style={{ fontSize: 14, fontWeight: 500, color: 'var(--ink)' }}>Host root</div>
          <Tag>/ (running system)</Tag>
        </Card>
        <div style={noteStyle}>
          No pull. No archive. Sandal just binds the live host as the lower layer —
          your container sees the exact tools already on disk.
        </div>
      </div>

      <div style={arrowColStyle}>
        <div style={{ color: 'var(--mute)' }}>
          <IconArrow length={140} label="lowerdir=/"/>
        </div>
      </div>

      <div style={colStyle}>
        <Card kind="water">
          <div style={{ fontSize: 13, fontWeight: 500 }}>Overlay</div>
          <div style={stackViz}>
            <StackLayer label="upperdir · writes" tone="wood"/>
            <StackLayer label="lower · host /" tone="ink"/>
          </div>
          <Tag tone="water">overlayfs</Tag>
        </Card>
      </div>

      <div style={arrowColStyle}>
        <div style={{ color: 'var(--mute)' }}><IconArrow length={90}/></div>
      </div>

      <DimensionOut name="host-derived"/>
    </div>
  );
}

// ────────────────────────────────────────────────────────────────
// Mode 2: Local image file (.sqfs / rootfs tarball)
function ModeImage() {
  return (
    <div style={rowStyle}>
      <div style={colStyle}>
        <Card kind="ink">
          <div style={{ color: 'var(--ink)' }}><IconImage size={56}/></div>
          <div style={{ fontSize: 14, fontWeight: 500, color: 'var(--ink)' }}>Local image</div>
          <Tag>./alpine.sqfs</Tag>
        </Card>
        <div style={noteStyle}>
          Point at any squashfs, rootfs tarball, or directory on disk —
          useful for air-gapped machines and reproducible base images.
        </div>
      </div>

      <div style={arrowColStyle}>
        <div style={{ color: 'var(--mute)' }}>
          <IconArrow length={140} label="mount"/>
        </div>
      </div>

      <div style={colStyle}>
        <Card kind="water">
          <div style={{ fontSize: 13, fontWeight: 500 }}>Overlay</div>
          <div style={stackViz}>
            <StackLayer label="upperdir · writes" tone="wood"/>
            <StackLayer label="lower · alpine.sqfs" tone="ink"/>
          </div>
          <Tag tone="water">overlayfs</Tag>
        </Card>
      </div>

      <div style={arrowColStyle}>
        <div style={{ color: 'var(--mute)' }}><IconArrow length={90}/></div>
      </div>

      <DimensionOut name="alpine"/>
    </div>
  );
}

// ────────────────────────────────────────────────────────────────
// Mode 3: Registry images — and the combine-multiple case
function ModeRegistry() {
  return (
    <div style={rowStyle}>
      <div style={{...colStyle, gap: 10}}>
        <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
          <Card kind="wood">
            <IconRegistry size={48}/>
            <div style={{ fontSize: 13, fontWeight: 500 }}>busybox:latest</div>
            <Tag tone="wood">docker.io</Tag>
          </Card>
          <Card kind="wood">
            <IconRegistry size={48}/>
            <div style={{ fontSize: 13, fontWeight: 500 }}>python:3.12-slim</div>
            <Tag tone="wood">ghcr.io</Tag>
          </Card>
        </div>
        <div style={noteStyle}>
          Pull from any OCI/Docker registry. This flag can usable multiple times —
          Sandal stacks each image as its own lower layer, in the order you give them.
        </div>
      </div>

      <div style={arrowColStyle}>
        <div style={{ color: 'var(--mute)' }}>
          <IconArrow length={140} label="-lw × N"/>
        </div>
      </div>

      <div style={colStyle}>
        <Card kind="water">
          <div style={{ fontSize: 13, fontWeight: 500 }}>Overlay (stacked)</div>
          <div style={stackViz}>
            <StackLayer label="upperdir · writes" tone="wood"/>
            <StackLayer label="lower · python:3.12-slim" tone="water"/>
            <StackLayer label="lower · busybox:latest"   tone="ink"/>
          </div>
          <Tag tone="water">overlayfs · N lowers</Tag>
        </Card>
      </div>

      <div style={arrowColStyle}>
        <div style={{ color: 'var(--mute)' }}><IconArrow length={90}/></div>
      </div>

      <DimensionOut name="py-on-busybox"/>
    </div>
  );
}

// ────────────────────────────────────────────────────────────────
function StackLayer({ label, tone }) {
  const tones = {
    ink:   { bg: 'var(--ink)',        fg: 'var(--bone)' },
    wood:  { bg: 'var(--wood-light)', fg: 'var(--wood-deep)' },
    water: { bg: 'var(--water)',      fg: '#1a4049' },
  };
  const t = tones[tone] || tones.ink;
  return (
    <div style={{
      width: '100%', background: t.bg, color: t.fg,
      fontFamily: 'var(--font-mono)', fontSize: 11, lineHeight: 1.35,
      padding: '7px 10px', borderRadius: 2, textAlign: 'left',
      whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis',
    }} title={label}>{label}</div>
  );
}

const rowStyle = {
  display: 'grid',
  gridTemplateColumns: '1fr auto 1fr auto auto',
  gap: 12, alignItems: 'center',
};
const colStyle = { display: 'flex', flexDirection: 'column', gap: 10, alignItems: 'center' };
const arrowColStyle = { display: 'flex', alignItems: 'center', justifyContent: 'center' };
const stackViz = { display: 'flex', flexDirection: 'column', gap: 3, width: '100%', alignItems: 'stretch' };
const noteStyle = {
  fontFamily: 'var(--font-sans)', fontSize: 13, color: 'var(--ink-soft)',
  lineHeight: 1.5, maxWidth: 280, textAlign: 'center', textWrap: 'pretty',
};

Object.assign(window, { ModeHost, ModeImage, ModeRegistry, Tag, Card, DimensionOut, StackLayer });
