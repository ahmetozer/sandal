// Inline SVG icons tuned to the Sandal palette (ink + wood + water).
// All icons render at 1em and inherit currentColor unless they have
// explicit multi-part fills.

function IconHost({ size = 48 }) {
  // A running system / mounted root — stylized as a monitor with /
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" fill="none">
      <rect x="4" y="7" width="40" height="28" rx="2" stroke="currentColor" strokeWidth="1.5"/>
      <path d="M4 30h40" stroke="currentColor" strokeWidth="1"/>
      <path d="M18 41h12M22 35v6M26 35v6" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
      <text x="24" y="22" textAnchor="middle" fontFamily="var(--font-mono)" fontSize="10" fill="currentColor" fontWeight="500">/</text>
      <circle cx="9" cy="12" r="0.8" fill="currentColor"/>
      <circle cx="12" cy="12" r="0.8" fill="currentColor"/>
      <circle cx="15" cy="12" r="0.8" fill="currentColor"/>
    </svg>
  );
}

function IconImage({ size = 48 }) {
  // A .sqfs / rootfs file — folded archive
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" fill="none">
      <path d="M10 6h20l10 10v26a2 2 0 0 1-2 2H10a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2z"
        stroke="currentColor" strokeWidth="1.5" fill="var(--paper)"/>
      <path d="M30 6v10h10" stroke="currentColor" strokeWidth="1.5" fill="none"/>
      <path d="M14 24h20M14 29h20M14 34h14" stroke="currentColor" strokeWidth="1" opacity="0.45"/>
      <text x="16" y="44" fontFamily="var(--font-mono)" fontSize="6.5" fill="currentColor" opacity="0.6">.sqfs</text>
    </svg>
  );
}

function IconMount({ size = 48 }) {
  // Bind mount — folder on host pinned through into the dimension
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" fill="none">
      {/* host folder (left) */}
      <path d="M5 14h8l3 3h12a1.5 1.5 0 0 1 1.5 1.5V32a1.5 1.5 0 0 1-1.5 1.5H5A1.5 1.5 0 0 1 3.5 32V15.5A1.5 1.5 0 0 1 5 14z"
        stroke="currentColor" strokeWidth="1.4" fill="var(--paper)"/>
      {/* guest folder (right, slightly offset) */}
      <path d="M22 19h6l2 2h10a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H22a1 1 0 0 1-1-1V20a1 1 0 0 1 1-1z"
        stroke="currentColor" strokeWidth="1.4" fill="var(--paper)" opacity="0.9"/>
      {/* bind arrow */}
      <path d="M16 24h5" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/>
      <path d="M19 22l3 2-3 2" stroke="currentColor" strokeWidth="1.3" fill="none" strokeLinecap="round" strokeLinejoin="round"/>
      {/* small -v label */}
      <text x="32" y="44" fontFamily="var(--font-mono)" fontSize="6" fill="currentColor" opacity="0.7">-v</text>
    </svg>
  );
}

function IconVM({ size = 48 }) {
  // Virtual machine — server chassis with virtual-layer halo
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" fill="none">
      {/* virtualization halo */}
      <rect x="3" y="8" width="42" height="32" rx="4" stroke="currentColor" strokeWidth="1" strokeDasharray="2 2" opacity="0.55" fill="none"/>
      {/* server chassis */}
      <rect x="8" y="13" width="32" height="22" rx="2" stroke="currentColor" strokeWidth="1.5" fill="var(--paper)"/>
      {/* rack vents */}
      <path d="M11 17h10M11 19h10M11 21h10" stroke="currentColor" strokeWidth="0.9" opacity="0.4"/>
      {/* cpu cores */}
      <rect x="24" y="17" width="6" height="6" rx="0.5" stroke="currentColor" strokeWidth="1" fill="none"/>
      <rect x="32" y="17" width="6" height="6" rx="0.5" stroke="currentColor" strokeWidth="1" fill="none"/>
      {/* power LED */}
      <circle cx="37" cy="30" r="1.1" fill="currentColor" opacity="0.75"/>
      {/* bottom strip */}
      <path d="M11 30h18" stroke="currentColor" strokeWidth="1" opacity="0.4"/>
      <text x="11" y="34" fontFamily="var(--font-mono)" fontSize="4.5" fill="currentColor" opacity="0.7">VM</text>
    </svg>
  );
}

function IconDrive({ size = 48 }) {
  // External USB / NVMe drive — SSD-style enclosure with cable
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" fill="none">
      {/* enclosure */}
      <rect x="5" y="14" width="32" height="22" rx="2.5" stroke="currentColor" strokeWidth="1.5" fill="var(--paper)"/>
      {/* label area */}
      <rect x="9" y="19" width="15" height="6" rx="0.5" stroke="currentColor" strokeWidth="1" fill="none" opacity="0.55"/>
      {/* activity LED */}
      <circle cx="30" cy="22" r="1.3" fill="currentColor" opacity="0.75"/>
      {/* vent slits */}
      <path d="M9 30h24M9 32h24" stroke="currentColor" strokeWidth="0.9" opacity="0.35"/>
      {/* usb/nvme cable stub on right side */}
      <path d="M37 23v4h4v-4" stroke="currentColor" strokeWidth="1.4" fill="var(--paper)"/>
      <path d="M39 27v5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round"/>
      <rect x="37.5" y="32" width="3" height="5" rx="0.6" stroke="currentColor" strokeWidth="1.2" fill="none"/>
      {/* small NVMe label */}
      <text x="11" y="24" fontFamily="var(--font-mono)" fontSize="4.5" fill="currentColor" opacity="0.7">NVMe</text>
    </svg>
  );
}

function IconRegistry({ size = 48 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" fill="none">
      <defs>
        <linearGradient id="disc-g" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%"  stopColor="#f0d8d8"/>
          <stop offset="30%" stopColor="#a7d8e0"/>
          <stop offset="60%" stopColor="#c89968"/>
          <stop offset="100%" stopColor="#8a5a2b"/>
        </linearGradient>
      </defs>
      <ellipse cx="24" cy="24" rx="18" ry="18" fill="url(#disc-g)" stroke="currentColor" strokeWidth="1.2"/>
      <ellipse cx="24" cy="24" rx="5" ry="5" fill="var(--bone)" stroke="currentColor" strokeWidth="1"/>
      <ellipse cx="24" cy="24" rx="1.2" ry="1.2" fill="currentColor"/>
    </svg>
  );
}

function IconCube({ size = 48 }) {
  // The resulting dimension — isometric cube
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" fill="none">
      <path d="M24 6 L42 15 L42 33 L24 42 L6 33 L6 15 Z"
        fill="var(--wood-light)" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round"/>
      <path d="M24 6 L24 24 M24 24 L6 15 M24 24 L42 15"
        stroke="currentColor" strokeWidth="1.2" opacity="0.7"/>
      <path d="M24 6 L42 15 L24 24 Z" fill="var(--oar)" opacity="0.5"/>
    </svg>
  );
}

function IconArrow({ length = 120, dashed = false, label }) {
  return (
    <svg width={length} height="28" viewBox={`0 0 ${length} 28`} style={{ display: 'block' }}>
      <defs>
        <marker id={"arr"+length} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto">
          <path d="M0,0 L10,5 L0,10 z" fill="currentColor"/>
        </marker>
      </defs>
      <line x1="0" y1="14" x2={length-4} y2="14"
        stroke="currentColor" strokeWidth="1.5"
        strokeDasharray={dashed ? "4 4" : undefined}
        markerEnd={`url(#arr${length})`}/>
      {label ? (
        <text x={length/2} y="9" textAnchor="middle"
          fontFamily="var(--font-mono)" fontSize="10" fill="currentColor">{label}</text>
      ) : null}
    </svg>
  );
}

// Curved arrow for "combine" layouts
function IconCurvedArrow({ width = 160, height = 80, flip = false }) {
  const d = flip
    ? `M 10 ${height-10} Q ${width/2} 10, ${width-14} ${height-10}`
    : `M 10 10 Q ${width/2} ${height-10}, ${width-14} 10`;
  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`}>
      <defs>
        <marker id={"carr"+width} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto">
          <path d="M0,0 L10,5 L0,10 z" fill="currentColor"/>
        </marker>
      </defs>
      <path d={d} stroke="currentColor" strokeWidth="1.5" fill="none"
        markerEnd={`url(#carr${width})`}/>
    </svg>
  );
}

Object.assign(window, { IconHost, IconImage, IconDrive, IconVM, IconMount, IconRegistry, IconCube, IconArrow, IconCurvedArrow });
