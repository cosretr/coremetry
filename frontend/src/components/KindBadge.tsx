import type { ProfileFrameKind, ProfileCategoryBreakdown } from '@/lib/types';
import { seriesPalette } from '@/lib/chartFmt';

// Shared vocabulary for frame kinds (CPU / Lock / IO / Sleep / GC).
//
// v0.10.922 (sade palet adım 1) — tür bir KATEGORİ, durum değil:
// satır rozeti her tür için tek nötr stil (--bg3 zemin, --text2 yazı),
// ayrımı etiket taşır. Eski eşleme (cpu --ok yeşil, lock --brand
// kırmızı, io --accent, gc --orange) durum/marka renklerini kategoriye
// harcıyordu (K5: yeşil yalnız geçiş; K6: marka kırmızısı yalnız logo).
// Dağılım çubuğu bir GRAFİK: dilimler ayırt edilmeli, o yüzden seri
// paletinden sabit yuva alır (durum renkleri paletin dışında) — lejant
// etiket + yüzde yazdığı için renk tek taşıyıcı değil.
const ORDER: ProfileFrameKind[] = ['cpu', 'lock', 'io', 'sleep', 'gc'];

const LABELS: Record<ProfileFrameKind, string> = {
  cpu:   'CPU',
  lock:  'Lock',
  io:    'IO',
  sleep: 'Sleep',
  gc:    'GC',
};

// kindColor — YALNIZ dağılım çubuğu (grafik) için: türün sabit seri
// yuvası, tema-farkında. Rozet renk almaz.
export function kindColor(k: ProfileFrameKind): string {
  const pal = seriesPalette();
  return pal[ORDER.indexOf(k) % pal.length];
}
export function kindLabel(k: ProfileFrameKind): string { return LABELS[k]; }

export function KindBadge({ kind }: { kind: ProfileFrameKind }) {
  if (kind === 'cpu') return null; // CPU is the default; reduce visual noise
  return (
    <span style={{
      fontSize: 10, fontWeight: 700, padding: '1px 6px',
      marginLeft: 6,
      // v0.10.922 (sade palet adım 1) — her tür aynı nötr stil; v0.10.920
      // sleep mürekkep istisnası artık gereksiz (--text2 her zeminde okunur).
      background: 'var(--bg3)', color: 'var(--text2)',
      borderRadius: 3, fontFamily: 'monospace',
      verticalAlign: 'middle',
    }}>
      {LABELS[kind]}
    </span>
  );
}

// BreakdownBar — stacked horizontal bar showing the leaf-time
// distribution across kinds. Sits at the top of the hotspot
// view so the operator sees at a glance whether a slow service
// is CPU-bound, lock-bound, or IO-bound BEFORE drilling into
// individual methods.
export function BreakdownBar({ b }: { b: ProfileCategoryBreakdown | undefined }) {
  if (!b) return null;
  const total = b.cpu + b.lock + b.io + b.sleep + b.gc;
  if (total <= 0) return null;
  const order = ORDER;
  const pct = (n: number) => (n / total) * 100;
  return (
    <div style={{
      marginBottom: 12, padding: 10, borderRadius: 6,
      background: 'var(--bg1)', border: '1px solid var(--border)',
    }}>
      <div style={{ fontSize: 11, color: 'var(--text2)', marginBottom: 6 }}>
        Leaf-time breakdown — where each sample landed
      </div>
      <div style={{ display: 'flex', height: 16, borderRadius: 4, overflow: 'hidden',
                    border: '1px solid var(--border)' }}>
        {order.map(k => {
          const w = pct(b[k]);
          if (w <= 0) return null;
          return (
            <div key={k} title={`${LABELS[k]}: ${w.toFixed(1)}%`} style={{
              width: w + '%', background: kindColor(k), minWidth: 1,
            }} />
          );
        })}
      </div>
      <div style={{ display: 'flex', gap: 14, marginTop: 8, fontSize: 11, flexWrap: 'wrap' }}>
        {order.map(k => {
          const w = pct(b[k]);
          if (w <= 0) return null;
          return (
            <span key={k} style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
              <span style={{ width: 8, height: 8, borderRadius: 2, background: kindColor(k) }} />
              <b style={{ color: 'var(--text)' }}>{LABELS[k]}</b>
              <span style={{ color: 'var(--text2)' }}>{w.toFixed(1)}%</span>
            </span>
          );
        })}
      </div>
    </div>
  );
}
