// MLTab — /anomalies "Log ML" sekmesi (log ML dilim 0). components/coremetry
// altında çünkü Tailwind sınıfları yalnız burada üretiliyor (features/ değil).
// Eski dedektörler kendi sekmesinde; burada yalnız ml_events (gölge) +
// satır başına «gerçek / değil» + not (anomaly_verdicts 'ml:' önekli) ve
// üstte dedektör karnesi (30 gün isabet + örnek). Tablo: coremetry DataTable.
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { useMLScorecard } from '@/lib/queries';
import type { MLEvent, MLDetectorScore, MLBackfillProgress, MLTickStatus, MLSignalTick, MLScorecard, MLKindTick, MLBaselineStatus, MLThanosTick } from '@/lib/types';
import { MLPublishGatesSection } from './MLPublishGates';
import { MLSvcShadowCompareSection } from './MLSvcShadowCompare';
import { EngineCompareSection } from './EngineCompare';
import { ProblemGraphShadowSection } from './ProblemGraphShadow';
import { lazy, Suspense } from 'react';
import { useAuthRole } from '@/components/AuthProvider';
import { SIGNAL_KINDS } from '@/lib/signalKind';
import { SignalKindBadge } from '@/components/coremetry/SignalKindBadge';
import { ML_DETECTOR_TR, mlDetectorLabel } from '@/lib/mlDetectorLabels';
import { t as tr, tf, useLang, type Lang } from '@/lib/i18n';

// v0.10.2392 — etki kuralı geri oynatma: yalnız admin, tembel parça (bundle bütçesi).
const ImpactReplaySection = lazy(() => import('./ImpactReplay'));

// v0.10.2163 — harita lib/mlDetectorLabels.ts'te (Problem olay çizelgesi de okur;
// bu ağır modülü Problems parçasına çekmesin). Buradan yeniden dışa açılır.
export { ML_DETECTOR_TR };
export type MLSignal = '' | 'log' | 'trace' | 'metric';
/** SAF: ?signal= çözümü; bilinmeyen → hepsi. */
export function resolveMLSignal(raw: string | null): MLSignal {
  return raw === 'log' || raw === 'trace' || raw === 'metric' ? raw : '';
}

/** Karne satırı metni — SAF (test): örnek yoksa isabet yazılmaz (S4). */
export function scoreLabel(s: MLDetectorScore): string {
  const name = ML_DETECTOR_TR[s.detector] ?? s.detector;
  if (s.samples <= 0) return `${name}: ${s.events} olay · karar yok`;
  return `${name}: ${s.events} olay · %${Math.round(s.hitRate * 100)} isabet (${s.samples} karar)`;
}

// v0.10.1844 — sinyal adları tek yerden (lib/signalKind.ts).
const SIGNAL_TR: Record<string, string> = { log: SIGNAL_KINDS.log.label, trace: SIGNAL_KINDS.trace.label, metric: SIGNAL_KINDS.metric.label };
// v0.10.2282 — arayüz dilinde sinyal adı (TR yukarıdaki; EN signalKind.*).
const signalName = (k: string, lang: Lang): string | undefined =>
  lang === 'tr' ? SIGNAL_TR[k] : (k in SIGNAL_TR ? tr(`signalKind.${k}`, lang) : undefined);
// v0.10.2282 — ekranın metinleri lib/i18n mlTab.* (TR metin birebir aynı).
const M = (k: string, lang: Lang, vars?: Record<string, string | number>) => tf(`mlTab.${k}`, lang, vars);
const loc = (lang: Lang) => (lang === 'tr' ? 'tr-TR' : 'en-US');

/** Sinyal karnesi satırı — SAF (test). */
export function signalScoreLabel(s: MLDetectorScore): string {
  const name = SIGNAL_TR[s.detector] ?? s.detector;
  if (s.samples <= 0) return `${name}: ${s.events} olay · karar yok`;
  return `${name}: %${Math.round(s.hitRate * 100)} isabet (${s.samples} karar / ${s.events} olay)`;
}

/** Backfill satırı — SAF (test). */
export function backfillLabel(b: MLBackfillProgress): string {
  const miss = b.missing?.length ?? 0;
  const head = `${SIGNAL_TR[b.signal] ?? b.signal} geçmişi: ${b.done + miss}/${b.total} saat`;
  const tail = miss ? ` · ${miss} eksik` : '';
  if (b.complete) return `${head}${tail} · tamam`;
  if (b.running) return `${head}${tail} · sürüyor`;
  return `${head}${tail} · duraklı${b.lastError ? ` (${b.lastError})` : ''}`;
}

/** SAF (test): v0.10.2019 — tikin Thanos ölçümü (VM sayacından ayrı). */
export function thanosTickText(t: MLThanosTick, lang: Lang = 'tr'): string {
  const bits = [M('thanos', lang, { q: t.queries, s: (t.ms / 1000).toFixed(1), m: (t.maxMs / 1000).toFixed(1) })];
  if (t.errors) bits.push(M('thanos.errors', lang, { c: t.errors }));
  if (t.budgetCut) bits.push(M('thanos.budget', lang, { c: t.budgetCut }));
  if (t.unmatched) bits.push(M('thanos.unmatched', lang, { c: t.unmatched }));
  if (t.failed?.length) bits.push(M('thanos.failed', lang, { list: t.failed.join(', ') }));
  return bits.join(' · ');
}

/** v0.10.2053 (D4) — SAF: grup → Problem tik özeti. Canlı kipte açılan /
 *  kapanan sayısı (izleyici de görür); kapalıyken gölge sayacı. Boş = satır yok. */
export function groupTickText(t: MLTickStatus, lang: Lang = 'tr'): string {
  // v0.10.2058: tek sinyal kolu yalnız gölgede — her iki kipte ayrı sayaç
  // v0.10.2092: tek sinyal kolu canlı — canlı kipte AÇILAN sayısı, kapalıyken "açardı".
  const single = (t.groupLive ? (t.groupSingleOpened ? M('group.singleOpened', lang, { c: t.groupSingleOpened }) : '')
    : (t.shadowSingleWouldOpen ? M('group.singleWould', lang, { c: t.shadowSingleWouldOpen }) : '')) +
    // v0.10.2089: servis düzeyi kapı (svc_error_rate / svc_breadth). v0.10.2130: canlı kipte AÇILAN sayısı.
    (t.groupLive ? (t.svcOpened ? M('group.svcOpened', lang, { c: t.svcOpened }) : '')
      : (t.shadowSvcWouldOpen ? M('group.svcWould', lang, { c: t.shadowSvcWouldOpen, p1: t.shadowSvcP1 ? `, ${t.shadowSvcP1} P1` : '' }) : ''));
  if (t.groupLive) {
    const bits = [M('group.opened', lang, { c: t.groupOpened ?? 0 }), M('group.ongoing', lang, { c: t.groupRefreshed ?? 0 }), M('group.resolved', lang, { c: t.groupResolved ?? 0 })];
    return M('group.live', lang, { bits: bits.join(', '), ev: t.shadowGroups ? M('group.evaluated', lang, { c: t.shadowGroups }) : '', single });
  }
  return t.shadowGroups ? M('group.shadow', lang, { g: t.shadowGroups, o: t.shadowWouldOpen ?? 0, single }) : '';
}

/** Son tik + VM bekleme satırı — SAF (test). nowSec: şimdi (unix sn). */
export function tickLabel(t: MLTickStatus | undefined, nowSec: number): string {
  if (!t) return 'son tik yok';
  const ago = Math.max(0, Math.round((nowSec - t.at) / 60));
  const parts = [`son tik ${ago} dk önce · ${Math.round(t.durationMs / 1000)} sn · ${t.events} olay`];
  if (t.docs) parts.push(`${t.docs.toLocaleString('tr-TR')} belge`);
  if (t.partial) parts.push(`${t.partial} kısmi ES sonucu (yazılmadı)`);
  if (t.batchCut) parts.push(`batch kuralı ${t.batchCut} adayı kesti`);
  const gt = groupTickText(t);
  if (gt) parts.push(gt);
  if (t.thanos?.queries) parts.push(thanosTickText(t.thanos));
  if (t.backoff.length) parts.push(`VM bekleme: ${t.backoff.map(b => `${b.family} (${Math.max(1, Math.round((b.until - nowSec) / 60))} dk)`).join(', ')}`);
  if (t.lastError) parts.push(`hata: ${t.lastError}`);
  return parts.join(' · ');
}

/** Dedektör başına son tik satırı — SAF (test): "0 olay" ile "çalışmadı" ayrımı. */
export function signalTickLabel(name: string, s: MLSignalTick): string {
  if (!s.ran) return `${name}: çalışmadı`;
  const bits = [`${s.services} servis`, `${s.candidates} aday`, `${s.written} yazıldı`, `${(s.sourceMs / 1000).toFixed(1)} sn`];
  if (s.batchCut) bits.push(`${s.batchCut} batch kesildi`);
  if (s.partial) bits.push('kısmi ES');
  if (s.error) bits.push(`hata: ${s.error}`);
  return `${name}: ${bits.join(' · ')}`;
}

/** Durum etiketi — SAF (test). B3: closed = epizot kapandı. */
export function stageLabel(stage: string): string {
  return stage === 'promoted' ? 'terfi' : stage === 'closed' ? 'kapandı' : stage === 'chronic' ? 'kronik' : 'açık';
}
/** Süre (ns) → kısa metin — SAF (test). */
// v0.10.2282 — lang (varsayılan 'tr'); anomalies.dur.* (mlGroupRows ile aynı kalıp).
export function durationLabel(ns: number, lang: Lang = 'tr'): string {
  const m = Math.max(0, Math.round(ns / 60e9));
  if (m < 60) return tf('anomalies.dur.m', lang, { m });
  const h = Math.floor(m / 60);
  return h < 48 ? tf('anomalies.dur.hm', lang, { h, m: m % 60 }) : tf('anomalies.dur.d', lang, { d: Math.round(h / 24) });
}

/** Dedektör → recorder dedektör grubu (tik özeti anahtarı) — SAF. */
export function detectorGroup(d: string): string {
  if (d.startsWith('metric_')) return 'metric_ml';
  switch (d) {
    case 'log_volume': case 'error_rate': case 'service_silent': return 'log_red';
    case 'template_spike': case 'rare_template': return 'log_templates';
    case 'rare_error_code': case 'pod_outlier': return 'log_breakdown';
    case 'change_point': return 'log_change_point';
    case 'new_template': return 'new_template'; // v0.10.2069: eski log_template_new kararı
    case 'op_latency': case 'op_error_rate': case 'throughput_drop': case 'svc_error_rate': case 'svc_response_time': case 'endpoint_impact': return 'trace_red';
    case 'dependency_latency': case 'dependency_error_rate': return 'dependency';
    case 'trace_error_count': return 'trace_errors'; // v0.10.2018
    case 'svc_breadth': return 'svc_breadth'; // v0.10.2086: türetilmiş, kendi tik satırı
    default: return 'trace_pods';
  }
}

export interface DetectorRow { detector: string; signal: string; enabled: boolean; events: number; samples: number; hitRate: number; tick?: MLSignalTick;
  /** v0.10.2063: son tikte kaynağı yoktu ("ölçülemedi: …"); sağlıklı DEĞİL. */
  unmeasured?: string }

/** Karne satırları — SAF (test): tüm dedektörler, olay yoksa 0; açık/kapalı sunucunun `enabled` haritasından. */
export function detectorRows(sc: MLScorecard, signal: MLSignal): DetectorRow[] {
  const byDet = new Map(sc.detectors.map(d => [d.detector, d]));
  return Object.keys(sc.signals ?? {}).filter(d => !signal || sc.signals[d] === signal).map(d => {
    const sc1 = byDet.get(d);
    const tick = sc.tick?.signals?.[detectorGroup(d)];
    return { detector: d, signal: sc.signals[d], enabled: sc.enabled?.[d] === true,
      events: sc1?.events ?? 0, samples: sc1?.samples ?? 0, hitRate: sc1?.hitRate ?? 0, tick, unmeasured: tick?.unmeasured?.[d] };
  });
}

/** Durum kutuları — SAF (test). */
export function statusTiles(sc: MLScorecard, signal: MLSignal, nowSec: number, lang: Lang = 'tr'): { k: string; v: string; s: string; warn?: boolean }[] {
  const t = sc.tick;
  const ago = t ? Math.max(0, Math.round((nowSec - t.at) / 60)) : null;
  const bf = (sc.backfill ?? []).find(b => !signal || b.signal === signal);
  const miss = bf?.missing?.length ?? 0;
  const events = (sc.bySignal ?? []).filter(s => !signal || s.detector === signal).reduce((a, s) => a + s.events, 0);
  const samples = (sc.bySignal ?? []).filter(s => !signal || s.detector === signal).reduce((a, s) => a + s.samples, 0);
  const bo = t?.backoff ?? [];
  return [
    { k: M('tile.tick', lang), v: t ? M('tile.ago', lang, { c: ago ?? 0 }) : M('none', lang), s: t ? `${M('sec', lang, { s: Math.round(t.durationMs / 1000) })} · ${M('events', lang, { c: t.events })}${t.docs ? ` · ${M('docs', lang, { c: t.docs.toLocaleString(loc(lang)) })}` : ''}${t.partial ? ` · ${M('partial', lang, { c: t.partial })}` : ''}${t.batchCut ? ` · ${M('batchCut', lang, { c: t.batchCut })}` : ''}${groupTickText(t, lang) ? ` · ${groupTickText(t, lang)}` : ''}${t.thanos?.queries ? ` · ${thanosTickText(t.thanos, lang)}` : ''}${t.contribQueries ? ` · ${M('contrib', lang, { c: t.contribQueries })}` : ''}${t.logSvcUnmatched ? ` · ${M('logSvcUnmatched', lang, { c: t.logSvcUnmatched })}` : ''}${t.logSvcViaPod ? ` · ${M('logSvcViaPod', lang, { c: t.logSvcViaPod })}` : ''}` : M('tile.tickNone', lang), warn: !!t?.lastError },
    { k: M('tile.backfill', lang), v: bf ? M('tile.backfillV', lang, { a: bf.done + miss, b: bf.total }) : '—', s: bf ? M(bf.complete ? 'tile.complete' : bf.running ? 'tile.running' : 'tile.paused', lang) + (miss ? M('tile.missing', lang, { c: miss }) : '') : M('tile.noRecord', lang) },
    { k: M('tile.events', lang), v: String(events), s: samples ? M('tile.verdicts', lang, { c: samples }) : M('tile.noVerdicts', lang) },
    { k: M('tile.vm', lang), v: bo.length ? M('tile.families', lang, { c: bo.length }) : M('none', lang), s: bo.map(b => M('tile.famMin', lang, { f: b.family, c: Math.max(1, Math.round((b.until - nowSec) / 60)) })).join(', ') || M('tile.allOpen', lang), warn: bo.length > 0 },
  ];
}

const KIND_ORDER = ['log', 'trace', 'metric'];

/** Sinyal türü başına son tik ölçümü — SAF (test). v0.10.1812, audit §10 dilim 1.1. */
export function kindTickLines(t: MLTickStatus | undefined, signal: MLSignal, lang: Lang = 'tr'): { kind: string; text: string; warn: boolean }[] {
  const kinds = t?.kinds ?? {};
  return Object.keys(kinds).filter(k => !signal || k === signal)
    .sort((a, b) => (KIND_ORDER.indexOf(a) + 1 || 99) - (KIND_ORDER.indexOf(b) + 1 || 99) || a.localeCompare(b))
    .map(kind => {
      const k: MLKindTick = kinds[kind];
      const n = (v: number) => v.toLocaleString(loc(lang));
      const bits = [M('kind.services', lang, { c: n(k.services) }), M('kind.series', lang, { c: n(k.series), r: n(k.seriesRows) }), M('kind.queries', lang, { es: k.es, vm: k.vm, ch: k.ch }), M('sec', lang, { s: (k.durationMs / 1000).toFixed(1) })];
      if (k.dropped) bits.push(M('kind.dropped', lang, { sig: signalName(kind, lang)?.toLowerCase() ?? kind, c: n(k.dropped) }));
      return { kind, text: `${signalName(kind, lang) ?? kind} · ${bits.join(' · ')}`, warn: k.dropped > 0 };
    });
}

/** Günlük baseline işinin tek satırı — SAF (test). v0.10.1814, audit §4.2. null = durum kaydı yok (iş hiç koşmadı / kapalı). */
export function baselineStatusLine(st: MLBaselineStatus | undefined, lang: Lang = 'tr'): { text: string; warn: boolean } | null {
  if (!st) return null;
  const n = (v: number) => v.toLocaleString(loc(lang));
  const run = st.last;
  const bits: string[] = [];
  if (st.lastDay) bits.push(M('base.lastDay', lang, { d: st.lastDay }));
  if (run) bits.push(M('base.rows', lang, { c: n(run.rows) }), M('sec', lang, { s: (run.durationMs / 1000).toFixed(1) }));
  if (st.running) bits.push(M('base.processing', lang, { x: st.running }));
  if (run?.error) bits.push(M('base.failed', lang, { d: run.day, err: run.error }));
  if (st.manual) bits.push(M('base.manual', lang, { d: st.manual.day, x: st.manual.error ? M('base.error', lang, { err: st.manual.error }) : M('base.rows', lang, { c: n(st.manual.rows) }) }));
  if (st.profiles) bits.push(st.profiles.error ? M('base.profileFailed', lang, { w: st.profiles.week, err: st.profiles.error }) : M('base.profile', lang, { w: st.profiles.week, s: n(st.profiles.services), c: n(st.profiles.changed) }));
  if (!bits.length) bits.push(M('base.none', lang));
  return { text: `Baseline · ${bits.join(' · ')}`, warn: !!run?.error || !!st.manual?.error || !!st.profiles?.error };
}

// ── v0.10.1879 — sade görünüm (operatör, prod ekran görüntüsü: "Bu sayfa genel
// tasarıma uymuyor, çok net anlaşılmıyor"). Önce düz cümle, sonra dört sorunun
// cevabını veren tablo (ne izliyor / açık mı / ne buldu); tik süreleri ve
// aday sayıları "Teknik detay" altında kapalı. v0.10.2067: "Problem açar mı"
// sütunu (dedektör başına terfi kapısı) kopya yoluyla birlikte kalktı.

/** Durumun düz cümlesi + (varsa) dikkat satırları — SAF (test). */
export function mlStatusSummary(sc: MLScorecard, signal: MLSignal, nowSec: number, lang: Lang = 'tr'): { text: string; warnings: string[] } {
  const n = (v: number) => v.toLocaleString(loc(lang));
  const t = sc.tick;
  const scoped = (sc.bySignal ?? []).filter(x => !signal || x.detector === signal);
  const events = scoped.reduce((a, x) => a + x.events, 0);
  const samples = scoped.reduce((a, x) => a + x.samples, 0);
  const warnings: string[] = [];
  let head: string;
  if (!t) {
    head = M('notRun', lang);
    warnings.push(M('noTick', lang));
  } else {
    const ago = Math.max(0, Math.round((nowSec - t.at) / 60));
    const dur = Math.max(1, Math.round(t.durationMs / 1000));
    const when = ago === 0 ? M('justNow', lang) : M('minAgo', lang, { c: n(ago) });
    head = M('running', lang, { when, dur: n(dur), found: t.events > 0 ? M('found', lang, { c: n(t.events) }) : M('foundNone', lang) });
    if (ago > 15) {
      head = M('late', lang, { c: n(ago) });
      warnings.push(M('lateWarn', lang));
    }
    const bo = t.backoff ?? [];
    if (bo.length > 0) warnings.push(M('backoffWarn', lang, { c: n(bo.length), fams: bo.map(b => b.family).join(', ') }));
    if (t.partial) warnings.push(M('partialWarn', lang, { c: n(t.partial) }));
  }
  const bf = (sc.backfill ?? []).find(b => !signal || b.signal === signal);
  if (bf && !bf.complete) warnings.push(M('backfillWarn', lang, { done: n(bf.done + (bf.missing?.length ?? 0)), total: n(bf.total), state: M(bf.running ? 'backfillRunning' : 'backfillPaused', lang) }));
  const tail = events > 0
    ? M('tail', lang, { days: sc.windowDays, c: n(events), judged: samples > 0 ? M('tailJudged', lang, { c: n(samples) }) : M('tailNone', lang) })
    : M('tailEmpty', lang, { days: sc.windowDays });
  return { text: head + tail, warnings };
}

/** Değerlendirme hücresi — SAF (test): "%0 (2)" yerine "2 değerlendirme · 0 doğru". */
export function detectorVerdictText(r: Pick<DetectorRow, 'samples' | 'hitRate'>, lang: Lang = 'tr'): string {
  if (r.samples <= 0) return '';
  return M('verdictText', lang, { s: r.samples.toLocaleString(loc(lang)), c: Math.round(r.hitRate * r.samples).toLocaleString(loc(lang)) });
}

// v0.10.2282 — başlıklar ve dedektör adları (sıralama da görünene göre) arayüz dilinde.
const dcolsFor = (lang: Lang): ColumnDef<DetectorRow>[] => [
  { id: 'detector', label: M('col.detector', lang), width: 220, minWidth: 150, flex: true, priority: 1, sortValue: r => mlDetectorLabel(r.detector, lang), naturalDir: 'asc' },
  { id: 'signal', label: M('col.signal', lang), width: 110, minWidth: 90, priority: 1, sortValue: r => r.signal, naturalDir: 'asc' },
  // Açık olanlar önde, kendi içinde bulduğu olay sayısına göre.
  { id: 'state', label: tr('anomalies.col.status', lang), width: 100, minWidth: 80, priority: 1, sortValue: r => (r.enabled ? 1e9 : 0) + r.events },
  { id: 'events', label: M('col.events', lang), width: 150, minWidth: 110, priority: 1, numeric: true, sortValue: r => r.events },
  { id: 'hit', label: M('col.hit', lang), width: 200, minWidth: 130, priority: 2, sortValue: r => (r.samples > 0 ? r.hitRate : -1) },
];
// v0.10.2307 — İngilizce katalog tembel (lib/i18n.ts): EN kolonları modül
// yüklenirken değil ilk kullanımda (etkin dil 'en' = katalog yüklü) kurulur.
const DCOLS_BY_LANG: Partial<Record<Lang, ColumnDef<DetectorRow>[]>> = {};
const dcolsOf = (lang: Lang): ColumnDef<DetectorRow>[] => (DCOLS_BY_LANG[lang] ??= dcolsFor(lang));

// Teknik detay tablosu: son kontrolün dedektör başına ölçümü.
const dtechFor = (lang: Lang): ColumnDef<DetectorRow>[] => [
  { id: 'detector', label: M('col.detector', lang), width: 180, minWidth: 140, priority: 1, sortValue: r => mlDetectorLabel(r.detector, lang), naturalDir: 'asc' },
  { id: 'tick', label: M('col.tick', lang), width: 110, numeric: true, priority: 2, sortValue: r => r.tick?.sourceMs ?? -1 },
  { id: 'services', label: M('col.services', lang), width: 110, numeric: true, priority: 2, sortValue: r => r.tick?.services ?? -1 },
  { id: 'candidates', label: M('col.candidates', lang), width: 70, numeric: true, priority: 3, sortValue: r => r.tick?.candidates ?? -1 },
  { id: 'written', label: M('col.written', lang), width: 90, numeric: true, priority: 3, sortValue: r => r.tick?.written ?? -1 },
];
// v0.10.2307 — İngilizce katalog tembel (lib/i18n.ts): EN kolonları modül
// yüklenirken değil ilk kullanımda (etkin dil 'en' = katalog yüklü) kurulur.
const DTECH_BY_LANG: Partial<Record<Lang, ColumnDef<DetectorRow>[]>> = {};
const dtechOf = (lang: Lang): ColumnDef<DetectorRow>[] => (DTECH_BY_LANG[lang] ??= dtechFor(lang));

/** SAF (test): seyrin tepe değeri (sıralama); seyir yoksa -1. */
export function sparkPeak(e: MLEvent): number {
  return e.spark?.v.length ? Math.max(...e.spark.v) : -1;
}

/** SAF (test): satır metni — sunucu özeti; yoksa (eski sunucu) servis · dedektör adı. Cümle KURMAZ. */
export function mlRowText(e: MLEvent, lang: Lang = 'tr'): string {
  return e.summary || `${e.service} · ${mlDetectorLabel(e.detector, lang)}`;
}

// v0.10.2027 — /anomalies tek sayfa: olay listesi UnifiedAnomalies'te; MLTab yalnız
// ML dedektör durumu (özet, karne, teknik detay, dönem oynatma) — sayfanın kapalı
// "Teknik ayrıntı / Dedektör durumu" bölümünde.
export function MLTab({ signal = '', onSignal }: { signal?: MLSignal; onSignal?: (s: MLSignal) => void }) {
  const sc = useMLScorecard();
  const lang = useLang();
  const isAdmin = useAuthRole() === 'admin';
  return (
    <div data-testid="ml-tab" className="flex flex-col gap-3">
      {onSignal && (
        <SegmentedControl<MLSignal> value={signal} onChange={onSignal} aria-label={tr('anomalies.signal.aria', lang)} size="sm"
          options={[{ value: '', label: tr('anomalies.signal.all', lang) }, { value: 'log', label: 'Log' }, { value: 'trace', label: 'Trace' }, { value: 'metric', label: tr('signalKind.metric', lang) }]} />
      )}
      {sc.data && (() => {
        const sum = mlStatusSummary(sc.data, signal, Math.floor(Date.now() / 1000), lang);
        return (
          <div data-testid="ml-status-summary" className="rounded-md border border-border bg-card px-3 py-2">
            <p className="m-0 text-[length:var(--fs-md)] text-foreground">{sum.text}</p>
            {sum.warnings.map(w => (
              <p key={w} className="m-0 mt-1 text-[length:var(--fs-sm)] text-[color:var(--warn)]">{w}</p>
            ))}
          </div>
        );
      })()}
      {sc.data && (
        <>
          <h3 className="mt-1 mb-0 font-semibold">{M('detectors', lang)}</h3>
          <DataTable<DetectorRow>
            storageKey="anomalies-ml-detectors-v2" columns={dcolsOf(lang)} rows={detectorRows(sc.data, signal)}
            state={{ kind: 'empty', message: M('noDetectors', lang) }} sort={{ initial: { id: 'state', dir: 'desc' } }}
            getRowKey={r => r.detector} ariaLabel={M('detectors', lang)}
            renderCell={(r, c) => {
              switch (c.id) {
                case 'detector': return mlDetectorLabel(r.detector, lang);
                case 'signal': return r.signal === 'log' || r.signal === 'trace' || r.signal === 'metric'
                  ? <SignalKindBadge kind={r.signal} /> : (signalName(r.signal, lang) ?? r.signal);
                case 'state': return r.enabled
                  ? (r.unmeasured ? <span className="text-[color:var(--warn)]" title={r.unmeasured}>{M(r.unmeasured.startsWith('kısmen') ? 'onPartly' : 'onUnmeasured', lang)}</span> : M('on', lang))
                  : <span className="text-muted-foreground">{M('off', lang)}</span>;
                case 'events': return r.events.toLocaleString(loc(lang));
                case 'hit': {
                  const v = detectorVerdictText(r, lang);
                  return v || <span className="text-muted-foreground" title={M('noVerdictTitle', lang)}>—</span>;
                }
                default: return null;
              }
            }} />
          <p className="m-0 text-[length:var(--fs-sm)] text-muted-foreground">
            {M('explain', lang)}
          </p>
          <details data-testid="ml-tech-details" className="rounded-md border border-border px-3 py-2">
            <summary className="cursor-pointer text-[length:var(--fs-sm)] font-semibold text-muted-foreground">{M('tech', lang)}</summary>
            <div className="mt-2 flex flex-col gap-3">
              <div className="grid gap-2 md:grid-cols-4" data-testid="ml-status-tiles">
                {statusTiles(sc.data, signal, Math.floor(Date.now() / 1000), lang).map(t => (
                  <div key={t.k} className="rounded-md border border-border bg-card px-3 py-2">
                    <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{t.k}</div>
                    <div className={`text-lg font-semibold ${t.warn ? 'text-[color:var(--warn)]' : ''}`}>{t.v}</div>
                    <div className="text-xs text-muted-foreground">{t.s}</div>
                  </div>
                ))}
              </div>
              {(sc.data.tick?.kinds || sc.data.baseline) && (
                <div className="flex flex-col gap-0.5 text-xs text-muted-foreground" data-testid="ml-kind-ticks">
                  {kindTickLines(sc.data.tick, signal, lang).map(l => (
                    <div key={l.kind} className={l.warn ? 'text-[color:var(--warn)]' : ''}>{l.text}</div>
                  ))}
                  {(() => {
                    const b = baselineStatusLine(sc.data.baseline, lang);
                    return b && <div data-testid="ml-baseline-line" className={b.warn ? 'text-[color:var(--warn)]' : ''}>{b.text}</div>;
                  })()}
                </div>
              )}
              <DataTable<DetectorRow>
                storageKey="anomalies-ml-detectors-tech" columns={dtechOf(lang)} rows={detectorRows(sc.data, signal)}
                state={{ kind: 'empty', message: M('noDetectors', lang) }} sort={{ initial: { id: 'detector', dir: 'asc' } }}
                getRowKey={r => r.detector} ariaLabel={M('techAria', lang)}
                renderCell={(r, c) => {
                  switch (c.id) {
                    case 'detector': return mlDetectorLabel(r.detector, lang);
                    case 'tick': return r.tick ? M('sec', lang, { s: (r.tick.sourceMs / 1000).toFixed(1) }) : '—';
                    case 'services': return r.tick ? r.tick.services : '—';
                    case 'candidates': return r.tick ? r.tick.candidates : '—';
                    case 'written': return r.tick ? r.tick.written : '—';
                    default: return null;
                  }
                }} />
              <MLPublishGatesSection tick={sc.data.tick} />
              <MLSvcShadowCompareSection />
              <EngineCompareSection />
              {isAdmin && <Suspense fallback={null}><ImpactReplaySection /></Suspense>}
              <ProblemGraphShadowSection />
            </div>
          </details>
        </>
      )}
    </div>
  );
}
