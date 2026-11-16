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
import { mlDetectorLabel } from '@/lib/mlDetectorLabels';
import { t, tf, type Lang } from '@/lib/i18n'; // v0.10.2286 — satır metni lang alır (varsayılan 'tr')

/** "Ne oluyor" altında gösterilen en çok satır (spec: ≤ 5). */
export const EVENT_LINES_SHOWN = 5;

// Eski dedektörlerin metrik anomalisi: kural kimliğinin son parçası metrik.
// v0.10.2286 — adlar katalogda (pev.metric.<metrik>).
const METRIC_ANOMALY = new Set(['error_rate', 'p99_ms', 'request_rate']);
const LOCALE: Record<Lang, string> = { tr: 'tr-TR', en: 'en-US' };

/** SAF: satırın Türkçe adı. Üye dedektörleri (eski aileler) kendi adlarıyla;
 *  ML dedektörleri ML adı + "(ML)"; bilinmeyen dedektör kimliğiyle. */
export function problemEventName(e: Pick<ProblemEvent, 'detector' | 'ruleId'>, lang: Lang = 'tr'): string {
  switch (e.detector) {
    case 'svc_slowdown':
      return t('pev.svcSlowdown', lang);
    case 'anomaly_promotion':
      return t('pev.promotion', lang);
    case 'metric_anomaly': {
      const rule = e.ruleId ?? '';
      const metric = rule.slice(rule.lastIndexOf(':') + 1);
      return t(METRIC_ANOMALY.has(metric) ? `pev.metric.${metric}` : 'pev.metric', lang);
    }
  }
  const ml = mlDetectorLabel(e.detector, lang);
  return ml !== e.detector ? `${ml} (ML)` : e.detector;
}

/** SAF: sapma oranı ("3,1×"); olağan değer yoksa / oran anlamsızsa null. */
export function problemEventRatio(e: Pick<ProblemEvent, 'observed' | 'baseline' | 'direction'>, lang: Lang = 'tr'): string | null {
  if (!(e.baseline > 0) || !(e.observed >= 0)) return null;
  let r = e.observed / e.baseline;
  if (e.direction === 'down') {
    if (!(e.observed > 0)) return null;
    r = e.baseline / e.observed;
  }
  if (!Number.isFinite(r) || r <= 0) return null;
  const s = r >= 100 ? Math.round(r).toLocaleString(LOCALE[lang]) : r.toLocaleString(LOCALE[lang], { maximumFractionDigits: 1 });
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

const rawNum = (v: number, lang: Lang) => v.toLocaleString(LOCALE[lang], { maximumFractionDigits: 3 });

/** SAF: ham değer metni; ikisi de 0 ise (sahibin ölçüsü üyeden) null. */
export function problemEventRaw(e: Pick<ProblemEvent, 'observed' | 'baseline'>, lang: Lang = 'tr'): string | null {
  if (!(e.observed > 0) && !(e.baseline > 0)) return null;
  return e.baseline > 0
    ? tf('pev.raw', lang, { o: rawNum(e.observed, lang), b: rawNum(e.baseline, lang) })
    : tf('pev.rawNoBase', lang, { o: rawNum(e.observed, lang) });
}

/** SAF: çizelge satırları (sunucu sırası = zaman sırası korunur). `clock`
 *  unix-ns → saat metni (çağıranın dilimi). */
export function problemEventLines(events: ProblemEvent[] | undefined, clock: (ns: number) => string, lang: Lang = 'tr'): ProblemEventLine[] {
  return (events ?? []).map((e, i) => {
    const parts = [clock(e.startedAt), problemEventName(e, lang) + (e.own ? t('pev.own', lang) : '')];
    const ratio = problemEventRatio(e, lang);
    if (ratio) parts.push(ratio);
    if (!e.live) parts.push(t('pev.ended', lang));
    return {
      key: `${e.detector}:${e.ruleId ?? e.eventId ?? ''}:${e.startedAt}:${i}`,
      text: parts.join(' '),
      live: e.live,
      href: e.eventId ? `/anomalies/ml/${encodeURIComponent(e.eventId)}` : null,
      raw: problemEventRaw(e, lang),
    };
  });
}

/** SAF: kısa liste + kalan sayısı ("ve N olay daha" metni çağıranda). */
export function splitEventLines(lines: ProblemEventLine[], shown = EVENT_LINES_SHOWN): { head: ProblemEventLine[]; rest: number } {
  return { head: lines.slice(0, shown), rest: Math.max(0, lines.length - shown) };
}
