// tracePodPanelModel.ts — v0.10.968 — Trace › Metrics seçili pod paneli ve pod
// odak görünümünün SAF yardımcıları (tablo testli: tracePodPanel.test.ts).
//
// v0.10.968 — Dosya adı BİLEREK `tracePodPanel.ts` DEĞİL: macOS'un büyük/küçük
// harf duyarsız dosya sisteminde `./TracePodPanel` içe aktarımı `.ts`'i
// `.tsx`'ten önce dener ve bileşen yerine bu modülü bulur (tsc TS1149, Vite
// yanlış modülü yükler). Bileşen `TracePodPanel.tsx`, saf çekirdek burada.
//
// v0.10.968 — Operatör onayı 2026-09-27 ("3 onay", mockup PodDetail.dc.html +
// Main.dc.html sağ panel). Buradaki her karar DOM'suz: kardeş oranı (medyan),
// grafik öğeleri (pod çizgisi / trace bandı / limit çizgisi), "Şu an"
// satırları ve metrik durum mesajları. Bileşenler (TracePodPanel /
// TracePodCharts) yalnız bunları çizer.
//
// v0.10.2207 (operatör: panel "kalabalık") — karşılaştırma çipleri, kardeş
// çizgileri, odak görünümü (span zaman çizelgesi, huni cümlesi, işaretli
// gezinme) ve "en büyük öz süre" rozeti kalktı; grafik yalnız seçili pod.
//
// v0.10.968 — Kural: bir HATA hiçbir zaman "boş sonuç" gibi yazılmaz
// (v0.10.962 hata 3). stateMessage('error') "okunamadı" der, hata tonunu
// taşır ve yeniden deneme eylemi verir; "örnek yok" yalnız no_samples'ta.
import type { SpanMetricSeries } from '@/lib/types';
import type { ChartThreshold, ChartTimeRegion } from '@/lib/chart/overlays';
import { seriesColorsFor, type ChartTheme } from '@/lib/chartFmt';
import {
  LEVEL_ERR, SIBLING_RATIO_WARN,
  type MetricLevel, type PodMetricSeries, type PodMetricState, type TraceMetricsModel, type TracePodInfo,
} from './traceMetricsModel';
import { fmtBytesTr, fmtClockNs, fmtClockSec, fmtCores, fmtCoresShort, fmtDurNs, fmtPct, fmtRatio } from './traceMetricsFmt';
import { trPossessive } from './trSuffix';
import { t, tf, type Lang } from '@/lib/i18n';

// v0.10.2316 — metinler katalogda (tpp.*); her metin üreten yardımcı `lang`
// alır (varsayılan 'tr' — TR çıktısı birebir aynı). Bileşenler useLang()
// değerini geçirir.

// ── Ortak ─────────────────────────────────────────────────────────────────

const isNum = (v: number | null | undefined): v is number => typeof v === 'number' && Number.isFinite(v);

/** v0.10.968 — ondalık virgüllü, sondaki sıfırları atan kısa sayı ("1", "0,5",
 *  "1,25"): zaman çizelgesi eksen işaretleri ("0,5 sn", "1 sn"). */
export function trNum(v: number, maxDec = 2): string {
  const f = 10 ** maxDec;
  const r = Math.round(v * f) / f;
  return String(r).replace('.', ',');
}

/** v0.10.968 — çekirdek sayısı birimsiz ("1", "0,5"): "1 / 0,5 çekirdek" çifti
 *  için; biçim traceMetricsFmt.fmtCoresShort ile aynı kaynaktan. */
export function coreNum(v: number, lang: Lang = 'tr'): string {
  return fmtCoresShort(v, lang).replace(/ c$/, '');
}

/** v0.10.968 — "limitin %97'si" / "isteğin %124'ü" (Türkçe iyelik eki sayının
 *  okunuşundan, trSuffix). */
export function pctOf(ratio: number, lang: Lang = 'tr'): string {
  return lang === 'tr' ? `${fmtPct(ratio)}${trPossessive(Math.round(ratio * 100))}` : fmtPct(ratio, lang);
}

/** v0.10.968 — seçili pod'un servisinin BU TRACE'teki pod'ları, model sırasıyla. */
export function servicePods(model: TraceMetricsModel, service: string): TracePodInfo[] {
  return model.groups.find(g => g.service === service)?.pods
    ?? model.pods.filter(p => p.service === service);
}

const okData = (s: PodMetricState): PodMetricSeries | null => (s.kind === 'ok' ? s.data : null);

// ── Kardeşlere göre (medyan oranı) ────────────────────────────────────────

/** v0.10.968 — medyan (tek: orta; çift: iki ortanın ortalaması); boşsa null. */
export function median(xs: readonly number[]): number | null {
  const v = xs.filter(isNum).slice().sort((a, b) => a - b);
  if (v.length === 0) return null;
  const m = Math.floor(v.length / 2);
  return v.length % 2 ? v[m] : (v[m - 1] + v[m]) / 2;
}

export interface SiblingRatio { ratio: number; n: number; warn: boolean }

/** v0.10.968 — seçili pod'un trace-anı değeri ÷ kardeşlerin (aynı servisin bu
 *  trace'teki DİĞER pod'ları, metriği olanlar) medyanı. Kardeş yok, seçilinin
 *  değeri yok ya da medyan 0 → null (sonsuz kat yazılmaz). ≥1,5 uyarı. */
export function siblingRatio(value: number | null | undefined, siblings: readonly (number | null | undefined)[]): SiblingRatio | null {
  const vals = siblings.filter(isNum);
  if (!isNum(value) || vals.length === 0) return null;
  const m = median(vals);
  if (m == null || m <= 0) return null;
  const ratio = value / m;
  return { ratio, n: vals.length, warn: ratio >= SIBLING_RATIO_WARN };
}

export interface SiblingStats {
  total: number;       // servisin bu trace'teki diğer pod sayısı
  withMetrics: number; // bunlardan metriği (ok) olan
  failed: number;      // v0.10.968 — okunamayan (error) kardeş sayısı; boş sayılmaz
  cpu: SiblingRatio | null;
  mem: SiblingRatio | null;
}

/** v0.10.968 — "Kardeşlere göre" satırının verisi. Karşılaştırma kümesinin
 *  yalnız SEÇİLİ pod'u dışarıda: diğer karşılaştırılan pod'lar da kardeştir. */
export function siblingStats(model: TraceMetricsModel, selected: TracePodInfo, metrics: (pod: string) => PodMetricState): SiblingStats | null {
  const self = okData(metrics(selected.pod));
  if (!self) return null;
  const sibs = servicePods(model, selected.service).filter(p => p.pod !== selected.pod);
  const data = sibs.map(p => okData(metrics(p.pod))).filter((d): d is PodMetricSeries => d != null);
  return {
    total: sibs.length,
    withMetrics: data.length,
    failed: sibs.filter(p => metrics(p.pod).kind === 'error').length,
    cpu: siblingRatio(self.cpuAt, data.map(d => d.cpuAt)),
    mem: siblingRatio(self.memAt, data.map(d => d.memAt)),
  };
}

export interface CopySeg { t: string; tone?: 'warn' | 'faint' | 'err'; strong?: boolean }

/** v0.10.968 — "Kardeşlere göre" metni, parça parça (≥1,5 kat uyarı + 600).
 *  Servis adı ek almaz (§7); kardeş kapsamı hep "bu trace'teki diğer N pod". */
export function siblingCopy(st: SiblingStats, lang: Lang = 'tr'): CopySeg[] {
  if (st.total === 0) return [{ t: t('tpp.sib.alone', lang), tone: 'faint' }];
  if (!st.cpu && !st.mem) {
    // v0.10.968 — okunamayan kardeşler "metriği yok" DEĞİL: hata boş sonuç
    // gibi yazılmaz (v0.10.962 hata 3), hata tonunda söylenir.
    if (st.withMetrics === 0 && st.failed > 0) {
      return [{ t: tf('tpp.sib.failed', lang, { n: st.failed }), tone: 'err', strong: true }];
    }
    return [{ t: st.withMetrics === 0
      ? tf('tpp.sib.noMetrics', lang, { n: st.total })
      : t('tpp.sib.noValue', lang), tone: 'faint' }];
  }
  const n = (st.cpu ?? st.mem)!.n;
  const seg = (r: SiblingRatio): CopySeg => ({ t: fmtRatio(r.ratio, lang), tone: r.warn ? 'warn' : undefined, strong: r.warn });
  const out: CopySeg[] = [];
  if (st.cpu) out.push({ t: tf('tpp.sib.cpu', lang, { n }) }, seg(st.cpu));
  if (st.mem) {
    out.push({ t: st.cpu ? t('tpp.sib.memAnd', lang) : tf('tpp.sib.mem', lang, { n }) }, seg(st.mem));
  }
  return out;
}

// ── Trace anında ──────────────────────────────────────────────────────────

export const THROTTLE_NOTE = 'Limite dayandı: CPU kısıtlaması (throttling) olası. Kısıtlama oranı bu sürümde ölçülmüyor.';

export interface MomentRow {
  k: string;           // 'CPU' | 'Bellek' (dile göre)
  main: string;        // değer + limit oranı (seviye renginde, 600)
  level: MetricLevel;
  rest: string;        // soluk ek ("limit 1 çekirdek (şu an)")
  title?: string;
  cap?: string;        // alt satır notu (CPU ≥%90 kısıtlama)
}

/** v0.10.968 — "Trace anında" CPU/Bellek satırı. Limit bilinmiyorsa renk YOK
 *  (renk yalnız limite göre sapmada). Envanter yok/okunamadı → dürüst neden. */
export function momentRow(kind: 'cpu' | 'mem', d: PodMetricSeries, lang: Lang = 'tr'): MomentRow {
  const k = kind === 'cpu' ? 'CPU' : t('tpp.mem', lang);
  const v = kind === 'cpu' ? d.cpuAt : d.memAt;
  const lim = kind === 'cpu' ? d.cpuLimit : d.memLimit;
  const req = kind === 'cpu' ? d.cpuRequest : d.memRequest;
  const val = (x: number) => (kind === 'cpu' ? fmtCores(x, lang) : fmtBytesTr(x, lang));
  const amount = val;
  if (!isNum(v)) return { k, main: '—', level: 'none', rest: t('tpp.m.noSample', lang) };
  if (isNum(lim) && lim > 0) {
    const r = v / lim;
    return {
      k, main: tf('tpp.m.ofLimit', lang, { v: val(v), p: pctOf(r, lang) }),
      level: kind === 'cpu' ? d.cpuLevel : d.memLevel,
      rest: tf('tpp.m.limitNow', lang, { a: amount(lim) }),
      cap: kind === 'cpu' && r >= LEVEL_ERR ? t('tpp.throttle', lang) : undefined,
    };
  }
  if (d.inventory === 'absent') return { k, main: val(v), level: 'none', rest: t('tpp.m.absent', lang) };
  if (d.inventory === 'unknown') return { k, main: val(v), level: 'none', rest: t('tpp.m.unknown', lang) };
  if (d.instantPartial) {
    // v0.10.968 — kube-state-metrics kısmen düştü: eksik limit "tanımsız" değil "bilinmiyor".
    if (isNum(req) && req > 0) {
      const r = v / req;
      return {
        k, main: tf('tpp.m.limitUnknown', lang, { v: val(v) }), level: 'none',
        rest: tf('tpp.m.partialReq', lang, { p: pctOf(r, lang) }),
        title: tf('tpp.m.reqTitle', lang, { p: pctOf(r, lang), a: amount(req) }),
      };
    }
    return { k, main: tf('tpp.m.limitUnknown', lang, { v: val(v) }), level: 'none', rest: t('tpp.m.partial', lang) };
  }
  if (isNum(req) && req > 0) {
    const r = v / req;
    return {
      k, main: tf('tpp.m.limitUnset', lang, { v: val(v) }), level: 'none',
      rest: tf('tpp.m.ofReq', lang, { p: pctOf(r, lang) }),
      title: tf('tpp.m.reqTitle', lang, { p: pctOf(r, lang), a: amount(req) }),
    };
  }
  return { k, main: tf('tpp.m.limitUnset', lang, { v: val(v) }), level: 'none', rest: t('tpp.m.reqUnset', lang) };
}

/** v0.10.968 — seçili pod'un çizgi rengi (etiket → renk; seriesColorsFor,
 *  tema-farkında: çağıran render'da useThemeTick altında çağırır). Bellek /
 *  CPU ve JVM çizgileri BU haritadan okur (tek pod = tek renk). */
export function podColors(pod: string, theme?: ChartTheme): Map<string, string> {
  return seriesColorsFor([pod], undefined, theme);
}

// ── Grafik öğeleri ────────────────────────────────────────────────────────

export interface ChartItem { name: string; role: 'data'; series: SpanMetricSeries[]; color?: string }
export interface ChartPod { pod: string; label: string; data: PodMetricSeries }

export interface ChartBuild {
  kind: 'mem' | 'cpu';
  items: ChartItem[];
  regions: ChartTimeRegion[];
  thresholds: ChartThreshold[];
  limit: number | null;
  noLimit: boolean;         // CPU: "CPU limiti tanımsız: limit çizgisi yok."
}

/** v0.10.968 — bir pod serisi → grafik serisi. Boşluk NaN (dataFrame onu null
 *  yapar): ölçüm yoksa çizgi köprülenmez, sıfır da çizilmez. */
export function podSeries(label: string, d: PodMetricSeries, kind: 'mem' | 'cpu'): SpanMetricSeries {
  const vals = kind === 'cpu' ? d.cpu : d.mem;
  return {
    groupKey: [label],
    points: vals.map((v, i) => ({ time: (d.startSec + i * d.stepSec) * 1e9, value: isNum(v) ? v : NaN })),
  };
}

/** v0.10.968 — seçili pod paneli grafiği (v0.10.2207: yalnız seçili pod):
 *   • pod çizgisi `role:'data'`;
 *   • trace bandı: en az bir adım geniş (`toSec = max(son, baş + adım)`),
 *     gerçek bitiş `endSec`te; renk var(--accent2), etiket "trace";
 *   • limit çizgisi yalnız limit BİLİNİRKEN (pod'un şu anki limiti). */
export function buildChartItems(kind: 'mem' | 'cpu', inp: {
  pod: ChartPod | null;
  traceStartNs: number;
  traceEndNs: number;
  stepSec: number;
  /** podColors (etiket → renk); verilmezse renk addan (CorePanel). */
  colors?: ReadonlyMap<string, string>;
  /** v0.10.2316 — limit çizgisi etiketinin dili (varsayılan 'tr'). */
  lang?: Lang;
}): ChartBuild {
  const sel = inp.pod?.data;
  const rawLimit = sel ? (kind === 'cpu' ? sel.cpuLimit : sel.memLimit) : undefined;
  const limit = isNum(rawLimit) && rawLimit > 0 ? rawLimit : null;
  const items: ChartItem[] = inp.pod
    ? [{ name: inp.pod.label, role: 'data', series: [podSeries(inp.pod.label, inp.pod.data, kind)], color: inp.colors?.get(inp.pod.label) }]
    : [];
  const fromSec = inp.traceStartNs / 1e9;
  const endSec = inp.traceEndNs / 1e9;
  const step = inp.stepSec > 0 ? inp.stepSec : 0;
  return {
    kind,
    items,
    regions: [{ fromSec, toSec: Math.max(endSec, fromSec + step), endSec, color: 'var(--accent2)', label: 'trace' }],
    thresholds: limit != null ? [{ value: limit, label: t('tpp.limitNowLabel', inp.lang ?? 'tr'), color: 'var(--warn)' }] : [],
    limit,
    noLimit: kind === 'cpu' && limit == null,
  };
}

/** v0.10.968 — seçili pod'un grafik girdisi (metriği ok değilse null). Etiket
 *  tam pod adı (v0.10.1644). */
export function chartPod(selected: TracePodInfo, metrics: (pod: string) => PodMetricState): ChartPod | null {
  const d = okData(metrics(selected.pod));
  return d ? { pod: selected.pod, label: selected.pod, data: d } : null;
}

/** v0.10.968 — grafik altı açıklama (gerçek adımla). */
export function chartCaption(o: { durNs: number; stepSec: number | null; cpuNoLimit: boolean }, lang: Lang = 'tr'): string {
  const step = o.stepSec != null ? tf('aed.unit.s', lang, { n: o.stepSec }) : t('tpp.cap.one', lang);
  const parts = [tf('tpp.cap.band', lang, { d: fmtDurNs(o.durNs, lang), step })];
  if (o.cpuNoLimit) parts.push(t('tpp.cap.noLimit', lang));
  parts.push(t('tpp.cap.rate', lang));
  parts.push(t('tpp.cap.corr', lang));
  return parts.join(' ');
}

// ── Şu an (trace anı değil) ──────────────────────────────────────────────

export interface NowRow { k: string; v: string; tone?: 'warn' | 'faint'; badge?: { t: string; tone: 'danger' | 'neutral' } }

/** v0.10.968 — trace'e göre olay zamanı: "trace'ten 12 dk sonra" / "… önce";
 *  saat/gün ölçeğine taşar. */
export function relToTrace(eventSec: number, traceStartNs: number, lang: Lang = 'tr'): string {
  const diff = eventSec - traceStartNs / 1e9;
  const a = Math.abs(diff);
  const dir = t(diff >= 0 ? 'tpp.rel.after' : 'tpp.rel.before', lang);
  if (a < 60) return t('tpp.rel.same', lang);
  if (a < 120 * 60) return tf('tpp.rel.m', lang, { n: Math.round(a / 60), dir });
  if (a < 48 * 3600) return tf('tpp.rel.h', lang, { n: Math.round(a / 3600), dir });
  return tf('tpp.rel.d', lang, { n: Math.round(a / 86400), dir });
}

/** v0.10.968 — "N dk önce" (Date.now() ÇAĞIRAN tarafında, useMemo içinde). */
// v0.10.2316 — metin ago.* katalog anahtarlarından (TR değerleri aynı); eşikler bu dosyanınki.
export function agoText(nowMs: number, thenMs: number, lang: Lang = 'tr'): string {
  const a = Math.max(0, nowMs - thenMs) / 1000;
  if (a < 60) return t('ago.now', lang);
  if (a < 120 * 60) return tf('ago.m', lang, { n: Math.round(a / 60) });
  if (a < 48 * 3600) return tf('ago.h', lang, { n: Math.round(a / 3600) });
  return tf('ago.d', lang, { n: Math.round(a / 86400) });
}

const pad2 = (n: number) => String(n).padStart(2, '0');
/** v0.10.968 — yerel saat "HH:MM". */
export function hhmm(ms: number): string {
  const d = new Date(ms);
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}
/** v0.10.968 — "Şu an" satırları (kube-state-metrics, ŞİMDİKİ değer — trace
 *  anı değil): restart >0 uyarı; OOMKilled tehlike rozeti (diğer nedenler
 *  nötr) + zaman ya da "zaman bilinmiyor"; limit/istek "tanımsız / 0,5". */
export function nowSection(d: PodMetricSeries, traceStartNs: number, lang: Lang = 'tr'): { rows: NowRow[]; note: string | null } {
  const n = d.now;
  const note = d.inventory === 'absent' ? t('tpp.now.absent', lang)
    : d.inventory === 'unknown' ? t('tpp.now.unknown', lang)
    : d.instantPartial ? t('tpp.now.partial', lang)
    : null;
  const rows: NowRow[] = [];
  const phase = t('podpage.col.phase', lang);
  rows.push(n.phase ? { k: phase, v: n.phase } : { k: phase, v: '—', tone: 'faint' });
  rows.push(isNum(n.restarts)
    ? { k: 'Restart', v: String(n.restarts), tone: n.restarts > 0 ? 'warn' : undefined }
    : { k: 'Restart', v: '—', tone: 'faint' });
  if (n.lastTermReason) {
    const when = isNum(n.lastTermAtSec) ? ` ${fmtClockSec(n.lastTermAtSec)} · ${relToTrace(n.lastTermAtSec, traceStartNs, lang)}` : t('tpp.now.whenUnknown', lang);
    rows.push({ k: t('podpage.ctr.lastTerm', lang), v: when, badge: { t: n.lastTermReason, tone: n.lastTermReason === 'OOMKilled' ? 'danger' : 'neutral' } });
  } else {
    rows.push({ k: t('podpage.ctr.lastTerm', lang), v: '—', tone: 'faint' });
  }
  // v0.10.968 — sorgu kısmen düştüyse eksik alan "tanımsız" (okundu, yok) değil "bilinmiyor".
  const missing = t(d.instantPartial ? 'aed.unknown' : 'tpp.now.unset', lang);
  const pair = (lim: number | undefined, req: number | undefined, f: (x: number) => string) =>
    `${isNum(lim) ? f(lim) : missing} / ${isNum(req) ? f(req) : missing}`;
  rows.push({ k: t('tpp.now.cpuLimReq', lang), v: tf('tpp.cores', lang, { n: pair(d.cpuLimit, d.cpuRequest, x => coreNum(x, lang)) }) });
  rows.push({ k: t('tpp.now.memLimReq', lang), v: pair(d.memLimit, d.memRequest, x => fmtBytesTr(x, lang)) });
  return { rows, note };
}

/** v0.10.968 — metriği ok OLMAYAN pod'un "Şu an" notu (Thanos cevabı yok →
 *  anlık bilgi de yok; neden söylenir). */
export function nowNote(s: PodMetricState, lang: Lang = 'tr'): { t: string; err?: boolean } | null {
  switch (s.kind) {
    case 'ok': case 'loading': return null;
    case 'error': return { t: t('tpp.nowNote.error', lang), err: true };
    default: return { t: t(`tpp.nowNote.${s.kind}`, lang) };
  }
}

// ── Metrik durum mesajı ──────────────────────────────────────────────────

export const AMBIGUOUS_COPY = "Pod adı birden çok namespace'te — span'de k8s.namespace.name yok";

export interface StateMsg {
  title: string;
  sub?: string;
  tone: 'err' | 'none';
  action?: 'retry' | 'load';
  settings?: boolean;   // "Ayarlar › Remote Cluster ↗" bağlantısı
  loading?: boolean;
}

/** v0.10.968 — hata nedeni: /zaman aşımı/ → "10 sn zaman aşımı"; değilse kırpılmış
 *  ileti (≤80). Sondaki nokta atılır ("<neden>. Diğer…" çift nokta olmasın). */
export function errorReason(s: { message: string; timeout: boolean }, lang: Lang = 'tr'): string {
  if (s.timeout || /zaman aşımı/.test(s.message)) return t('tpp.err.timeout', lang);
  const m = s.message.trim().replace(/\.+$/, '');
  if (!m) return t('hm.env.unknownErr', lang);
  return m.length > 80 ? `${m.slice(0, 79)}…` : m;
}

/** v0.10.968 — seçili pod'un metrik durumu → panel mesajı; ok → null.
 *  error ASLA boş sonuç gibi yazılmaz (v0.10.962 hata 3). */
export function stateMessage(s: PodMetricState, lang: Lang = 'tr'): StateMsg | null {
  const intact = t('tpp.st.intact', lang);
  switch (s.kind) {
    case 'ok': return null;
    case 'loading': return { title: t('dataTable.state.loading', lang), tone: 'none', loading: true };
    case 'idle': return { title: t('tpp.st.idle', lang), tone: 'none', action: 'load' };
    case 'off': return {
      title: t('tpp.st.off', lang),
      sub: t('tpp.st.offSub', lang),
      tone: 'none', settings: true,
    };
    case 'unmapped': return s.reason === 'no_cluster_value'
      ? {
          title: t('tpp.st.noCluster', lang),
          sub: intact,
          tone: 'none',
        }
      : {
          title: tf('tpp.st.unmapped', lang, { c: s.clusterValue }),
          sub: intact,
          tone: 'none', settings: true,
        };
    case 'no_samples': return {
      title: t('tpp.st.noSamples', lang),
      sub: t('tpp.st.noSamplesSub', lang),
      tone: 'none',
    };
    case 'ambiguous': return {
      title: `${t('tpp.ambiguous', lang)}.`,
      sub: intact,
      tone: 'none',
    };
    case 'error': return {
      title: t('dataTable.state.error', lang),
      sub: tf('tpp.st.errorSub', lang, { r: errorReason(s, lang) }),
      tone: 'err', action: 'retry',
    };
  }
}

// ── Başlık, rozetler, odak gezinmesi ────────────────────────────────────

/** v0.10.968 — metrik durumundan bilinen cluster adı (ok/no_samples/ambiguous);
 *  yoksa ''. "Pod sayfasında aç" bağlantısı bu boşken gizlenir. */
export function clusterNameOf(s: PodMetricState): string {
  if (s.kind === 'ok') return s.data.cluster.name;
  if (s.kind === 'no_samples' || s.kind === 'ambiguous') return s.cluster.name;
  return '';
}

/** v0.10.968 — başlık alt satırı: "<svc> · ns <ns> · <cluster adı ya da değer> · <runtime>". */
export function subLine(p: TracePodInfo, s: PodMetricState): string[] {
  const ns = (s.kind === 'ok' ? s.data.namespace : '') || p.namespace;
  const cl = clusterNameOf(s) || p.clusterValue;
  return [p.service, ns ? `ns ${ns}` : '', cl, p.runtime].filter(Boolean);
}

export interface PodBadge { t: string; tone: 'danger' | 'neutral' }

/** v0.10.968 — rozetler: kök hata (tehlike) · giriş · N hata (tehlike) ·
 *  kritik yol %X · Y (nötr, ≥%3). v0.10.2207 — "en büyük öz süre" rozeti
 *  kalktı (alt özet satırında bağlantı olarak duruyor). */
export function podBadges(p: TracePodInfo, critShareMin: number, lang: Lang = 'tr'): PodBadge[] {
  const out: PodBadge[] = [];
  if (p.rootCause) out.push({ t: t('tpp.b.root', lang), tone: 'danger' });
  if (p.entry) out.push({ t: t('tpp.b.entry', lang), tone: 'neutral' });
  if (p.errors > 0) out.push({ t: tf('tpp.b.errors', lang, { n: p.errors }), tone: 'danger' });
  if (p.critShare >= critShareMin) out.push({ t: tf('tpp.b.crit', lang, { p: fmtPct(p.critShare, lang), d: fmtDurNs(p.critNs, lang) }), tone: 'neutral' });
  return out;
}

// ── v0.10.1096 — grafik başlığı + tek satırlık özet ──────────────────────
// Operatör (prod, satır altı ayrıntı): "Çok fazla yazı var; sadece metrik
// yatayda inline gözükse olacak. Diğer yazılar aşağı olabilir." Grafikler
// panelin en üstüne yan yana çıktı; her birinin TEK satırlık başlığı trace
// anı değeri + limit, altında TEK satır özet. Ayrıntılı metin (Bu trace'te /
// Trace anında / Şu an, açıklama) kapalı "Teknik ayrıntı"da — silinmedi.

export interface ChartHeadline {
  k: string;            // 'Bellek' | 'CPU' (dile göre)
  value: string;        // trace anı değeri ("9,03 GiB" / "0,053 çekirdek")
  limit: string | null; // "limit 16 GiB" / "limit 1,6" / "limit tanımsız" / "limit bilinmiyor"
  level: MetricLevel;   // renk YALNIZ limit bilinirken ve sapmada (momentRow kararı)
  title: string;        // ipucu: "Trace anında" satırının tam cümlesi (+ kısıtlama notu)
}

/** v0.10.1096 — grafik başlığı: "Bellek 9,03 GiB · limit 16 GiB" / "CPU 0,053
 *  çekirdek · limit 1,6". Limitin "şu an" olduğu ve kısıtlama notu ipucunda
 *  (momentRow ile tek kaynak). Örnek yoksa değer "—", limit yazılmaz. */
export function chartHeadline(kind: 'cpu' | 'mem', d: PodMetricSeries, lang: Lang = 'tr'): ChartHeadline {
  const row = momentRow(kind, d, lang);
  const v = kind === 'cpu' ? d.cpuAt : d.memAt;
  const lim = kind === 'cpu' ? d.cpuLimit : d.memLimit;
  const title = [tf('tpp.h.title', lang, { v: `${row.main}${row.rest}` }).replace(/\s+/g, ' ').trim(), row.title, row.cap].filter(Boolean).join(' — ');
  if (!isNum(v)) return { k: row.k, value: '—', limit: null, level: 'none', title };
  let limit: string;
  if (isNum(lim) && lim > 0) limit = `limit ${kind === 'cpu' ? coreNum(lim, lang) : fmtBytesTr(lim, lang)}`;
  else if (d.inventory !== 'present' || d.instantPartial) limit = t('tpp.h.limUnknown', lang);
  else limit = t('tpp.h.limUnset', lang);
  return { k: row.k, value: kind === 'cpu' ? fmtCores(v, lang) : fmtBytesTr(v, lang), limit, level: row.level, title };
}

export interface FactSeg {
  id: 'spans' | 'errors' | 'crit' | 'self' | 'phase' | 'restarts' | 'term';
  t: string;
  /** Bağlantının önündeki düz önek ("en büyük öz süre:"). */
  label?: string;
  tone?: 'err' | 'warn' | 'faint';
  /** Verilirse segment Trace sekmesinde bu span'i açan bağlantı. */
  spanId?: string;
  title?: string;
  /** Verilirse segment rozet (son sonlanma nedeni). */
  badge?: 'danger' | 'neutral';
}

/** v0.10.1096 — grafiklerin altındaki TEK satır: "9 span · 0 hata · kritik yol
 *  %86 · 17 ms · en büyük öz süre: <op> · 21 ms ↗ · Running · restart 0".
 *  Hata >0 ve ilk hata biliniyorsa "N hata ↗" ilk hatalı span'i açar; son
 *  sonlanma nedeni rozet (OOMKilled tehlike). `now` yalnız metrik ok iken
 *  (faz/restart "şu an"dır — ipucu söyler). */
export function podFacts(p: TracePodInfo, o: {
  selfKnown: boolean; selfName: string; now: PodMetricSeries['now'] | null; traceStartNs: number;
}, lang: Lang = 'tr'): FactSeg[] {
  const out: FactSeg[] = [{ id: 'spans', t: `${p.spans} span` }];
  const fe = p.firstError;
  if (p.errors > 0) {
    out.push(fe
      ? { id: 'errors', t: tf('tpp.f.errorsLink', lang, { n: p.errors }), tone: 'err', spanId: fe.spanId, title: tf('tpp.f.firstErr', lang, { name: fe.name, status: fe.status, at: fmtClockNs(fe.timeNs) }) }
      : { id: 'errors', t: tf('tpp.b.errors', lang, { n: p.errors }), tone: 'err' });
  } else {
    out.push({ id: 'errors', t: tf('tpp.b.errors', lang, { n: 0 }), tone: 'faint' });
  }
  out.push(p.critNs > 0
    ? { id: 'crit', t: tf('tpp.b.crit', lang, { p: fmtPct(p.critShare, lang), d: fmtDurNs(p.critNs, lang) }) }
    : { id: 'crit', t: t('tpp.f.notCrit', lang), tone: 'faint' });
  if (o.selfKnown && p.maxSelfSpanId) {
    out.push({ id: 'self', label: t('tpp.f.selfLabel', lang), t: `${o.selfName || p.maxSelfSpanId} · ${fmtDurNs(p.maxSelfNs, lang)} ↗`, spanId: p.maxSelfSpanId });
  }
  const n = o.now;
  if (n) {
    if (n.phase) out.push({ id: 'phase', t: n.phase, title: t('tpp.f.phaseTip', lang) });
    if (isNum(n.restarts)) out.push({ id: 'restarts', t: `restart ${n.restarts}`, tone: n.restarts > 0 ? 'warn' : undefined, title: t('tpp.f.restartTip', lang) });
    if (n.lastTermReason) {
      const when = isNum(n.lastTermAtSec) ? `${fmtClockSec(n.lastTermAtSec)} · ${relToTrace(n.lastTermAtSec, o.traceStartNs, lang)}` : t('tpp.f.whenUnknown', lang);
      out.push({ id: 'term', t: n.lastTermReason, badge: n.lastTermReason === 'OOMKilled' ? 'danger' : 'neutral', title: tf('tpp.f.lastTerm', lang, { r: n.lastTermReason, when }) });
    }
  }
  return out;
}
