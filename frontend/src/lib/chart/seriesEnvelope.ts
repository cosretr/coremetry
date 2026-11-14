import { alignToUnion } from './alignSeries';

// seriesEnvelope — v0.10.2146 (servis sekmeleri S7, docs/frontend/
// service-tabs-plan.md §4; mockup operatör onayı 2026-11-13).
//
// Çok serili bir grafiği (pod başına, route başına) TEK okunur şekle indirir:
// her zaman noktasında serilerin en düşük / en yüksek değeri (zarf), ortancası
// ve "en yüksek" serinin kendisi (adıyla). 40 pod × ~300 nokta mikro-saniye
// düzeyi: istemci hesabı, sunucu sorgusu yok.
//
// AD: "zarf" (envelope). Anomali bantlarıyla (`?bands`, lib/bandsParam —
// beklenen aralık) İLGİSİ YOK: bu, aynı anda çizilen serilerin gerçek
// yayılımıdır (plan K14).
//
// Dürüstlük: bir zaman noktasında hiç değer yoksa lo / hi / ortanca null
// (grafikte boşluk; uydurma 0 yok). Değeri hiç olmayan seri sayılmaz (`n`).
// SAF; React/DOM yok.

export interface EnvPoint { time: number; value: number | null }
export interface EnvSeries { label: string; points: readonly EnvPoint[] }

export type TopBy = 'mean' | 'max' | 'last';

export interface SeriesEnvelope {
  /** En az bir değeri olan seri sayısı. */
  n: number;
  lo: EnvPoint[];
  hi: EnvPoint[];
  median: EnvPoint[];
  /** Sıralama ölçüsüne göre en yüksek seri (kendi noktalarıyla); seri yoksa null. */
  top: { label: string; points: EnvPoint[] } | null;
}

const finite = (v: number | null | undefined): v is number => v != null && Number.isFinite(v);

function medianOf(sorted: number[]): number {
  const mid = sorted.length >> 1;
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
}

/** Serinin sıralama puanı; değeri yoksa null. */
export function seriesScore(points: readonly EnvPoint[], by: TopBy = 'mean'): number | null {
  let sum = 0, n = 0, max = -Infinity, last: number | null = null;
  for (const p of points) {
    if (!finite(p.value)) continue;
    sum += p.value; n++;
    if (p.value > max) max = p.value;
    last = p.value;
  }
  if (n === 0) return null;
  return by === 'max' ? max : by === 'last' ? last : sum / n;
}

/**
 * En yüksek `k` serinin GİRİŞ dizinleri, puana göre azalan. Eşitlikte giriş
 * sırası korunur (kararlı: yenilemede renkler yer değiştirmez). Değeri
 * olmayan seri listeye girmez.
 */
export function topSeriesIndexes(series: readonly EnvSeries[], k: number, by: TopBy = 'mean'): number[] {
  const scored: { i: number; s: number }[] = [];
  series.forEach((s, i) => {
    const sc = seriesScore(s.points, by);
    if (sc != null) scored.push({ i, s: sc });
  });
  scored.sort((a, b) => b.s - a.s || a.i - b.i);
  return scored.slice(0, Math.max(0, k)).map(x => x.i);
}

export function seriesEnvelope(series: readonly EnvSeries[], by: TopBy = 'mean'): SeriesEnvelope {
  const live = series.filter(s => s.points.some(p => finite(p.value)));
  if (live.length === 0) return { n: 0, lo: [], hi: [], median: [], top: null };
  // Ayrı okumaların kova kümeleri farklılaşabilir → tek eksene hizala.
  const { times, cols } = alignToUnion(live.map(s => s.points));
  const lo: EnvPoint[] = [], hi: EnvPoint[] = [], median: EnvPoint[] = [];
  const buf: number[] = [];
  for (let t = 0; t < times.length; t++) {
    buf.length = 0;
    for (const col of cols) {
      const v = col[t];
      if (finite(v)) buf.push(v);
    }
    const time = times[t];
    if (buf.length === 0) {
      lo.push({ time, value: null }); hi.push({ time, value: null }); median.push({ time, value: null });
      continue;
    }
    buf.sort((a, b) => a - b);
    lo.push({ time, value: buf[0] });
    hi.push({ time, value: buf[buf.length - 1] });
    median.push({ time, value: medianOf(buf) });
  }
  const ti = topSeriesIndexes(live, 1, by)[0];
  const top = ti == null ? null : { label: live[ti].label, points: live[ti].points.map(p => ({ time: p.time, value: p.value })) };
  return { n: live.length, lo, hi, median, top };
}
