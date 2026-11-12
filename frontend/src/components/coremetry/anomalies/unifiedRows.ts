// unifiedRows — v0.10.2027 (operatör: "Anomalies hâlâ karışık", "tekilleştirme
// çalışmadı"): /anomalies TEK liste. v0.10.2051: birleştirme, gruplama,
// durum ve sıra SUNUCUDA (internal/ml/unified.go, /api/anomalies/unified;
// istemci birleştirmesi kaynak başına 200 satırda sessizce kesiyordu). Bu
// dosyada yalnız SATIR METNİ kalır: satırın lideri (ML üyesi ya da eski olay)
// → "Ne oldu" cümlesi + bağlantı, eski olayın kanıt / durum cümleleri. SAF.
//
// "Ne oldu": ML lideri varsa sunucu özeti (ml.ExplainSummary, mlRowText);
// yoksa eski olayın anlatımı (lib/anomalyNarrative.ts) — yeni log kalıbında
// kaç satır görüldüğüyle.
import type { AnomalyEvent, UnifiedAnomalyRow } from '@/lib/types';
import { anomalyPlainHeadline } from '@/lib/anomalyNarrative';
import { anomalyEventHref, mlEventHref } from '@/lib/anomalyHref';
import type { SignalKind } from '@/lib/signalKind';
import { groupStatusText, type MLGroupStatus } from './mlGroupRows';
import { mlRowText } from './MLTab';

/** SAF: satırın "Ne oldu" cümlesi (servis önekli; liste hücresi listHeadline). */
export function rowHeadline(r: Pick<UnifiedAnomalyRow, 'mlLead' | 'legacyLead' | 'service'>): string {
  if (r.mlLead) return mlRowText(r.mlLead);
  if (r.legacyLead) return legacyHeadline(r.legacyLead);
  return r.service;
}

/** SAF: satır bağlantısı — liderin detay sayfası. */
export function rowHref(r: Pick<UnifiedAnomalyRow, 'mlLead' | 'legacyLead'>): string {
  if (r.mlLead) return mlEventHref(r.mlLead.id);
  if (r.legacyLead) return anomalyEventHref(r.legacyLead.id);
  return '/anomalies';
}

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
export function listHeadline(r: Pick<UnifiedAnomalyRow, 'mlLead' | 'legacyLead' | 'service'>): string {
  return stripServicePrefix(rowHeadline(r), r.service);
}
