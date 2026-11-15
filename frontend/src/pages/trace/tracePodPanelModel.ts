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
export function coreNum(v: number): string {
  return fmtCoresShort(v).replace(/ c$/, '');
}

/** v0.10.968 — "limitin %97'si" / "isteğin %124'ü" (Türkçe iyelik eki sayının
 *  okunuşundan, trSuffix). */
export function pctOf(ratio: number): string {
  return `${fmtPct(ratio)}${trPossessive(Math.round(ratio * 100))}`;
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
export function siblingCopy(st: SiblingStats): CopySeg[] {
  if (st.total === 0) return [{ t: "Bu trace'te servisin tek pod'u — kıyaslanacak kardeş yok", tone: 'faint' }];
  if (!st.cpu && !st.mem) {
    // v0.10.968 — okunamayan kardeşler "metriği yok" DEĞİL: hata boş sonuç
    // gibi yazılmaz (v0.10.962 hata 3), hata tonunda söylenir.
    if (st.withMetrics === 0 && st.failed > 0) {
      return [{ t: `Bu trace'teki diğer ${st.failed} pod'un metrikleri okunamadı — kıyaslanacak kardeş yok`, tone: 'err', strong: true }];
    }
    return [{ t: st.withMetrics === 0
      ? `Bu trace'teki diğer ${st.total} pod'un metriği yok — kıyaslanacak kardeş yok`
      : 'Trace anında kıyaslanacak değer yok', tone: 'faint' }];
  }
  const n = (st.cpu ?? st.mem)!.n;
  const seg = (r: SiblingRatio): CopySeg => ({ t: fmtRatio(r.ratio), tone: r.warn ? 'warn' : undefined, strong: r.warn });
  const out: CopySeg[] = [];
  if (st.cpu) out.push({ t: `CPU, bu trace'teki diğer ${n} pod'un medyanının ` }, seg(st.cpu));
  if (st.mem) {
    out.push({ t: st.cpu ? ' · Bellek ' : `Bellek, bu trace'teki diğer ${n} pod'un medyanının ` }, seg(st.mem));
  }
  return out;
}

// ── Trace anında ──────────────────────────────────────────────────────────

export const THROTTLE_NOTE = 'Limite dayandı: CPU kısıtlaması (throttling) olası. Kısıtlama oranı bu sürümde ölçülmüyor.';

export interface MomentRow {
  k: 'CPU' | 'Bellek';
  main: string;        // değer + limit oranı (seviye renginde, 600)
  level: MetricLevel;
  rest: string;        // soluk ek ("limit 1 çekirdek (şu an)")
  title?: string;
  cap?: string;        // alt satır notu (CPU ≥%90 kısıtlama)
}

/** v0.10.968 — "Trace anında" CPU/Bellek satırı. Limit bilinmiyorsa renk YOK
 *  (renk yalnız limite göre sapmada). Envanter yok/okunamadı → dürüst neden. */
export function momentRow(kind: 'cpu' | 'mem', d: PodMetricSeries): MomentRow {
  const k = kind === 'cpu' ? 'CPU' : 'Bellek';
  const v = kind === 'cpu' ? d.cpuAt : d.memAt;
  const lim = kind === 'cpu' ? d.cpuLimit : d.memLimit;
  const req = kind === 'cpu' ? d.cpuRequest : d.memRequest;
  const val = (x: number) => (kind === 'cpu' ? fmtCores(x) : fmtBytesTr(x));
  const amount = (x: number) => (kind === 'cpu' ? fmtCores(x) : fmtBytesTr(x));
  if (!isNum(v)) return { k, main: '—', level: 'none', rest: ' trace anında örnek yok' };
  if (isNum(lim) && lim > 0) {
    const r = v / lim;
    return {
      k, main: `${val(v)} · limitin ${pctOf(r)}`,
      level: kind === 'cpu' ? d.cpuLevel : d.memLevel,
      rest: ` limit ${amount(lim)} (şu an)`,
      cap: kind === 'cpu' && r >= LEVEL_ERR ? THROTTLE_NOTE : undefined,
    };
  }
  if (d.inventory === 'absent') return { k, main: val(v), level: 'none', rest: ' · Pod şu anki envanterde yok — limit bilinmiyor' };
  if (d.inventory === 'unknown') return { k, main: val(v), level: 'none', rest: ' · limit okunamadı (kube-state-metrics sorgusu başarısız)' };
  if (d.instantPartial) {
    // v0.10.968 — kube-state-metrics kısmen düştü: eksik limit "tanımsız" değil "bilinmiyor".
    if (isNum(req) && req > 0) {
      const r = v / req;
      return {
        k, main: `${val(v)} · limit bilinmiyor`, level: 'none',
        rest: ` · kube-state-metrics kısmen okunamadı · isteğin ${pctOf(r)}`,
        title: `İsteğin ${pctOf(r)} (istek ${amount(req)}, şu an)`,
      };
    }
    return { k, main: `${val(v)} · limit bilinmiyor`, level: 'none', rest: ' · kube-state-metrics kısmen okunamadı' };
  }
  if (isNum(req) && req > 0) {
    const r = v / req;
    return {
      k, main: `${val(v)} · limit tanımsız`, level: 'none',
      rest: ` isteğin ${pctOf(r)}`,
      title: `İsteğin ${pctOf(r)} (istek ${amount(req)}, şu an)`,
    };
  }
  return { k, main: `${val(v)} · limit tanımsız`, level: 'none', rest: ' · istek de tanımsız' };
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
    thresholds: limit != null ? [{ value: limit, label: 'limit (şu an)', color: 'var(--warn)' }] : [],
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
export function chartCaption(o: { durNs: number; stepSec: number | null; cpuNoLimit: boolean }): string {
  const step = o.stepSec != null ? `${o.stepSec} sn` : 'bir';
  const parts = [`Mavi bant: trace (${fmtDurNs(o.durNs)}; en az bir ${step} adım genişliğinde çizilir).`];
  if (o.cpuNoLimit) parts.push('CPU limiti tanımsız: limit çizgisi yok.');
  parts.push('CPU rate[5m] ile yumuşatılmıştır; saniyelik sıçramalar görünmez.');
  parts.push('Grafikler eşzamanlılığı gösterir, nedenselliği değil.');
  return parts.join(' ');
}

// ── Şu an (trace anı değil) ──────────────────────────────────────────────

export interface NowRow { k: string; v: string; tone?: 'warn' | 'faint'; badge?: { t: string; tone: 'danger' | 'neutral' } }

/** v0.10.968 — trace'e göre olay zamanı: "trace'ten 12 dk sonra" / "… önce";
 *  saat/gün ölçeğine taşar. */
export function relToTrace(eventSec: number, traceStartNs: number): string {
  const diff = eventSec - traceStartNs / 1e9;
  const a = Math.abs(diff);
  const dir = diff >= 0 ? 'sonra' : 'önce';
  if (a < 60) return "trace'le aynı dakikada";
  if (a < 120 * 60) return `trace'ten ${Math.round(a / 60)} dk ${dir}`;
  if (a < 48 * 3600) return `trace'ten ${Math.round(a / 3600)} sa ${dir}`;
  return `trace'ten ${Math.round(a / 86400)} gün ${dir}`;
}

/** v0.10.968 — "N dk önce" (Date.now() ÇAĞIRAN tarafında, useMemo içinde). */
export function agoText(nowMs: number, thenMs: number): string {
  const a = Math.max(0, nowMs - thenMs) / 1000;
  if (a < 60) return 'az önce';
  if (a < 120 * 60) return `${Math.round(a / 60)} dk önce`;
  if (a < 48 * 3600) return `${Math.round(a / 3600)} sa önce`;
  return `${Math.round(a / 86400)} gün önce`;
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
export function nowSection(d: PodMetricSeries, traceStartNs: number): { rows: NowRow[]; note: string | null } {
  const n = d.now;
  const note = d.inventory === 'absent' ? 'Pod şu anki envanterde yok: faz, restart ve limitler bilinmiyor.'
    : d.inventory === 'unknown' ? 'kube-state-metrics okunamadı: faz, restart ve limitler bilinmiyor.'
    : d.instantPartial ? 'kube-state-metrics kısmen okunamadı: eksik alanlar bilinmiyor.'
    : null;
  const rows: NowRow[] = [];
  rows.push(n.phase ? { k: 'Faz', v: n.phase } : { k: 'Faz', v: '—', tone: 'faint' });
  rows.push(isNum(n.restarts)
    ? { k: 'Restart', v: String(n.restarts), tone: n.restarts > 0 ? 'warn' : undefined }
    : { k: 'Restart', v: '—', tone: 'faint' });
  if (n.lastTermReason) {
    const when = isNum(n.lastTermAtSec) ? ` ${fmtClockSec(n.lastTermAtSec)} · ${relToTrace(n.lastTermAtSec, traceStartNs)}` : ' · zaman bilinmiyor';
    rows.push({ k: 'Son sonlanma', v: when, badge: { t: n.lastTermReason, tone: n.lastTermReason === 'OOMKilled' ? 'danger' : 'neutral' } });
  } else {
    rows.push({ k: 'Son sonlanma', v: '—', tone: 'faint' });
  }
  // v0.10.968 — sorgu kısmen düştüyse eksik alan "tanımsız" (okundu, yok) değil "bilinmiyor".
  const missing = d.instantPartial ? 'bilinmiyor' : 'tanımsız';
  const pair = (lim: number | undefined, req: number | undefined, f: (x: number) => string) =>
    `${isNum(lim) ? f(lim) : missing} / ${isNum(req) ? f(req) : missing}`;
  rows.push({ k: 'CPU limit / istek', v: `${pair(d.cpuLimit, d.cpuRequest, coreNum)} çekirdek` });
  rows.push({ k: 'Bellek limit / istek', v: pair(d.memLimit, d.memRequest, fmtBytesTr) });
  return { rows, note };
}

/** v0.10.968 — metriği ok OLMAYAN pod'un "Şu an" notu (Thanos cevabı yok →
 *  anlık bilgi de yok; neden söylenir). */
export function nowNote(s: PodMetricState): { t: string; err?: boolean } | null {
  switch (s.kind) {
    case 'ok': case 'loading': return null;
    case 'unmapped': return { t: 'Cluster eşlenmemiş: faz, restart ve limitler okunamıyor.' };
    case 'off': return { t: 'Thanos Remote Cluster tanımlı değil: faz, restart ve limitler okunamıyor.' };
    case 'error': return { t: 'Veri okunamadı: faz, restart ve limitler bilinmiyor.', err: true };
    case 'idle': return { t: 'Metrik yüklenmedi: faz, restart ve limitler bilinmiyor.' };
    case 'ambiguous': return { t: 'Namespace belirsiz: faz, restart ve limitler okunamıyor.' };
    case 'no_samples': return { t: 'Bu pencerede Thanos örneği yok: faz, restart ve limitler gösterilmiyor.' };
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
export function errorReason(s: { message: string; timeout: boolean }): string {
  if (s.timeout || /zaman aşımı/.test(s.message)) return '10 sn zaman aşımı';
  const m = s.message.trim().replace(/\.+$/, '');
  if (!m) return 'bilinmeyen hata';
  return m.length > 80 ? `${m.slice(0, 79)}…` : m;
}

/** v0.10.968 — seçili pod'un metrik durumu → panel mesajı; ok → null.
 *  error ASLA boş sonuç gibi yazılmaz (v0.10.962 hata 3). */
export function stateMessage(s: PodMetricState): StateMsg | null {
  switch (s.kind) {
    case 'ok': return null;
    case 'loading': return { title: 'Yükleniyor', tone: 'none', loading: true };
    case 'idle': return { title: 'Metrik henüz yüklenmedi.', tone: 'none', action: 'load' };
    case 'off': return {
      title: 'Thanos Remote Cluster tanımlı değil.',
      sub: "Pod CPU ve bellek metrikleri Thanos'tan okunur; bu trace'teki bilgiler (span, hata, kritik yol) tam.",
      tone: 'none', settings: true,
    };
    case 'unmapped': return s.reason === 'no_cluster_value'
      ? {
          title: "Bu pod'un span'lerinde cluster özniteliği yok — metrik gösterilemiyor.",
          sub: "Bu trace'teki bilgiler (span, hata, kritik yol) tam; yalnız Thanos grafikleri yok.",
          tone: 'none',
        }
      : {
          title: `Bu pod'un cluster'ı (${s.clusterValue}) bir Remote Cluster kaydına eşlenmemiş — metrik gösterilemiyor.`,
          sub: "Bu trace'teki bilgiler (span, hata, kritik yol) tam; yalnız Thanos grafikleri yok.",
          tone: 'none', settings: true,
        };
    case 'no_samples': return {
      title: 'Bu pencerede Thanos örneği yok.',
      sub: 'Cluster eşli ve sorgu başarılı, ama bu pod için CPU / bellek serisi dönmedi.',
      tone: 'none',
    };
    case 'ambiguous': return {
      title: `${AMBIGUOUS_COPY}.`,
      sub: "Bu trace'teki bilgiler (span, hata, kritik yol) tam; yalnız Thanos grafikleri yok.",
      tone: 'none',
    };
    case 'error': return {
      title: 'Veri okunamadı — bu bir hata, boş sonuç değil.',
      sub: `${errorReason(s)}. Diğer cluster'ların metrikleri etkilenmedi.`,
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
export function podBadges(p: TracePodInfo, critShareMin: number): PodBadge[] {
  const out: PodBadge[] = [];
  if (p.rootCause) out.push({ t: 'kök hata', tone: 'danger' });
  if (p.entry) out.push({ t: 'giriş', tone: 'neutral' });
  if (p.errors > 0) out.push({ t: `${p.errors} hata`, tone: 'danger' });
  if (p.critShare >= critShareMin) out.push({ t: `kritik yol ${fmtPct(p.critShare)} · ${fmtDurNs(p.critNs)}`, tone: 'neutral' });
  return out;
}

// ── v0.10.1096 — grafik başlığı + tek satırlık özet ──────────────────────
// Operatör (prod, satır altı ayrıntı): "Çok fazla yazı var; sadece metrik
// yatayda inline gözükse olacak. Diğer yazılar aşağı olabilir." Grafikler
// panelin en üstüne yan yana çıktı; her birinin TEK satırlık başlığı trace
// anı değeri + limit, altında TEK satır özet. Ayrıntılı metin (Bu trace'te /
// Trace anında / Şu an, açıklama) kapalı "Teknik ayrıntı"da — silinmedi.

export interface ChartHeadline {
  k: 'Bellek' | 'CPU';
  value: string;        // trace anı değeri ("9,03 GiB" / "0,053 çekirdek")
  limit: string | null; // "limit 16 GiB" / "limit 1,6" / "limit tanımsız" / "limit bilinmiyor"
  level: MetricLevel;   // renk YALNIZ limit bilinirken ve sapmada (momentRow kararı)
  title: string;        // ipucu: "Trace anında" satırının tam cümlesi (+ kısıtlama notu)
}

/** v0.10.1096 — grafik başlığı: "Bellek 9,03 GiB · limit 16 GiB" / "CPU 0,053
 *  çekirdek · limit 1,6". Limitin "şu an" olduğu ve kısıtlama notu ipucunda
 *  (momentRow ile tek kaynak). Örnek yoksa değer "—", limit yazılmaz. */
export function chartHeadline(kind: 'cpu' | 'mem', d: PodMetricSeries): ChartHeadline {
  const row = momentRow(kind, d);
  const v = kind === 'cpu' ? d.cpuAt : d.memAt;
  const lim = kind === 'cpu' ? d.cpuLimit : d.memLimit;
  const title = [`Trace anında: ${row.main}${row.rest}`.replace(/\s+/g, ' ').trim(), row.title, row.cap].filter(Boolean).join(' — ');
  if (!isNum(v)) return { k: row.k, value: '—', limit: null, level: 'none', title };
  let limit: string;
  if (isNum(lim) && lim > 0) limit = `limit ${kind === 'cpu' ? coreNum(lim) : fmtBytesTr(lim)}`;
  else if (d.inventory !== 'present' || d.instantPartial) limit = 'limit bilinmiyor';
  else limit = 'limit tanımsız';
  return { k: row.k, value: kind === 'cpu' ? fmtCores(v) : fmtBytesTr(v), limit, level: row.level, title };
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
}): FactSeg[] {
  const out: FactSeg[] = [{ id: 'spans', t: `${p.spans} span` }];
  const fe = p.firstError;
  if (p.errors > 0) {
    out.push(fe
      ? { id: 'errors', t: `${p.errors} hata ↗`, tone: 'err', spanId: fe.spanId, title: `İlk hatalı span'i aç: ${fe.name} · ${fe.status} · ${fmtClockNs(fe.timeNs)}` }
      : { id: 'errors', t: `${p.errors} hata`, tone: 'err' });
  } else {
    out.push({ id: 'errors', t: '0 hata', tone: 'faint' });
  }
  out.push(p.critNs > 0
    ? { id: 'crit', t: `kritik yol ${fmtPct(p.critShare)} · ${fmtDurNs(p.critNs)}` }
    : { id: 'crit', t: 'kritik yolda değil', tone: 'faint' });
  if (o.selfKnown && p.maxSelfSpanId) {
    out.push({ id: 'self', label: 'en büyük öz süre:', t: `${o.selfName || p.maxSelfSpanId} · ${fmtDurNs(p.maxSelfNs)} ↗`, spanId: p.maxSelfSpanId });
  }
  const n = o.now;
  if (n) {
    if (n.phase) out.push({ id: 'phase', t: n.phase, title: 'Faz — şu an (trace anı değil)' });
    if (isNum(n.restarts)) out.push({ id: 'restarts', t: `restart ${n.restarts}`, tone: n.restarts > 0 ? 'warn' : undefined, title: 'Restart — şu an (trace anı değil)' });
    if (n.lastTermReason) {
      const when = isNum(n.lastTermAtSec) ? `${fmtClockSec(n.lastTermAtSec)} · ${relToTrace(n.lastTermAtSec, o.traceStartNs)}` : 'zaman bilinmiyor';
      out.push({ id: 'term', t: n.lastTermReason, badge: n.lastTermReason === 'OOMKilled' ? 'danger' : 'neutral', title: `Son sonlanma: ${n.lastTermReason} · ${when}` });
    }
  }
  return out;
}
