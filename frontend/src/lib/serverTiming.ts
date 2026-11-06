// serverTiming — v0.10.1834 (Log Search Faz 1 dilim 1.11; audit §5).
//
// /api/logs* yanıtları `Server-Timing: cache;desc="HIT", es;dur=12.3,
// total;dur=40.1` taşır (internal/api/server_timing.go, v0.10.1817). Bu SAF
// yardımcı başlığı okur; akışlı yükleme göstergesi ("✓ Satırlar 0,4 sn")
// sunucunun `total` süresini, başlık yoksa (ingress sildi / CH yolu / eski
// sunucu) istemcinin ölçtüğü süreyi gösterir.
import type { LoadTiming } from './types';

/** Başlık → {totalMs, esMs, cache}; tanınmayan girdi yok sayılır. */
export function parseServerTiming(header: string | null | undefined): Omit<LoadTiming, 'clientMs'> {
  const out: Omit<LoadTiming, 'clientMs'> = {};
  if (!header) return out;
  for (const entry of header.split(',')) {
    const [rawName, ...params] = entry.split(';').map(s => s.trim());
    const name = rawName?.toLowerCase();
    let dur: number | undefined;
    let desc: string | undefined;
    for (const p of params) {
      const eq = p.indexOf('=');
      if (eq < 0) continue;
      const k = p.slice(0, eq).trim().toLowerCase();
      const v = p.slice(eq + 1).trim().replace(/^"(.*)"$/, '$1');
      if (k === 'dur') { const n = Number(v); if (Number.isFinite(n) && n >= 0) dur = n; }
      if (k === 'desc') desc = v;
    }
    if (name === 'total' && dur !== undefined) out.totalMs = dur;
    else if (name === 'es' && dur !== undefined) out.esMs = dur;
    else if (name === 'cache' && desc) out.cache = desc;
  }
  return out;
}

/** Gösterilecek süre: sunucu `total`, yoksa istemci ölçümü. */
export function loadTimingMs(t: LoadTiming | undefined): number | undefined {
  if (!t) return undefined;
  return t.totalMs ?? t.clientMs;
}

/** ms → "0,4 sn" (tr-TR; < 0,1 sn → "<0,1 sn"; ≥ 10 sn tam sayı). */
export function formatLoadSec(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms)) return '';
  const s = ms / 1000;
  if (s < 0.1) return '<0,1 sn';
  if (s >= 10) return `${Math.round(s)} sn`;
  return `${s.toLocaleString('tr-TR', { minimumFractionDigits: 1, maximumFractionDigits: 1 })} sn`;
}

/** Çipin ipucu: kaynak + kırılım (sunucu toplamı / ES / önbellek / istemci). */
export function loadTimingTitle(t: LoadTiming | undefined): string {
  if (!t) return '';
  const parts: string[] = [];
  if (t.totalMs !== undefined) parts.push(`sunucu ${formatLoadSec(t.totalMs)}`);
  if (t.esMs !== undefined) parts.push(`ES ${formatLoadSec(t.esMs)}`);
  if (t.cache) parts.push(`önbellek ${t.cache}`);
  parts.push(`tarayıcı ${formatLoadSec(t.clientMs)}`);
  return parts.join(' · ');
}
