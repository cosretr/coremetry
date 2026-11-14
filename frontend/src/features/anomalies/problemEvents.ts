// problemEvents — v0.10.2163 (Davis tarzı tek Problem D4, spec
// docs/plans/spec-davis-style-problem-merge.md). Problem detayındaki OLAY
// ÇİZELGESİNİN metni, SAF ve tablo testli (problemEvents.test.ts); sayfa
// yalnız çizer.
//
// Servis başına tek Problem'e katılan her sinyal bir satır:
//   "14:02 Yaygın yavaşlama 30×"  ·  "14:07 Servis hata oranı (ML) 2,4×"
// Saat operatörün diliminde (çağıran biçimlendirir), ad dedektörün Türkçe
// adı, oran gözlenen / olağan (düşüşte tersi). Olağan değer yoksa oran
// YAZILMAZ — uydurma sayı yok. Sahibin kendi satırı "(açan)" ile işaretli.
//
// Kısa liste "Ne oluyor" kutusunun altında (≤ EVENT_LINES_SHOWN); fazlası ve
// ham değerler kapalı "Teknik kanıt"ta.
import type { ProblemEvent } from '@/lib/types';
import { ML_DETECTOR_TR } from '@/lib/mlDetectorLabels';

/** "Ne oluyor" altında gösterilen en çok satır (spec: ≤ 5). */
export const EVENT_LINES_SHOWN = 5;

// Eski dedektörlerin metrik anomalisi: kural kimliğinin son parçası metrik.
const METRIC_ANOMALY_TR: Record<string, string> = {
  error_rate: 'Hata oranı anomalisi',
  p99_ms: 'Gecikme (p99) anomalisi',
  request_rate: 'İstek hızı anomalisi',
};

/** SAF: satırın Türkçe adı. Üye dedektörleri (eski aileler) kendi adlarıyla;
 *  ML dedektörleri ML adı + "(ML)"; bilinmeyen dedektör kimliğiyle. */
export function problemEventName(e: Pick<ProblemEvent, 'detector' | 'ruleId'>): string {
  switch (e.detector) {
    case 'svc_slowdown':
      return 'Yaygın yavaşlama';
    case 'anomaly_promotion':
      return 'Anomali terfisi';
    case 'metric_anomaly': {
      const rule = e.ruleId ?? '';
      const metric = rule.slice(rule.lastIndexOf(':') + 1);
      return METRIC_ANOMALY_TR[metric] ?? 'Metrik anomalisi';
    }
  }
  const ml = ML_DETECTOR_TR[e.detector];
  return ml ? `${ml} (ML)` : e.detector;
}

/** SAF: sapma oranı ("3,1×"); olağan değer yoksa / oran anlamsızsa null. */
export function problemEventRatio(e: Pick<ProblemEvent, 'observed' | 'baseline' | 'direction'>): string | null {
  if (!(e.baseline > 0) || !(e.observed >= 0)) return null;
  let r = e.observed / e.baseline;
  if (e.direction === 'down') {
    if (!(e.observed > 0)) return null;
    r = e.baseline / e.observed;
  }
  if (!Number.isFinite(r) || r <= 0) return null;
  const s = r >= 100 ? Math.round(r).toLocaleString('tr-TR') : r.toLocaleString('tr-TR', { maximumFractionDigits: 1 });
  return `${s}×`;
}

export interface ProblemEventLine {
  key: string;
  /** "14:02 Yaygın yavaşlama (açan) 30×" */
  text: string;
  live: boolean;
  /** ML olayının detay yolu (/anomalies/ml/<id>); yoksa null. */
  href: string | null;
  /** Ham değerler ("gözlenen 0,034 · olağan 0,01"); değer yoksa null. Yalnız Teknik kanıtta. */
  raw: string | null;
}

const rawNum = (v: number) => v.toLocaleString('tr-TR', { maximumFractionDigits: 3 });

/** SAF: ham değer metni; ikisi de 0 ise (sahibin ölçüsü üyeden) null. */
export function problemEventRaw(e: Pick<ProblemEvent, 'observed' | 'baseline'>): string | null {
  if (!(e.observed > 0) && !(e.baseline > 0)) return null;
  return e.baseline > 0 ? `gözlenen ${rawNum(e.observed)} · olağan ${rawNum(e.baseline)}` : `gözlenen ${rawNum(e.observed)} · olağan değer yok`;
}

/** SAF: çizelge satırları (sunucu sırası = zaman sırası korunur). `clock`
 *  unix-ns → saat metni (çağıranın dilimi). */
export function problemEventLines(events: ProblemEvent[] | undefined, clock: (ns: number) => string): ProblemEventLine[] {
  return (events ?? []).map((e, i) => {
    const parts = [clock(e.startedAt), problemEventName(e) + (e.own ? ' (açan)' : '')];
    const ratio = problemEventRatio(e);
    if (ratio) parts.push(ratio);
    if (!e.live) parts.push('(bitti)');
    return {
      key: `${e.detector}:${e.ruleId ?? e.eventId ?? ''}:${e.startedAt}:${i}`,
      text: parts.join(' '),
      live: e.live,
      href: e.eventId ? `/anomalies/ml/${encodeURIComponent(e.eventId)}` : null,
      raw: problemEventRaw(e),
    };
  });
}

/** SAF: kısa liste + kalan sayısı ("ve N olay daha" metni çağıranda). */
export function splitEventLines(lines: ProblemEventLine[], shown = EVENT_LINES_SHOWN): { head: ProblemEventLine[]; rest: number } {
  return { head: lines.slice(0, shown), rest: Math.max(0, lines.length - shown) };
}
