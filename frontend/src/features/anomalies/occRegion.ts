// occRegion — v0.10.1365 (operatör: "Burada neden pencere boyu yazıyor … Devam
// etmiyorsa bittiyse kapanır, resolve onu gösterir. Şu an anlamsız pencere
// boyu yazması"). Exception detayı Occurrences grafiğindeki durum bandı.
//
// Eskiden: çözülmemiş grupta bant firstSeen → grafiğin SON bucket'ı (sıfır
// dolgu şimdiye uzanıyor) çiziliyordu; 08:39'da 6 kez görülüp susan grup
// bile grafiğin tamamını kaplıyor, kronik kuralı "· pencere boyu" ekliyordu.
// Şimdi bant grubun GERÇEK hâlini söyler:
//   • resolved            → firstSeen → resolvedAt, etiket durum ("RESOLVED")
//   • hâlâ görülüyor      → firstSeen → grafik sonu, "NEW · sürüyor"
//     (son görülme son ~2 bucket içinde)
//   • sustu               → firstSeen → son görülmenin bucket sonu,
//     "NEW · son görülme 08:39"
// Etiket durumu kendisi söylediği için kronik "· pencere boyu" eki basılmaz
// (noChronicLabel). SAF — occRegion.test.ts.
import type { ChartTimeRegion } from '@/lib/chart/overlays';
import { fmtOccTick } from './occTick';
import { t as tk, tf, type Lang } from '@/lib/i18n'; // v0.10.2287 — bant etiketi dile göre (varsayılan 'tr')

export interface OccRegionInput {
  firstSeenNs: number;
  lastSeenNs: number;
  resolvedAtNs?: number;
  /** Durum etiketi (STATE_LABEL[state]); resolved grupta "RESOLVED". */
  stateLabel: string;
  /** Grafiğin bucket başları (sn), artan. */
  occTimes: number[];
  nowSec: number;
  lang?: Lang;
}

/** Son görülme bu kadar bucket içindeyse grup "sürüyor" sayılır. */
export const OCC_ACTIVE_BUCKETS = 2;

function hhmm(sec: number, nowSec: number): string {
  const full = fmtOccTick(sec); // "dd.mm HH:MM"
  return fmtOccTick(nowSec).slice(0, 5) === full.slice(0, 5) ? full.slice(6) : full;
}

export function occRegion(o: OccRegionInput): ChartTimeRegion | undefined {
  const t = o.occTimes;
  if (t.length === 0) return undefined;
  const step = t.length >= 2 ? Math.max(1, t[1] - t[0]) : 60;
  const fromSec = o.firstSeenNs / 1e9;
  const lastSec = o.lastSeenNs / 1e9;
  const chartEnd = t[t.length - 1] + step;
  let toSec: number;
  let endSec: number;
  let label: string;
  if (o.resolvedAtNs) {
    endSec = toSec = o.resolvedAtNs / 1e9;
    label = o.stateLabel;
  } else if (o.nowSec - lastSec <= OCC_ACTIVE_BUCKETS * step) {
    toSec = chartEnd;
    endSec = o.nowSec;
    label = `${o.stateLabel} · ${tk('exc.occ.ongoing', o.lang ?? 'tr')}`;
  } else {
    // Bucket sonu: tek bucket'lık patlamada da bant görünür genişlikte kalır.
    toSec = Math.min(chartEnd, lastSec + step);
    endSec = lastSec;
    label = `${o.stateLabel} · ${tf('exc.occ.lastSeen', o.lang ?? 'tr', { ts: hhmm(lastSec, o.nowSec) })}`;
  }
  if (!(toSec > fromSec)) return undefined;
  return { fromSec, toSec, endSec, color: 'var(--err)', label, noChronicLabel: true };
}
