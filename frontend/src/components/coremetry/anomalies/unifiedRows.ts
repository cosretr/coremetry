// unifiedRows — v0.10.2027 (operatör: "Anomalies hâlâ karışık", "tekilleştirme
// çalışmadı"): /anomalies TEK liste. GEÇİŞ birleştirmesi: eski dedektörler
// silinene dek iki kaynak aynı satır modeline iner — ml_events grupları (D2,
// sunucu) + eski anomaly_events (son 24 sa, Dedektörler sekmesinin okuduğu).
// Gruplama iki kaynağa BİRLİKTE uygulanır: aynı servis + pencereler arası
// boşluk ≤ teyit penceresi (ML varsayılanı 10 dk; sunucu D2 kuralının aynısı,
// zincirleme) → tek satır. Aynı saniyede doğan iki log_template_new tek satır
// olur; aynı serviste ML olayı varsa onunla birleşir. SAF.
//
// "Ne oldu": ML üyesi varsa grubun en güçlü ML üyesinin sunucu özeti
// (ml.ExplainSummary); yoksa en güçlü eski olayın anlatımı
// (lib/anomalyNarrative.ts) — yeni log kalıbında kaç satır görüldüğüyle.
import type { AnomalyEvent, MLEvent } from '@/lib/types';
import { anomalyPlainHeadline } from '@/lib/anomalyNarrative';
import { anomalyEventHref, mlEventHref } from '@/lib/anomalyHref';
import type { SignalKind } from '@/lib/signalKind';
import { groupRows, groupStatusText, leadMember, type MLGroupRow, type MLGroupStatus } from './mlGroupRows';
import { mlRowText } from './MLTab';

/** Teyit penceresi (ns) — ml.quality.confirmWindowMin varsayılanı (10 dk). */
export const UNIFIED_SLACK_NS = 10 * 60e9;

export interface UnifiedRow {
  key: string;
  service: string;
  start: number; // unix ns
  end: number;   // unix ns
  signals: SignalKind[];
  status: MLGroupStatus;
  headline: string;
  href: string;
  ml: MLGroupRow[];
  legacy: AnomalyEvent[];
}

const SIGNAL_ORDER: SignalKind[] = ['trace', 'log', 'metric'];

/** SAF: eski olay türü → sinyal çipi. */
export function legacySignal(e: Pick<AnomalyEvent, 'kind'>): SignalKind {
  const k = String(e.kind);
  if (k.startsWith('log_') || k === 'elastic_ml') return 'log'; // Elastic ML job'ları log indeksinde koşar
  if (k.startsWith('trace_op') || k === 'op_latency') return 'trace';
  if (k.startsWith('ml:')) return 'log';
  return 'metric'; // behavior_change, calendar_traffic_drop, subject_red, metrik türleri
}

/** SAF: eski olayın durumu. */
function legacyStatus(e: AnomalyEvent): MLGroupStatus {
  if (e.verdict === 'not_anomaly') return 'rejected';
  return e.status === 'active' ? 'watching' : 'done';
}

/** SAF: eski olay satır cümlesi — yeni log kalıbında kaç satır görüldüğü de. */
export function legacyHeadline(e: AnomalyEvent): string {
  const h = anomalyPlainHeadline(e);
  if (e.kind === 'log_template_new' && e.currentCount > 0) return `${h}, ${e.currentCount.toLocaleString('tr-TR')} satır`;
  return h;
}

/** SAF: eski olayın kanıt satırı cümlesi — log türünde örnek satır, yoksa başlık. */
export function legacyEvidenceText(e: AnomalyEvent): string {
  const sample = (e.sample || '').trim();
  if ((e.kind === 'log_template_new' || e.kind === 'log_pattern') && sample) {
    const one = sample.split('\n')[0];
    return `${legacyHeadline(e)}. Örnek satır: ${one.length > 240 ? `${one.slice(0, 240)}…` : one}`;
  }
  return legacyHeadline(e);
}

/** v0.10.2038 — eski olayların kanıt grubu: aynı tür + aynı kalıp TEK satır. */
export interface LegacyEvidenceGroup {
  key: string; kind: AnomalyEvent['kind']; ids: string[];
  /** Cümlede olmayan bilgi (log türünde "3 satır"); diğer türlerde olayın kendi kanıt cümlesi. */
  text: string;
  /** Log türünde kalıp metni (tam; kopyalanabilir). */
  pattern?: string;
  /** En çok 2 örnek satır, tekrar edenler bir kez. */
  samples: string[];
  source: string;
}

/** SAF (v0.10.2038; operatör: aynı kalıp 100 ms arayla iki satır olarak
 *  listeleniyordu): eski olaylar → kanıt grupları. Aynı tür + aynı kalıp
 *  tek grup; log türünde satır sayıları toplanır, örnekler tekilleşir. */
export function legacyEvidenceGroups(events: AnomalyEvent[]): LegacyEvidenceGroup[] {
  const out: LegacyEvidenceGroup[] = [];
  const byKey = new Map<string, LegacyEvidenceGroup>();
  const counts = new Map<string, number>();
  for (const e of events) {
    const isLog = e.kind === 'log_template_new' || e.kind === 'log_pattern';
    const key = isLog ? `${e.kind}|${e.service}|${e.pattern}` : `id|${e.id}`;
    let g = byKey.get(key);
    if (!g) {
      g = { key, kind: e.kind, ids: [], text: isLog ? '' : legacyEvidenceText(e), pattern: isLog && e.pattern ? e.pattern : undefined, samples: [], source: legacySource(e) };
      byKey.set(key, g);
      out.push(g);
    }
    g.ids.push(e.id);
    counts.set(key, (counts.get(key) ?? 0) + (e.currentCount > 0 ? e.currentCount : 1));
    const one = (e.sample || '').trim().split('\n')[0]?.trim();
    if (isLog && one && g.samples.length < 2 && !g.samples.includes(one)) g.samples.push(one.length > 400 ? `${one.slice(0, 400)}…` : one);
  }
  for (const g of byKey.values()) {
    if (!g.text) g.text = `${(counts.get(g.key) ?? 0).toLocaleString('tr-TR')} satır${g.samples.length ? '; örnek:' : ''}`;
  }
  return out;
}

/** SAF: eski olayın kaynak notu (sinyalin nereden okunduğu). */
export function legacySource(e: Pick<AnomalyEvent, 'kind'>): string {
  switch (String(e.kind)) {
    case 'log_template_new': return 'log kalıpları (Drain)';
    case 'log_pattern': return 'log deseni sayımı';
    case 'elastic_ml': return 'Elastic ML';
    case 'trace_op': case 'trace_op_latency': case 'op_latency': return "ClickHouse span'leri";
    case 'behavior_change': return 'davranış motoru (28 gün)';
    default: return 'metrikler';
  }
}

/** SAF: eski olay detayının durum metni (liste kuralıyla aynı; 0 süre = tek ölçüm). */
export function legacyStatusText(e: AnomalyEvent, nowNs: number): string {
  const st = legacyStatus(e);
  if (st === 'rejected') return 'Değil dendi';
  const end = st === 'watching' ? nowNs : e.lastSeen;

  return groupStatusText({ status: st, start: e.startedAt, end }, nowNs);
}

/** SAF: en güçlü eski olay (tepe oran, sayım, son görülme, kimlik). */
function leadLegacy(es: AnomalyEvent[]): AnomalyEvent {
  return [...es].sort((a, b) => b.peakRatio - a.peakRatio || b.currentCount - a.currentCount || b.lastSeen - a.lastSeen || a.id.localeCompare(b.id))[0];
}

function combineStatus(ss: MLGroupStatus[]): MLGroupStatus {
  if (ss.includes('running')) return 'running';
  if (ss.includes('watching')) return 'watching';
  if (ss.includes('done')) return 'done';
  return 'rejected';
}

type Item = { start: number; end: number; ml?: MLGroupRow; legacy?: AnomalyEvent };

/** SAF: iki kaynak → birleşik satırlar (sıra: durum, sonra başlangıç yeni önce). */
export function unifiedRows(ml: MLEvent[], legacy: AnomalyEvent[], slackNs = UNIFIED_SLACK_NS): UnifiedRow[] {
  const bySvc = new Map<string, Item[]>();
  const push = (svc: string, it: Item) => {
    const l = bySvc.get(svc);
    if (l) l.push(it); else bySvc.set(svc, [it]);
  };
  for (const g of groupRows(ml)) push(g.service, { start: g.start, end: Math.max(g.end, g.start), ml: g });
  for (const e of legacy) push(e.service || '', { start: e.startedAt, end: Math.max(e.lastSeen, e.startedAt), legacy: e });
  const out: UnifiedRow[] = [];
  for (const [svc, items] of bySvc) {
    items.sort((a, b) => a.start - b.start || (a.ml?.key ?? a.legacy?.id ?? '').localeCompare(b.ml?.key ?? b.legacy?.id ?? ''));
    let cur: Item[] = [];
    let end = 0;
    const flush = () => { if (cur.length) out.push(buildRow(svc, cur)); };
    for (const it of items) {
      if (cur.length && it.start <= end + slackNs) {
        cur.push(it);
        end = Math.max(end, it.end);
        continue;
      }
      flush();
      cur = [it];
      end = it.end;
    }
    flush();
  }
  return sortUnified(out);
}

function buildRow(service: string, items: Item[]): UnifiedRow {
  const ml = items.flatMap(i => (i.ml ? [i.ml] : []));
  const legacy = items.flatMap(i => (i.legacy ? [i.legacy] : []));
  const sig = new Set<SignalKind>();
  for (const g of ml) g.signals.forEach(s => sig.add(s));
  for (const e of legacy) sig.add(legacySignal(e));
  let headline: string;
  let href: string;
  let key: string;
  if (ml.length) {
    const lead = leadMember(ml.map(g => g.lead));
    headline = mlRowText(lead);
    href = mlEventHref(lead.id);
    key = `ml:${ml.find(g => g.lead.id === lead.id)?.key ?? lead.id}`;
  } else {
    const lead = leadLegacy(legacy);
    headline = legacyHeadline(lead);
    href = anomalyEventHref(lead.id);
    key = `ae:${lead.id}`;
  }
  return {
    key, service, href, headline, ml, legacy,
    start: Math.min(...items.map(i => i.start)),
    end: Math.max(...items.map(i => i.end)),
    signals: SIGNAL_ORDER.filter(s => sig.has(s)),
    status: combineStatus([...ml.map(g => g.status), ...legacy.map(legacyStatus)]),
  };
}

/** SAF: süren önce (Sürüyor, İzleniyor), sonra başlangıç (yeni önce). */
export function sortUnified(rows: UnifiedRow[]): UnifiedRow[] {
  const rank = (s: MLGroupStatus) => (s === 'running' ? 3 : s === 'watching' ? 2 : s === 'done' ? 1 : 0);
  return [...rows].sort((a, b) => rank(b.status) - rank(a.status) || b.start - a.start || a.key.localeCompare(b.key));
}

/** SAF: süren mi (üst tablo) yoksa biten mi ("Bitenler" bölümü). */
export function isOpenRow(r: Pick<UnifiedRow, 'status'>): boolean {
  return r.status === 'running' || r.status === 'watching';
}

/** SAF (v0.10.2030; operatör: "Ne oldu" servisi tekrar ediyor): liste hücresi
 *  cümlesi — servis kendi sütununda; baştaki "<servis>: " / "<servis> " öneki
 *  atılır, ilk harf büyür. Cümle KURULMAZ, yalnız önek kırpılır. */
export function stripServicePrefix(text: string, service: string): string {
  let t = text.trim();
  const svc = service.trim();
  if (svc) {
    for (const pre of [`${svc}: `, `${svc} servisinin `, `${svc} servisi, `, `${svc} `]) {
      if (t.startsWith(pre)) { t = t.slice(pre.length); break; }
    }
  }
  if (!t) return text;
  const first = t[0] === 'i' ? 'İ' : t[0].toLocaleUpperCase('tr-TR');
  return first + t.slice(1);
}

/** SAF: listedeki "Ne oldu" metni. */
export function listHeadline(r: Pick<UnifiedRow, 'headline' | 'service'>): string {
  return stripServicePrefix(r.headline, r.service);
}
