// mlRecordText — v0.10.2248 (anomali kartı Dilim D, audit §10 D + C şablonu;
// mockup D0/G0 onaylı): kayıtlı olay kartının SAF metinleri. Her sayı kaydın
// bir alanından (başlık kuralı §9: ölçü başına tek sayı, ikincisi adıyla);
// metin üreten kod sorgu yapmaz. Diller i18n kataloğundan (tf). Kritik yol
// yüzdesi cümlesi YOK; oylar hiçbir metni ya da kararı sürmez (K3).
import { t, tf, type Lang } from '@/lib/i18n';
import { mlEventHref } from '@/lib/anomalyHref';
import { serviceHref } from '@/lib/serviceHref';
import { fmtClock, fmtDateTime } from '@/lib/utils';
import type { MLRecordContext, MLRecordEvidence, MLRecordGate, MLRecordView } from '@/lib/types';
import { fmtRecordNum, fmtRecordValue, peakAtSec, recordWindow, sourceLabel } from './mlRecord';

const hm = (ns: number) => fmtClock(new Date(ns / 1e6)).slice(0, 5);
const hmSec = (sec: number) => fmtClock(new Date(sec * 1000)).slice(0, 5);

/** SAF: süre metni ("12 dk" / "12 min", "2 sa 5 dk" / "2 h 5 min"). */
export function durText(ns: number, lang: Lang): string {
  const min = Math.max(0, Math.round(ns / 6e10));
  const [h, m] = [Math.floor(min / 60), min % 60];
  const H = lang === 'tr' ? 'sa' : 'h';
  const M = lang === 'tr' ? 'dk' : 'min';
  if (h === 0) return `${m} ${M}`;
  return m === 0 ? `${h} ${H}` : `${h} ${H} ${m} ${M}`;
}

export const recordOpen = (r: MLRecordView) => !r.endedAt;

/** SAF: telemetri hacmi kartı mı (sınıf ya da hacim dedektörü + sınıf). */
export const isTelemetryVolume = (r: MLRecordView) => r.class === 'telemetry_volume';

/** SAF: başlık çipindeki durum ("Ended · lasted 12 min" / "Ongoing · 14 min"). */
export function recordStatus(r: MLRecordView, nowNs: number, lang: Lang): string {
  return recordOpen(r)
    ? tf('mlRecord.card.statusOpen', lang, { d: durText(nowNs - r.onsetAt, lang) })
    : tf(r.endReason === 'no_data' ? 'mlRecord.card.statusNoData' : 'mlRecord.card.statusEnded', lang, { d: durText(r.endedAt! - r.onsetAt, lang) });
}

/** SAF: kartta gösterilen "şu an" değeri — sürerken son nokta, bitince tespit anı. */
export function headlineValue(r: MLRecordView): number {
  const v = r.snapshot.v;
  return recordOpen(r) && v.length ? v[v.length - 1] : r.valueAtDetection;
}

const mean = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : Number.NaN);

/** SAF: hacim şeridinin olay penceresi / öncesi ortalama oranı (ingest kat sayısı). */
export function ingestFactor(r: MLRecordView): number | null {
  const n = r.snapshot.n;
  const w = recordWindow(r);
  if (!n || n.length !== r.snapshot.v.length || !w || w.from === 0) return null;
  const before = mean(n.slice(0, w.from));
  const during = mean(n.slice(w.from, w.to + 1));
  return before > 0 && Number.isFinite(during) ? during / before : null;
}

/** SAF: yan serinin olay öncesi ve olay penceresi ortalaması. */
export function contextBeforeAfter(r: MLRecordView, c: MLRecordContext): { before: number; after: number } | null {
  const w = recordWindow(r);
  const v = c.snapshot.v;
  if (!w || w.from === 0 || v.length !== r.snapshot.v.length) return null;
  return { before: mean(v.slice(0, w.from)), after: mean(v.slice(w.from, w.to + 1)) };
}

/** SAF: servis etkisi kapısı "etki yok" dedi mi (yan seriler bant içinde). */
export function noServiceImpact(r: MLRecordView): boolean {
  const g = (r.gates ?? []).find(x => x.name === 'service_impact');
  return !!g && !g.notEvaluable && !g.passed;
}

/** SAF: plan dilde başlık. measure = ölçü adı (çağıran i18n'den). */
export function recordHeadline(r: MLRecordView, measure: string, lang: Lang): string {
  const f = (v: number) => fmtRecordValue(v, r.series.unit, lang);
  const open = recordOpen(r);
  if (isTelemetryVolume(r)) {
    const since = fmtDateTime(new Date(r.onsetAt / 1e6)).slice(0, 16);
    const a = fmtRecordNum(r.expectedValue, lang, 1);
    const b = fmtRecordNum(headlineValue(r), lang, 1);
    let s = tf(r.direction === 'down' ? 'mlRecord.card.volHeadDown' : 'mlRecord.card.volHeadUp', lang,
      { svc: r.subject.service, measure, a, b, since });
    if (noServiceImpact(r)) s += ' ' + tf('mlRecord.card.volUnchanged', lang);
    const k = ingestFactor(r);
    if (k != null && noServiceImpact(r)) s += ' ' + tf('mlRecord.card.volNoImpact', lang, { k: fmtRecordNum(k, lang, 1) });
    else if (k != null) s += ' ' + tf('mlRecord.card.volIngest', lang, { k: fmtRecordNum(k, lang, 1) });
    return s;
  }
  const who = r.subject.operation ? `${r.subject.operation} ${measure}` : measure.charAt(0).toUpperCase() + measure.slice(1);
  const calls = r.volume != null && r.detector !== 'svc_telemetry_volume'
    ? tf('mlRecord.card.calls', lang, { n: fmtRecordNum(r.volume, lang, 0) }) : '';
  // v0.10.2344 (operatör bulgusu: "bu gösterim anlaşılmıyor") — biten olayda
  // tespit anı değeri çoğu kez banda DÖNMÜŞ olur (tespit, bant dışı kovalar
  // onaylandıktan sonra gelir): "tespit anında 4,94; beklenen ≈ 5,78" sorun
  // yokmuş gibi okunuyordu. O durumda başlık bant dışına çıkan uç değerle
  // (tepe / dip ve saati) başlar; tespit anı değeri ikinci cümleye iner.
  if (!open && backInBandAtDetection(r)) {
    const at = peakAtSec(r);
    return tf(r.direction === 'down' ? 'mlRecord.card.headDipBack' : 'mlRecord.card.headPeakBack', lang, {
      who, peak: f(r.peakValue), t: at != null ? hmSec(at) : '—', calls, expected: f(r.expectedValue),
      band: `${f(r.baseline.lo)}–${f(r.baseline.hi)}`, value: f(r.valueAtDetection),
    });
  }
  return tf(open ? 'mlRecord.card.headNow' : 'mlRecord.card.headAtDetection', lang,
    { who, value: f(headlineValue(r)), calls, expected: f(r.expectedValue) });
}

/** SAF: tespit anı değeri bandın içinde VE kayıtlı uç değer bandın dışında mı. */
export function backInBandAtDetection(r: MLRecordView): boolean {
  const { lo, hi } = r.baseline;
  if (!Number.isFinite(lo) || !Number.isFinite(hi) || !(hi > lo)) return false;
  const v = r.valueAtDetection, p = r.peakValue;
  if (!Number.isFinite(v) || !Number.isFinite(p)) return false;
  return v >= lo && v <= hi && (p > hi || p < lo);
}

/** SAF: ikinci satır — başlangıç, tespit, bitiş + adlandırılmış tepe/dip. */
export function recordTimeline(r: MLRecordView, lang: Lang): string {
  const at = peakAtSec(r);
  const peak = tf(r.direction === 'down' ? 'mlRecord.card.dip' : 'mlRecord.card.peak', lang,
    { v: fmtRecordValue(r.peakValue, r.series.unit, lang), t: at != null ? hmSec(at) : '—' });
  const times = recordOpen(r)
    ? tf('mlRecord.card.timesOpen', lang, { onset: hm(r.onsetAt), det: hm(r.detectedAt) })
    : tf(r.endReason === 'no_data' ? 'mlRecord.card.timesNoData' : 'mlRecord.card.timesEnded', lang, { onset: hm(r.onsetAt), det: hm(r.detectedAt), end: hm(r.endedAt!) });
  return `${times} · ${peak}`;
}

/** SAF: bağımsız kanıt sayısı — A1 kapı satırı varsa onun değeri (K9 kuralı sunucuda). */
export function independentCount(r: MLRecordView): number {
  const g = (r.gates ?? []).find(x => x.name === 'independent_evidence');
  if (g && !g.notEvaluable) return g.value;
  return (r.evidence ?? []).filter(e => e.independent).length;
}

/** SAF: kanıt satırının etiketi — bağımsız / aynı seri ailesi / bağlam. */
export function evidenceTag(kind: string, independent: boolean): 'independent' | 'sameSeries' | 'context' {
  if (independent) return 'independent';
  return kind === 'series' ? 'sameSeries' : 'context';
}

/** SAF: kapı satırının ölçülen değer metni (birimli). */
export function gateValueText(g: MLRecordGate, r: MLRecordView, lang: Lang): string {
  if (g.notEvaluable) return '—';
  switch (g.name) {
    case 'visible_deviation': return tf('mlRecord.gate.buckets', lang, { n: g.value });
    case 'min_volume': return fmtRecordNum(g.value, lang, 0);
    case 'min_abs_diff': return fmtRecordValue(g.value, r.series.unit, lang);
    case 'service_impact': return tf('mlRecord.gate.minutes', lang, { n: fmtRecordNum(g.value, lang, 1) });
    default: return fmtRecordNum(g.value, lang);
  }
}

/** SAF: kapı eşiği metni. */
export function gateThresholdText(g: MLRecordGate, r: MLRecordView, lang: Lang): string {
  switch (g.name) {
    case 'series_consistency': return '0';
    case 'visible_deviation': return `≥ ${tf('mlRecord.gate.buckets', lang, { n: g.threshold })}`;
    case 'min_abs_diff': return `≥ ${fmtRecordValue(g.threshold, r.series.unit, lang)}`;
    case 'service_impact': return '> 0';
    default: return `≥ ${fmtRecordNum(g.threshold, lang)}`;
  }
}

/** SAF: kanıt satırının metni + bağlantısı — kayıt alanlarından, dile göre
 * (sunucu özeti Türkçe serbest metin; yalnız bilinmeyen türde aynen gösterilir).
 * Bağlantı süzülmüş sayfaya: servis / rollouts / öteki olay. */
export function evidenceText(e: MLRecordEvidence, r: MLRecordView, measure: string, lang: Lang): { text: string; href?: string } {
  const svc = r.subject.service;
  const tail = e.summary.includes(': ') ? e.summary.slice(e.summary.lastIndexOf(': ') + 2).trim() : '';
  const num = /(\d+)/.exec(e.summary)?.[1];
  switch (e.kind) {
    case 'series':
      return { text: tf('mlRecord.ev.series', lang, { measure, source: sourceLabel(r.series.source) }), href: serviceHref(svc) };
    case 'confirm':
      return { text: t('mlRecord.ev.confirm', lang), href: tail ? mlEventHref(tail) : undefined };
    case 'rollout':
      return { text: tf('mlRecord.ev.rollout', lang, { n: num ?? '?' }), href: serviceHref(svc, { tab: 'gitops' }) };
    case 'pods':
      return { text: tf('mlRecord.ev.pods', lang, { n: num ?? '?' }) };
    case 'member':
      return { text: tf('mlRecord.ev.member', lang, { op: tail || '—' }) };
    case 'error_rate':
    case 'latency': {
      const c = (r.context ?? []).find(x => x.measure === e.kind);
      const ba = c ? contextBeforeAfter(r, c) : null;
      const key = e.kind === 'error_rate' ? 'mlRecord.ev.ctxErr' : 'mlRecord.ev.ctxLat';
      return {
        text: tf(key, lang, ba && c
          ? { v: `${fmtRecordValue(ba.before, c.unit, lang)} → ${fmtRecordValue(ba.after, c.unit, lang)}` }
          : { v: '—' }),
        href: serviceHref(svc),
      };
    }
  }
  return { text: e.summary, href: e.link };
}
