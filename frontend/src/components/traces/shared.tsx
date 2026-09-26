// shared.tsx — Phase 1 Task B (TRACES) common atoms.
//
// One per-service colour map for every trace surface (table badge, duration
// bar, scatter dot, mini-waterfall, full waterfall, RED chart). It reuses the
// SAME hash the topology graph + metrics charts use (chartFmt.seriesColor) so a
// service keeps ONE colour across the whole product — the operator's eye never
// recalibrates. Everything here is CSS-var-only (light + dark safe).

import { seriesColor } from '@/lib/chartFmt';

// svcColor — the shared per-service hue. Empty / unknown collapses to a stable
// 'unknown' bucket so blank service rows still get a deterministic colour.
export const svcColor = (name: string): string => seriesColor(name || 'unknown');

// svcBadgeBg — a faint, theme-safe tint of the service hue for badge fills.
export const svcBadgeBg = (name: string): string =>
  `color-mix(in srgb, ${svcColor(name)} 16%, transparent)`;

// durColor — duration → token colour. Errors are always red; otherwise a
// neutral/amber/red ramp by absolute latency: sub-400ms NEUTRAL (v0.10.922 (sade palet adım 1), K5 —
// sağlıklı durum renk almaz; eskiden yeşildi), sub-1s amber, ≥1s red.
export function durColor(ms: number, err: boolean): string {
  if (err) return 'var(--err)';
  if (ms > 1000) return 'var(--err)';
  if (ms > 400) return 'var(--warn-solid)'; // v0.10.920 — mini çubuk dolgusu
  return 'var(--text3)';
}

// fmtDur — compact duration label (ms under 1s, s above). Two decimals so
// sub-millisecond spans don't read as "0".
export function fmtDur(ms: number): string {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
  return `${ms.toFixed(2)}ms`;
}

// SvcBadge — service-coloured monospace pill. Same hue as the waterfall +
// topology, so service identity is colour-stable everywhere.
export function SvcBadge({ name }: { name: string }) {
  return (
    <span
      title={name || 'unknown'}
      style={{
        fontSize: 11,
        padding: '1px 7px',
        borderRadius: 4,
        fontFamily: 'var(--font-mono)',
        background: svcBadgeBg(name),
        color: svcColor(name),
        whiteSpace: 'nowrap',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        maxWidth: '100%',
        display: 'inline-block',
        verticalAlign: 'bottom',
      }}>
      {name || 'unknown'}
    </span>
  );
}

// DurationBar — value label + a track-bar scaled to the slowest visible row,
// coloured by latency (red if the trace/span errored). Reuses the .ov-minibar
// token track from globals.css.
export function DurationBar({ ms, err, max }: { ms: number; err: boolean; max: number }) {
  const pct = max > 0 ? Math.max(2, Math.min(100, (ms / max) * 100)) : 0;
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
      {/* v0.10.922 (sade palet adım 1) — değer metni nötr: hata zaten çubuk rengi + ERROR rozetinde (tek olgu, tek işaret). */}
      <span className="mono" style={{ minWidth: 58, color: 'var(--text)' }}>
        {fmtDur(ms)}
      </span>
      <span className="ov-minibar" style={{ maxWidth: 110, flex: 1 }}>
        <i style={{ width: `${pct}%`, background: durColor(ms, err) }} />
      </span>
    </div>
  );
}

// v0.10.924 — buton bütünlüğü Faz 2: QuickChip (elle boyanmış ham düğme)
// silindi; son çağıranı v0.9.304'te kısayol şeridiyle gitmişti. Hızlı filtre
// çipi gerekirse `ui/Chip` (pill + active).

// SpanKindChip — a compact, normalised span-kind label (server/client/
// producer/consumer/internal). Tokenised so it reads in both themes.
export function SpanKindChip({ kind }: { kind: string }) {
  const k = normKindLabel(kind);
  if (!k) return null;
  return (
    <span
      style={{
        fontSize: 9.5,
        fontWeight: 700,
        letterSpacing: '0.4px',
        textTransform: 'uppercase',
        padding: '1px 5px',
        borderRadius: 3,
        background: 'var(--bg3)',
        color: 'var(--text3)',
        border: '1px solid var(--border)',
        whiteSpace: 'nowrap',
      }}>
      {k}
    </span>
  );
}

function normKindLabel(kind: string | undefined): string {
  const k = (kind ?? '').toLowerCase().replace(/^span_kind_/, '');
  switch (k) {
    case 'server': case '2': return 'SRV';
    case 'client': case '3': return 'CLI';
    case 'producer': case '4': return 'PROD';
    case 'consumer': case '5': return 'CONS';
    case 'internal': case '1': return 'INT';
    default: return '';
  }
}
