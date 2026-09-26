// chatEvidence.ts — v0.10.558 (CoSRE Faz 4c-2). SAF: evidence bloğu → kart
// satırları. Delta: taban 0 iken "yeni"; ±%15 içindeyse "~"; aksi ×N (bir
// ondalık). Zaman yerel HH:MM. Hipotez yoksa verdict metni aynen (kart yine
// çizilir — değişiklik + desen listesi tek başına değerli, operatör mockup onayı).
import type { ChatEvidence, ChatEvidenceRED, ChatTypedBlock } from './types';

export function isChatEvidence(p: unknown): p is ChatEvidence {
  const o = p as Partial<ChatEvidence> | null;
  return !!o && typeof o.service === 'string' && typeof o.verdict === 'string' && Array.isArray(o.problems) && Array.isArray(o.changes) && Array.isArray(o.logPatterns);
}

export function evidenceBlocks(blocks: ChatTypedBlock[] | undefined): ChatEvidence[] {
  return (blocks ?? []).filter(b => b.type === 'evidence').map(b => b.payload).filter(isChatEvidence);
}

export interface EvidenceDelta { dir: 'up' | 'down' | 'flat' | 'new'; text: string }

export function fmtDelta(now: number, base: number): EvidenceDelta {
  if (!(base > 0)) return now > 0 ? { dir: 'new', text: '▲ yeni' } : { dir: 'flat', text: '~' };
  const r = now / base;
  if (r >= 1.15) return { dir: 'up', text: `▲ ×${r.toFixed(1)}` };
  if (r <= 0.85) return { dir: 'down', text: `▼ ×${(base / now || 0).toFixed(1)}` };
  return { dir: 'flat', text: '~' };
}

// v0.10.929 (K5) — satır başına YÖN bayrağı: `upIsBad` yükselişin sapma
// olup olmadığını söyler. Hata oranı / gecikme artışı kötü (kırmızı kalır);
// throughput (span/sn) artışı sağlık sinyali değil — nötr basılır.
export interface EvidenceRedRow { key: string; label: string; now: string; base: string; delta: EvidenceDelta; upIsBad: boolean }

const fmtPct = (v: number) => `%${v >= 10 ? v.toFixed(0) : v.toFixed(1)}`;
const fmtMs = (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(2)} s` : `${Math.round(v)} ms`);
const fmtRate = (v: number) => (v >= 10 ? v.toFixed(0) : v.toFixed(1));

// v0.10.944 — okunamayan pencere asla 0 diye biçimlenmez: şimdi yoksa satır
// yok (fark anlamsız); taban yoksa taban '—' ve Δ 'taban okunamadı' (tonsuz —
// '▲ yeni' sapma rengi almaz). Oran etiketi span/sn: değer service_summary_5m'in
// TÜM span türleri üzerinden hızıdır, istek hızı değil.
export function redRows(red: ChatEvidence['red']): EvidenceRedRow[] {
  if (!red?.current) return [];
  const c: ChatEvidenceRED = red.current;
  const b: ChatEvidenceRED | undefined = red.baseline;
  const row = (key: string, label: string, now: number, base: number | undefined, fmt: (v: number) => string, upIsBad: boolean): EvidenceRedRow => ({
    key, label, now: fmt(now),
    base: base === undefined ? '—' : fmt(base),
    delta: base === undefined ? { dir: 'flat', text: 'taban okunamadı' } : fmtDelta(now, base),
    upIsBad,
  });
  return [
    row('errorRate', 'hata oranı', c.errorRate, b?.errorRate, fmtPct, true),
    row('p95', 'p95', c.p95Ms, b?.p95Ms, fmtMs, true),
    row('rate', 'span/sn', c.rate, b?.rate, fmtRate, false),
  ];
}

// v0.10.929 (K5) — Δ hücresinin sınıfı. Yükseliş (▲ ×N ya da ▲ yeni) YALNIZ
// `upIsBad` satırında sapma tonunu (.ev-delta--up kırmızı / --new amber)
// alır; yükselişin iyi olduğu satırda (span/sn) değiştirici YOK → hücre
// .ev-table td'nin nötr rengini (--text2, .ev-delta--down ile aynı) taşır.
// Düşüş ve ~ her satırda kendi (nötr) sınıflarını korur.
export function evidenceDeltaClass(r: Pick<EvidenceRedRow, 'delta' | 'upIsBad'>): string {
  const rise = r.delta.dir === 'up' || r.delta.dir === 'new';
  if (rise && !r.upIsBad) return 'ev-delta';
  return `ev-delta ev-delta--${r.delta.dir}`;
}

export function fmtEvidenceTime(ns: number): string {
  if (!(ns > 0)) return '—';
  const d = new Date(ns / 1e6);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

export function fmtRangeTR(rangeS: number): string {
  if (rangeS >= 86400 && rangeS % 86400 === 0) return `son ${rangeS / 86400} gün`;
  if (rangeS >= 3600) return `son ${+(rangeS / 3600).toFixed(rangeS % 3600 ? 1 : 0)} sa`;
  return `son ${Math.max(1, Math.round(rangeS / 60))} dk`;
}

export const evidenceHasHypothesis = (e: ChatEvidence) => e.problems.some(p => !!p.topSuspect);
