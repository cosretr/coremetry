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
import type { AnomalyEvent, MLEvent, MLRecordView, UnifiedAnomalyRow } from '@/lib/types';
import { t, tf, type Lang } from '@/lib/i18n';
import { KNOWN_METRIC_IDS, measureKey, recordUsable } from './mlRecord';
import { recordHeadline } from './mlRecordText';
import { anomalyPlainHeadline } from '@/lib/anomalyNarrative';
import { anomalyEventHref, mlEventHref } from '@/lib/anomalyHref';
import type { SignalKind } from '@/lib/signalKind';
import { groupStatusText, type MLGroupStatus } from './mlGroupRows';
import { mlRowText } from './MLTab';

// v0.10.2272 — ML liderinin olay kaydı (ml_events.record; liste yanıtı zaten
// taşır, yeni sorgu yok) okunur ve bilinen bir ölçüsü varsa "Ne oldu" yeni
// kartın başlığıyla (mlRecordText.recordHeadline) arayüz dilinde kurulur.
// Kayıt yok / bozuk / invalid / ölçü bilinmiyor → sunucu cümlesi (mlRowText).
// v0.10.2330 — bilinen metrik ifadeleri (KNOWN_METRIC_IDS) de kayıttan.
const recordCache = new WeakMap<MLEvent, MLRecordView | null>();

/** SAF (v0.10.2330): metrik olayının ifade kimliği — Go splitThanosPart
 *  ("aile|kimlik@cluster", "#topic" eki kimliği değiştirmez). Kart bu kimliği
 *  replay ucunda RecordView.metricId olarak alır; liste satırı aynı kimliği
 *  `code`'dan çıkarır (yeni alan yok). Bilinmeyen kimlik → '' (bugünkü yedek). */
export function knownMetricId(e: Pick<MLEvent, 'signal' | 'code'>): string {
  if (e.signal !== 'metric' || !e.code) return '';
  const bar = e.code.indexOf('|');
  const id = bar < 0 ? '' : e.code.slice(bar + 1).split('@')[0].split('#')[0];
  return (KNOWN_METRIC_IDS as readonly string[]).includes(id) ? id : '';
}

/** SAF: liderin kart başlığına yeten kaydı; yetmiyorsa null. */
export function leadRecord(e: MLEvent): MLRecordView | null {
  const hit = recordCache.get(e);
  if (hit !== undefined) return hit;
  let rec: MLRecordView | null = null;
  if (e.record && e.publishState !== 'invalid') {
    try {
      const raw = JSON.parse(e.record) as Partial<MLRecordView>;
      const metricId = knownMetricId(e); // v0.10.2330 — bilinen metrik ölçüleri kartla aynı adla
      const cand = { ...raw, detector: e.detector, ...(metricId ? { metricId } : {}) } as MLRecordView;
      if (raw && raw.v === 1 && raw.series && raw.subject && typeof raw.expectedValue === 'number' && recordUsable(cand)) {
        const k = measureKey(cand);
        if (k && k !== 'mlRecord.measure.value') rec = cand;
      }
    } catch { rec = null; }
  }
  recordCache.set(e, rec);
  return rec;
}

/** SAF: satırın "Ne oldu" cümlesi (servis önekli; liste hücresi listHeadline). */
export function rowHeadline(r: Pick<UnifiedAnomalyRow, 'mlLead' | 'legacyLead' | 'service'>, lang: Lang = 'tr'): string {
  if (r.mlLead) {
    const rec = leadRecord(r.mlLead);
    if (rec) return recordHeadline(rec, t(measureKey(rec)!, lang), lang);
    return mlRowText(r.mlLead, lang);
  }
  if (r.legacyLead) return legacyHeadline(r.legacyLead, lang);
  return r.service;
}

/** SAF: satır bağlantısı — liderin detay sayfası. */
export function rowHref(r: Pick<UnifiedAnomalyRow, 'mlLead' | 'legacyLead'>): string {
  if (r.mlLead) return mlEventHref(r.mlLead.id);
  if (r.legacyLead) return anomalyEventHref(r.legacyLead.id);
  return '/inbox?tab=signals'; // v0.10.2481 — liste Problems › Sinyaller
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
export function legacyHeadline(e: AnomalyEvent, lang: Lang = 'tr'): string {
  const h = anomalyPlainHeadline(e, lang);
  if (e.kind === 'log_template_new' && e.currentCount > 0) {
    const n = e.currentCount.toLocaleString(lang === 'tr' ? 'tr-TR' : 'en-US');
    return `${h}, ${tf('anomalyHeadline.lines', lang, { n })}`;
  }
  return h;
}

/** SAF: eski olayın kanıt satırı cümlesi — log türünde örnek satır, yoksa başlık. */
// v0.10.2282 — lang (varsayılan 'tr'; TR metin aynı).
export function legacyEvidenceText(e: AnomalyEvent, lang: Lang = 'tr'): string {
  const sample = (e.sample || '').trim();
  if ((e.kind === 'log_template_new' || e.kind === 'log_pattern') && sample) {
    const one = sample.split('\n')[0];
    return tf('anomalyHeadline.sample', lang, { h: legacyHeadline(e, lang), line: one.length > 240 ? `${one.slice(0, 240)}…` : one });
  }
  return legacyHeadline(e, lang);
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
export function legacyEvidenceGroups(events: AnomalyEvent[], lang: Lang = 'tr'): LegacyEvidenceGroup[] {
  const out: LegacyEvidenceGroup[] = [];
  const byKey = new Map<string, LegacyEvidenceGroup>();
  const counts = new Map<string, number>();
  for (const e of events) {
    const isLog = e.kind === 'log_template_new' || e.kind === 'log_pattern';
    const key = isLog ? `${e.kind}|${e.service}|${e.pattern}` : `id|${e.id}`;
    let g = byKey.get(key);
    if (!g) {
      g = { key, kind: e.kind, ids: [], text: isLog ? '' : legacyEvidenceText(e), pattern: isLog && e.pattern ? e.pattern : undefined, samples: [], source: legacySource(e, lang) };
      byKey.set(key, g);
      out.push(g);
    }
    g.ids.push(e.id);
    counts.set(key, (counts.get(key) ?? 0) + (e.currentCount > 0 ? e.currentCount : 1));
    const one = (e.sample || '').trim().split('\n')[0]?.trim();
    if (isLog && one && g.samples.length < 2 && !g.samples.includes(one)) g.samples.push(one.length > 400 ? `${one.slice(0, 400)}…` : one);
  }
  for (const g of byKey.values()) {
    if (!g.text) g.text = `${tf('aed.sum.lines', lang, { n: (counts.get(g.key) ?? 0).toLocaleString(lang === 'tr' ? 'tr-TR' : 'en-US') })}${g.samples.length ? t('mlx.leg.sample', lang) : ''}`;
  }
  return out;
}

/** SAF: eski olayın kaynak notu (sinyalin nereden okunduğu). */
export function legacySource(e: Pick<AnomalyEvent, 'kind'>, lang: Lang = 'tr'): string {
  switch (String(e.kind)) {
    case 'log_template_new': return t('mlx.leg.src.tpl', lang);
    case 'log_pattern': return t('mlx.leg.src.pattern', lang);
    case 'elastic_ml': return 'Elastic ML';
    case 'trace_op': case 'trace_op_latency': case 'op_latency': return t('mlx.src.chSpans', lang);
    case 'behavior_change': return t('mlx.leg.src.behavior', lang);
    default: return t('mlx.leg.src.metrics', lang);
  }
}

/** SAF: eski olay detayının durum metni (liste kuralıyla aynı; 0 süre = tek ölçüm). */
export function legacyStatusText(e: AnomalyEvent, nowNs: number, lang: Lang = 'tr'): string {
  const st = legacyStatus(e);
  if (st === 'rejected') return t('anomalies.status.rejected', lang);
  const end = st === 'watching' ? nowNs : e.lastSeen;

  return groupStatusText({ status: st, start: e.startedAt, end }, nowNs, lang);
}

/** SAF (v0.10.2030; operatör: "Ne oldu" servisi tekrar ediyor): liste hücresi
 *  cümlesi — servis kendi sütununda; baştaki "<servis>: " / "<servis> " öneki
 *  atılır, ilk harf büyür. Cümle KURULMAZ, yalnız önek kırpılır. */
export function stripServicePrefix(text: string, service: string, lang: Lang = 'tr'): string {
  let t = text.trim();
  const svc = service.trim();
  if (svc) {
    for (const pre of [`${svc}: `, `${svc} servisinin `, `${svc} servisi, `, `${svc} servisi `, `${svc} `]) { // `servisi ` v0.10.2119 (hacim sıçraması)
      if (t.startsWith(pre)) { t = t.slice(pre.length); break; }
    }
  }
  if (!t) return text;
  // v0.10.2291 — Türkçe büyük harf kuralı (i → İ) yalnız TR metinde.
  const first = lang !== 'tr' ? t[0].toLocaleUpperCase('en-US') : t[0] === 'i' ? 'İ' : t[0].toLocaleUpperCase('tr-TR');
  return first + t.slice(1);
}

/** v0.10.2302 — liste hücresinin metni + sunucu cümlesi mi (ML lideri kayıtsız ve
 *  summary taşıyor → mlRowText sunucu cümlesi). Kayıttan / katalogdan kurulan
 *  metin false; render yeri true'yu EN arayüzde OrigLang ile sarar. SAF. */
export function listHeadlineView(r: Pick<UnifiedAnomalyRow, 'mlLead' | 'legacyLead' | 'service'>, lang: Lang = 'tr'): { text: string; serverText: boolean } {
  return { text: listHeadline(r, lang), serverText: !!r.mlLead && !leadRecord(r.mlLead) && !!r.mlLead.summary };
}

/** v0.10.2476 — SAF: grup kararı consumer lag'i beklenen günlük desen
 *  saydıysa (P3) satırın ek notu; değilse ''. TR sunucu cümlesi, EN anahtardan. */
export function rowLagPattern(r: Pick<UnifiedAnomalyRow, 'mlLead'>, lang: Lang = 'tr'): string {
  const d = r.mlLead?.groupShadow;
  if (!d?.pattern || !d.patternDays) return '';
  return lang === 'tr' ? `${d.pattern} · P3` : tf('anomalies.lagPattern', lang, { d: d.patternDays });
}

/** SAF: listedeki "Ne oldu" metni. */
export function listHeadline(r: Pick<UnifiedAnomalyRow, 'mlLead' | 'legacyLead' | 'service'>, lang: Lang = 'tr'): string {
  return stripServicePrefix(rowHeadline(r, lang), r.service, lang);
}
