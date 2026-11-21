import { median } from '@/lib/normalFold';

// runtimeHeapView — v0.10.2148 (servis sekmeleri S9, docs/frontend/
// service-tabs-plan.md §4; plan K12 — operatör kararı 2026-11-14, seçenek B).
// Runtime bölümünün JVM heap kartı için SAF çekirdek: değer heap sınırının
// yüzdesi. v0.10.2436 — gri/renkli özet (heapSummary) kalktı; pod seçimi
// runtimePodReduce'ta (medyan + en kötü + eşik üstü), `?rtview=lines` = Tüm
// pod'lar (her runtime grafiğinde).
//
// Yüzde POD BAŞINA ve kendi sınırına göre: kullanım ve sınır aynı pod için
// havuzların TOPLAMI olarak okunur (agg=sum, pod'a göre gruplu) ve bölünür —
// sunucudaki heap bandının (anomaly.HeapPctByPod) aynı hesabı. Ortalama
// (agg=avg) burada YANLIŞ olurdu: sınır bildirmeyen havuzlar (G1'de eden /
// survivor) paydayı havuz sayısı kadar küçültür. Sınır serisi yoksa (sınırsız
// JVM) yüzdeye çevrilmez: mutlak değer (ortalama okumasından), sınır çizgisi yok.

export interface HeapPoint { time: number; value: number | null }
export interface HeapPodLine { label: string; points: HeapPoint[] }
// ── ?rtview ── Virgüllü küme: `lines` = Tüm pod'lar (her runtime grafiği +
// non-heap grafiği açık); `nonheap` = yalnız non-heap grafiğini aç (S10).
// Bilinmeyen değer yok sayılır (varsayılan görünüm).
export interface RtView { lines: boolean; nonheap: boolean }
export function parseRtView(v: string | null | undefined): RtView {
  const set = new Set((v ?? '').split(',').map(x => x.trim()).filter(Boolean));
  return { lines: set.has('lines'), nonheap: set.has('nonheap') };
}
/** Parametre değeri; varsayılan görünümde '' (parametre silinir). */
export function rtViewParam(v: RtView): string {
  return [v.lines ? 'lines' : '', v.nonheap && !v.lines ? 'nonheap' : ''].filter(Boolean).join(',');
}

const finite = (v: number | null | undefined): v is number => v != null && Number.isFinite(v);

/**
 * Kullanım noktalarını sınırın yüzdesine çevirir. Sınır aynı zaman noktasında
 * yoksa en son bilinen (o da yoksa ilk bilinen) değeri kullanılır; sınır hiç
 * yoksa / 0 ise nokta null (uydurma yüzde yok).
 */
export function toPercentOfLimit(points: readonly HeapPoint[], limit: readonly HeapPoint[]): HeapPoint[] {
  const lim = limit.filter(p => finite(p.value) && p.value > 0).sort((a, b) => a.time - b.time);
  if (lim.length === 0) return points.map(p => ({ time: p.time, value: null }));
  let j = 0;
  const sorted = [...points].sort((a, b) => a.time - b.time);
  return sorted.map(p => {
    while (j + 1 < lim.length && lim[j + 1].time <= p.time) j++;
    const l = lim[j].value as number;
    return { time: p.time, value: finite(p.value) ? (p.value / l) * 100 : null };
  });
}

/** Pod kimliğiyle (grup anahtarı) eşlenen ham seri. */
export interface HeapKeyedLine { key: string; label: string; points: HeapPoint[] }

/**
 * Pod başına yüzde: her pod KENDİ sınır serisine bölünür (anahtar eşleşmesi).
 * Sınırı olmayan pod için yüzde hesaplanamaz — listeye girmez (uydurma yok).
 */
export function podPercentLines(used: readonly HeapKeyedLine[], limit: readonly HeapKeyedLine[]): HeapPodLine[] {
  const byKey = new Map(limit.map(l => [l.key, l.points]));
  const out: HeapPodLine[] = [];
  for (const u of used) {
    const lim = byKey.get(u.key);
    if (!lim) continue;
    const points = toPercentOfLimit(u.points, lim);
    if (points.some(p => finite(p.value))) out.push({ label: u.label, points });
  }
  return out;
}

// ── S10: "düz" seri ── (max − min) / ortanca, pencere boyunca. Eşik SABİT
// (ayar değil, plan K15).
export const FLAT_THRESHOLD = 0.10;

/** Serinin göreli oynaklığı; değer yoksa ya da ortanca 0 ise null. */
export function relativeSpread(points: readonly HeapPoint[]): number | null {
  const vs = points.map(p => p.value).filter(finite);
  if (vs.length === 0) return null;
  const med = median(vs);
  if (med <= 0) return null;
  return (Math.max(...vs) - Math.min(...vs)) / med;
}

export interface FlatSummary { pods: number; min: number; max: number }

/**
 * Tüm seriler düzse özet (pod sayısı + değer aralığı), aksi hâlde null.
 * Tek bir seri bile eşiği aşıyorsa ya da ölçülemiyorsa grafik çizilir.
 */
export function flatSummary(series: readonly HeapPodLine[], threshold: number = FLAT_THRESHOLD): FlatSummary | null {
  let min = Infinity, max = -Infinity, pods = 0;
  for (const s of series) {
    const vs = s.points.map(p => p.value).filter(finite);
    if (vs.length === 0) continue; // değersiz seri sayılmaz
    const spread = relativeSpread(s.points);
    if (spread == null || spread >= threshold) return null;
    pods++;
    min = Math.min(min, ...vs);
    max = Math.max(max, ...vs);
  }
  return pods > 0 ? { pods, min, max } : null;
}
