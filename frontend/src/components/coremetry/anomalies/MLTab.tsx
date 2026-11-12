// MLTab — /anomalies "Log ML" sekmesi (log ML dilim 0). components/coremetry
// altında çünkü Tailwind sınıfları yalnız burada üretiliyor (features/ değil).
// Eski dedektörler kendi sekmesinde; burada yalnız ml_events (gölge) +
// satır başına «gerçek / değil» + not (anomaly_verdicts 'ml:' önekli) ve
// üstte dedektör karnesi (30 gün isabet + örnek). Tablo: coremetry DataTable.
import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { Badge } from '@/components/shadcn/badge';
import { api } from '@/lib/api';
import { keys, useMLEvents, useMLScorecard } from '@/lib/queries';
import { fmtClock, fmtDateTime } from '@/lib/utils';
import type { MLEvent, MLDetectorScore, MLGate, MLBackfillProgress, MLTickStatus, MLSignalTick, MLScorecard, MLKindTick, MLBaselineStatus } from '@/lib/types';
import { Link } from 'react-router-dom';
import { MLPeriodReplay } from './MLPeriodReplay';
import { mlEventHref, mlEventPlayHref } from '@/lib/anomalyHref';
import { buttonVariants } from '@/components/shadcn/button';
import { serviceHref } from '@/lib/serviceHref';
import { SIGNAL_KINDS, signalKindOf } from '@/lib/signalKind';
import { SignalKindBadge } from '@/components/coremetry/SignalKindBadge';
import { Sparkline } from '@/components/Sparkline';

export const ML_DETECTOR_TR: Record<string, string> = {
  log_volume: 'Log hacmi', error_rate: 'Hata oranı', service_silent: 'Servis sustu',
  template_spike: 'Şablon spike', rare_template: 'Nadir şablon', rare_error_code: 'Nadir hata kodu',
  pod_outlier: 'Aykırı pod', change_point: 'Kırılma',
  op_latency: 'Endpoint gecikmesi', op_error_rate: 'Endpoint hata oranı', throughput_drop: 'Trafik düşüşü',
  metric_jvm: 'JVM', metric_pool: 'Connection pool', metric_kafka: 'Kafka', metric_http_client: 'HTTP client', metric_k8s: 'Kubernetes',
  dependency_latency: 'Bağımlılık gecikmesi', trace_pod_outlier: 'Aykırı pod (trace)', trace_change_point: 'Kırılma (trace)',
};
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

/** Sinyal karnesi satırı — SAF (test). */
export function signalScoreLabel(s: MLDetectorScore): string {
  const name = SIGNAL_TR[s.detector] ?? s.detector;
  if (s.samples <= 0) return `${name}: ${s.events} olay · karar yok`;
  return `${name}: %${Math.round(s.hitRate * 100)} isabet (${s.samples} karar / ${s.events} olay)`;
}

/** Terfi kapısı metni — SAF (test): geçemediği koşullar sırayla. */
export function gateLabel(g: MLGate | undefined): string {
  if (!g) return '';
  if (g.ok) return 'terfi kapısı geçildi';
  return `terfi kapısı: ${(g.reasons ?? []).join(' · ')}`;
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

/** Son tik + VM bekleme satırı — SAF (test). nowSec: şimdi (unix sn). */
export function tickLabel(t: MLTickStatus | undefined, nowSec: number): string {
  if (!t) return 'son tik yok';
  const ago = Math.max(0, Math.round((nowSec - t.at) / 60));
  const parts = [`son tik ${ago} dk önce · ${Math.round(t.durationMs / 1000)} sn · ${t.events} olay`];
  if (t.docs) parts.push(`${t.docs.toLocaleString('tr-TR')} belge`);
  if (t.partial) parts.push(`${t.partial} kısmi ES sonucu (yazılmadı)`);
  if (t.batchCut) parts.push(`batch kuralı ${t.batchCut} adayı kesti`);
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
export function durationLabel(ns: number): string {
  const m = Math.max(0, Math.round(ns / 60e9));
  if (m < 60) return `${m} dk`;
  const h = Math.floor(m / 60);
  return h < 48 ? `${h} sa ${m % 60} dk` : `${Math.round(h / 24)} g`;
}

/** Dedektör → recorder dedektör grubu (tik özeti anahtarı) — SAF. */
export function detectorGroup(d: string): string {
  if (d.startsWith('metric_')) return 'metric_ml';
  switch (d) {
    case 'log_volume': case 'error_rate': case 'service_silent': return 'log_red';
    case 'template_spike': case 'rare_template': return 'log_templates';
    case 'rare_error_code': case 'pod_outlier': return 'log_breakdown';
    case 'change_point': return 'log_change_point';
    case 'op_latency': case 'op_error_rate': case 'throughput_drop': return 'trace_red';
    case 'dependency_latency': return 'dependency';
    default: return 'trace_pods';
  }
}

export interface DetectorRow { detector: string; signal: string; enabled: boolean; events: number; samples: number; hitRate: number; tick?: MLSignalTick; gate?: MLGate }

/** Karne satırları — SAF (test): tüm dedektörler, olay yoksa 0; kapalı = kapı gerekçesinden. */
export function detectorRows(sc: MLScorecard, signal: MLSignal): DetectorRow[] {
  const byDet = new Map(sc.detectors.map(d => [d.detector, d]));
  return Object.keys(sc.signals ?? {}).filter(d => !signal || sc.signals[d] === signal).map(d => {
    const sc1 = byDet.get(d);
    const gate = sc.gates?.[d];
    const reasons = gate?.reasons ?? [];
    return { detector: d, signal: sc.signals[d], enabled: !reasons.includes('dedektör kapalı') && !reasons.includes('ana anahtar kapalı'),
      events: sc1?.events ?? 0, samples: sc1?.samples ?? 0, hitRate: sc1?.hitRate ?? 0, tick: sc.tick?.signals?.[detectorGroup(d)], gate };
  });
}

/** Durum kutuları — SAF (test). */
export function statusTiles(sc: MLScorecard, signal: MLSignal, nowSec: number): { k: string; v: string; s: string; warn?: boolean }[] {
  const t = sc.tick;
  const ago = t ? Math.max(0, Math.round((nowSec - t.at) / 60)) : null;
  const bf = (sc.backfill ?? []).find(b => !signal || b.signal === signal);
  const miss = bf?.missing?.length ?? 0;
  const events = (sc.bySignal ?? []).filter(s => !signal || s.detector === signal).reduce((a, s) => a + s.events, 0);
  const samples = (sc.bySignal ?? []).filter(s => !signal || s.detector === signal).reduce((a, s) => a + s.samples, 0);
  const bo = t?.backoff ?? [];
  return [
    { k: 'Son tik', v: t ? `${ago} dk önce` : 'yok', s: t ? `${Math.round(t.durationMs / 1000)} sn · ${t.events} olay${t.docs ? ` · ${t.docs.toLocaleString('tr-TR')} belge` : ''}${t.partial ? ` · ${t.partial} kısmi` : ''}${t.batchCut ? ` · batch kuralı ${t.batchCut} adayı kesti` : ''}` : 'tik yok', warn: !!t?.lastError },
    { k: 'Log geçmişi', v: bf ? `${bf.done + miss} / ${bf.total} sa` : '—', s: bf ? (bf.complete ? 'tamam' : bf.running ? 'sürüyor' : 'duraklı') + (miss ? ` · eksik ${miss}` : '') : 'kayıt yok' },
    { k: 'Olay · 30 gün', v: String(events), s: samples ? `${samples} karar` : 'karar yok · terfi kapalı' },
    { k: 'VM bekleme', v: bo.length ? `${bo.length} aile` : 'yok', s: bo.map(b => `${b.family} · ${Math.max(1, Math.round((b.until - nowSec) / 60))} dk`).join(', ') || 'tüm aileler açık', warn: bo.length > 0 },
  ];
}

const KIND_ORDER = ['log', 'trace', 'metric'];

/** Sinyal türü başına son tik ölçümü — SAF (test). v0.10.1812, audit §10 dilim 1.1. */
export function kindTickLines(t: MLTickStatus | undefined, signal: MLSignal): { kind: string; text: string; warn: boolean }[] {
  const kinds = t?.kinds ?? {};
  return Object.keys(kinds).filter(k => !signal || k === signal)
    .sort((a, b) => (KIND_ORDER.indexOf(a) + 1 || 99) - (KIND_ORDER.indexOf(b) + 1 || 99) || a.localeCompare(b))
    .map(kind => {
      const k: MLKindTick = kinds[kind];
      const n = (v: number) => v.toLocaleString('tr-TR');
      const bits = [`${n(k.services)} servis`, `${n(k.series)} seri (${n(k.seriesRows)} satır)`, `sorgu ES ${k.es} · VM ${k.vm} · CH ${k.ch}`, `${(k.durationMs / 1000).toFixed(1)} sn`];
      if (k.dropped) bits.push(`${SIGNAL_TR[kind]?.toLowerCase() ?? kind} sinyalinde ${n(k.dropped)} servis tavan dışında kaldı`);
      return { kind, text: `${SIGNAL_TR[kind] ?? kind} · ${bits.join(' · ')}`, warn: k.dropped > 0 };
    });
}

/** Günlük baseline işinin tek satırı — SAF (test). v0.10.1814, audit §4.2. null = durum kaydı yok (iş hiç koşmadı / kapalı). */
export function baselineStatusLine(st: MLBaselineStatus | undefined): { text: string; warn: boolean } | null {
  if (!st) return null;
  const n = (v: number) => v.toLocaleString('tr-TR');
  const run = st.last;
  const bits: string[] = [];
  if (st.lastDay) bits.push(`son gün ${st.lastDay}`);
  if (run) bits.push(`${n(run.rows)} satır`, `${(run.durationMs / 1000).toFixed(1)} sn`);
  if (st.running) bits.push(`işleniyor: ${st.running}`);
  if (run?.error) bits.push(`${run.day} hatalı: ${run.error}`);
  if (st.manual) bits.push(`elle ${st.manual.day}: ${st.manual.error ? `hata — ${st.manual.error}` : `${n(st.manual.rows)} satır`}`);
  if (st.profiles) bits.push(st.profiles.error ? `profil haftası ${st.profiles.week} hatalı: ${st.profiles.error}` : `profil haftası ${st.profiles.week}: ${n(st.profiles.services)} servis, ${n(st.profiles.changed)} değişim`);
  if (!bits.length) bits.push('henüz işlenmiş gün yok');
  return { text: `Baseline · ${bits.join(' · ')}`, warn: !!run?.error || !!st.manual?.error || !!st.profiles?.error };
}

// ── v0.10.1879 — sade görünüm (operatör, prod ekran görüntüsü: "Bu sayfa genel
// tasarıma uymuyor, çok net anlaşılmıyor"). Önce düz cümle, sonra dört sorunun
// cevabını veren tablo (ne izliyor / açık mı / ne buldu / Problem açar mı);
// tik süreleri, aday sayıları ve terfi kapısı gerekçeleri "Teknik detay"
// altında kapalı. Bilgi silinmedi, yalnız yerleşti.

/** Durumun düz cümlesi + (varsa) dikkat satırları — SAF (test). */
export function mlStatusSummary(sc: MLScorecard, signal: MLSignal, nowSec: number): { text: string; warnings: string[] } {
  const n = (v: number) => v.toLocaleString('tr-TR');
  const t = sc.tick;
  const scoped = (sc.bySignal ?? []).filter(x => !signal || x.detector === signal);
  const events = scoped.reduce((a, x) => a + x.events, 0);
  const samples = scoped.reduce((a, x) => a + x.samples, 0);
  const warnings: string[] = [];
  let head: string;
  if (!t) {
    head = 'Anomali tespiti henüz hiç çalışmadı.';
    warnings.push('Son kontrol kaydı yok: tespit kapalı olabilir (Settings › Anomaly).');
  } else {
    const ago = Math.max(0, Math.round((nowSec - t.at) / 60));
    const dur = Math.max(1, Math.round(t.durationMs / 1000));
    const when = ago === 0 ? 'az önce' : `${n(ago)} dakika önce`;
    head = `Anomali tespiti çalışıyor. Son kontrol ${when} yapıldı, ${n(dur)} saniye sürdü ve ${t.events > 0 ? `${n(t.events)} olay buldu` : 'olay bulmadı'}.`;
    if (ago > 15) {
      head = `Anomali tespiti gecikiyor: son kontrol ${n(ago)} dakika önce yapıldı.`;
      warnings.push('Kontroller beş dakikada bir çalışır; bu kadar uzun ara, tespitin durduğunu gösterebilir.');
    }
    const bo = t.backoff ?? [];
    if (bo.length > 0) warnings.push(`Metrik kaynağı ${n(bo.length)} metrik ailesinde geçici olarak bekletiliyor (${bo.map(b => b.family).join(', ')}).`);
    if (t.partial) warnings.push(`Son kontrolde ${n(t.partial)} kaynak eksik okundu.`);
  }
  const bf = (sc.backfill ?? []).find(b => !signal || b.signal === signal);
  if (bf && !bf.complete) warnings.push(`Log geçmişi henüz tamamlanmadı: ${n(bf.done + (bf.missing?.length ?? 0))} / ${n(bf.total)} saat${bf.running ? ', sürüyor' : ', duraklı'}.`);
  const tail = events > 0
    ? ` Son ${sc.windowDays} günde ${n(events)} olay kaydedildi; ${samples > 0 ? `${n(samples)} tanesi değerlendirildi` : 'henüz hiçbiri değerlendirilmedi'}.`
    : ` Son ${sc.windowDays} günde olay kaydedilmedi.`;
  return { text: head + tail, warnings };
}

/** Değerlendirme hücresi — SAF (test): "%0 (2)" yerine "2 değerlendirme · 0 doğru". */
export function detectorVerdictText(r: Pick<DetectorRow, 'samples' | 'hitRate'>): string {
  if (r.samples <= 0) return '';
  return `${r.samples.toLocaleString('tr-TR')} değerlendirme · ${Math.round(r.hitRate * r.samples).toLocaleString('tr-TR')} doğru`;
}

/** "Problem açar mı" hücresi — SAF (test). Ayrıntı (kapı gerekçeleri) ipucunda. */
export function detectorPromoteText(r: Pick<DetectorRow, 'enabled' | 'gate'>): { text: string; title: string } {
  const reasons = (r.gate?.reasons ?? []).filter(x => x !== 'dedektör kapalı' && x !== 'ana anahtar kapalı');
  if (!r.enabled) return { text: '', title: 'Dedektör kapalı' };
  if (r.gate?.ok) return { text: 'Evet', title: 'Bu dedektörün olayları Problem açabilir' };
  return { text: 'Hayır', title: reasons.length ? `Neden: ${reasons.join(' · ')}` : 'Otomatik Problem açma koşulları sağlanmadı' };
}

const DCOLS: ColumnDef<DetectorRow>[] = [
  { id: 'detector', label: 'Dedektör', width: 220, minWidth: 150, flex: true, priority: 1, sortValue: r => ML_DETECTOR_TR[r.detector] ?? r.detector, naturalDir: 'asc' },
  { id: 'signal', label: 'İzlediği', width: 110, minWidth: 90, priority: 1, sortValue: r => r.signal, naturalDir: 'asc' },
  // Açık olanlar önde, kendi içinde bulduğu olay sayısına göre.
  { id: 'state', label: 'Durum', width: 100, minWidth: 80, priority: 1, sortValue: r => (r.enabled ? 1e9 : 0) + r.events },
  { id: 'events', label: 'Son 30 günde olay', width: 150, minWidth: 110, priority: 1, numeric: true, sortValue: r => r.events },
  { id: 'hit', label: 'Değerlendirme', width: 200, minWidth: 130, priority: 2, sortValue: r => (r.samples > 0 ? r.hitRate : -1) },
  { id: 'gate', label: 'Problem açar mı', width: 140, minWidth: 110, priority: 2, sortValue: r => (r.gate?.ok ? 1 : 0) },
];

// Teknik detay tablosu: son kontrolün dedektör başına ölçümü + kapı gerekçesi.
const DTECH: ColumnDef<DetectorRow>[] = [
  { id: 'detector', label: 'Dedektör', width: 180, minWidth: 140, priority: 1, sortValue: r => ML_DETECTOR_TR[r.detector] ?? r.detector, naturalDir: 'asc' },
  { id: 'tick', label: 'Kontrol süresi', width: 110, numeric: true, priority: 2, sortValue: r => r.tick?.sourceMs ?? -1 },
  { id: 'services', label: 'Bakılan servis', width: 110, numeric: true, priority: 2, sortValue: r => r.tick?.services ?? -1 },
  { id: 'candidates', label: 'Aday', width: 70, numeric: true, priority: 3, sortValue: r => r.tick?.candidates ?? -1 },
  { id: 'written', label: 'Kaydedilen', width: 90, numeric: true, priority: 3, sortValue: r => r.tick?.written ?? -1 },
  { id: 'gate', label: 'Problem açma koşulları', flex: true, minWidth: 200, priority: 1, sortValue: r => (r.gate?.ok ? 0 : 1) },
];

// v0.10.1828 — 'groups' (Gruplar): anomali grupları (Faz 3); emekli
// /incidents buraya iner.
export type AnomaliesTab = 'detectors' | 'ml' | 'groups';
/** SAF: bilinmeyen değer → detectors (eski linkler bozulmaz). */
export function resolveAnomaliesTab(raw: string | null): AnomaliesTab {
  return raw === 'ml' ? 'ml' : raw === 'groups' ? 'groups' : 'detectors';
}

export function AnomaliesTabBar({ value, onChange }: { value: AnomaliesTab; onChange: (t: AnomaliesTab) => void }) {
  return (
    <div className="mb-3">
      <SegmentedControl<AnomaliesTab> value={value} onChange={onChange} aria-label="Anomali kaynağı"
        options={[{ value: 'detectors', label: 'Dedektörler' }, { value: 'ml', label: 'ML' }, { value: 'groups', label: 'Gruplar' }]} />
    </div>
  );
}

/** SAF (test): seyrin tepe değeri (sıralama); seyir yoksa -1. */
export function sparkPeak(e: MLEvent): number {
  return e.spark?.v.length ? Math.max(...e.spark.v) : -1;
}

/** SAF (test): satır metni — sunucu özeti; yoksa (eski sunucu) servis · dedektör adı. Cümle KURMAZ. */
export function mlRowText(e: MLEvent): string {
  return e.summary || `${e.service} · ${ML_DETECTOR_TR[e.detector] ?? e.detector}`;
}
const fmtTs = (ns: number) => fmtDateTime(new Date(ns / 1e6));

/** SAF (test): v0.10.2013 D2 — "Grup" hücresi. Tek üyeli / grupsuz = '' (soluk "—"). */
export function mlGroupLabel(e: MLEvent): { text: string; title: string } {
  const n = e.groupSize ?? 0;
  if (n <= 1) return { text: '', title: '' };
  const dets = e.groupDetectors ?? [];
  const title = `Aynı servis, aynı zaman: ${dets.map(d => ML_DETECTOR_TR[d] ?? d).join(', ')}`;
  return { text: dets.length > 1 ? `${dets.length} dedektör birlikte` : `${n} olay birlikte`, title };
}

const COLS: ColumnDef<MLEvent>[] = [
  // v0.10.1845 — mockup görünüm 1 sırası: Sinyal · Servis · Ne oluyor · Seyir · Başladı · süre (+ durum) · Servis tipi.
  // v0.10.1874 — "Seyir" (≤ 24 nokta, olay anı kaydından; liste ucu tek toplu okuma) + "Servis tipi" (profil).
  // v0.10.2013 — "Grup" (anomali tekilleştirme D2): aynı servis + örtüşen pencere; yalnız yeni sütun.
  { id: 'signal', label: 'Sinyal', width: 90, sortValue: e => signalKindOf(e.detector, e.signal), naturalDir: 'asc' },
  { id: 'service', label: 'Servis', width: 180, sortValue: e => e.service, naturalDir: 'asc' },
  { id: 'headline', label: 'Ne oluyor', flex: true, minWidth: 280, sortValue: e => mlRowText(e), naturalDir: 'asc' },
  { id: 'trend', label: 'Seyir', width: 110, priority: 2, sortValue: e => sparkPeak(e) },
  { id: 'started', label: 'Başladı · süre', width: 170, sortValue: e => e.startedAt },
  { id: 'group', label: 'Grup', width: 150, minWidth: 110, priority: 2, sortValue: e => e.groupSize ?? 0 },
  { id: 'profile', label: 'Servis tipi', width: 110, priority: 2, sortValue: e => e.profileLabel ?? '', naturalDir: 'asc' },
  { id: 'detector', label: 'Dedektör', width: 150, priority: 3, sortValue: e => e.detector, naturalDir: 'asc' },
  { id: 'last', label: 'Son görülme', width: 150, priority: 3, sortValue: e => e.lastSeen },
  { id: 'fb', label: 'Geri bildirim', width: 120, priority: 2, sortValue: e => (e.verdict === 'anomaly' ? 2 : e.verdict ? 1 : 0) },
  // v0.10.1846 — mockup "Olayı izle": ▶ detayı açıp oynatır; satır tıkı oynatmadan açar.
  { id: 'play', label: 'Olayı izle', width: 100 },
];


export function MLTab({ signal = '', onSignal }: { signal?: MLSignal; onSignal?: (s: MLSignal) => void }) {
  const qc = useQueryClient();
  const ev = useMLEvents(signal);
  const sc = useMLScorecard();
  const [busyId, setBusyId] = useState<string | null>(null);
  const rows = ev.data?.items ?? [];

  const verdict = async (e: MLEvent, v: 'anomaly' | 'not_anomaly') => {
    const note = v === 'not_anomaly' ? (window.prompt('Not (isteğe bağlı): neden anomali değil?') ?? '') : '';
    setBusyId(e.id);
    try {
      await api.putMlVerdict(e.id, { verdict: v, note, detector: e.detector, service: e.service });
      await qc.invalidateQueries({ queryKey: keys.anomalies.ml });
    } finally {
      setBusyId(null);
    }
  };

  const renderCell = (e: MLEvent, col: ColumnDef<MLEvent>) => {
    switch (col.id) {
      case 'signal': return <SignalKindBadge kind={signalKindOf(e.detector, e.signal)} />;
      case 'headline': return <Link to={mlEventHref(e.id)} title={e.level || e.pod || e.code || ''}>{mlRowText(e)}</Link>;
      case 'service': return <Link to={serviceHref(e.service)} className="font-mono" title={e.service}>{e.service}</Link>;
      case 'detector': return ML_DETECTOR_TR[e.detector] ?? e.detector;
      case 'trend': return e.spark && e.spark.v.length > 1
        ? <Sparkline values={e.spark.v} width={96} height={22} color={SIGNAL_KINDS[signalKindOf(e.detector, e.signal)].token} markerAt={e.spark.a} title="Olayın seyri; çizgi = başlangıç" />
        : <span className="text-muted-foreground">—</span>;
      case 'group': {
        const g = mlGroupLabel(e);
        return g.text ? <span title={g.title}>{g.text}</span> : <span className="text-muted-foreground">—</span>;
      }
      case 'profile': return e.profileLabel ? <Badge variant="outline">{e.profileLabel}</Badge> : <span className="text-muted-foreground">—</span>;
      case 'started': return (
        <span className="inline-flex flex-wrap items-center gap-1.5">
          <span className="font-mono" title={fmtTs(e.startedAt)}>{fmtClock(new Date(e.startedAt / 1e6)).slice(0, 5)} · {durationLabel(e.lastSeen - e.startedAt)}</span>
          <Badge variant="outline" className={e.stage === 'closed' ? '' : 'border-transparent bg-[color:var(--warn-bg)] text-[color:var(--warn)]'}>{stageLabel(e.stage)}</Badge>
        </span>
      );
      case 'last': return <span className="font-mono">{fmtTs(e.lastSeen)}</span>;
      case 'play': return <Link to={mlEventPlayHref(e.id)} className={buttonVariants({ variant: 'outline', size: 'sm' })} aria-label={`${e.service} olayını oynat`}>▶ Oynat</Link>;
      case 'fb':
        if (e.verdict === 'anomaly') return <Badge variant="outline" title={`${e.verdictBy ?? ''}${e.verdictAt ? ` · ${fmtTs(e.verdictAt)}` : ''}`}>gerçek ✓</Badge>;
        if (e.verdict === 'not_anomaly') return <Badge variant="outline" title={`${e.verdictBy ?? ''}${e.note ? ` · ${e.note}` : ''}`}>değil ✗</Badge>;
        return <span className="text-muted-foreground">—</span>;
      default: return null;
    }
  };

  const state = ev.isLoading ? { kind: 'loading' as const }
    : ev.error ? { kind: 'error' as const, message: 'ML olayları okunamadı' }
      : { kind: 'empty' as const, message: 'Log ML olayı yok — dedektörler Settings › Anomaly › Log ML\'den açılır; olaylar gölge olarak buraya düşer, Problem üretmez.' };

  return (
    <div data-testid="ml-tab" className="flex flex-col gap-3">
      {onSignal && (
        <SegmentedControl<MLSignal> value={signal} onChange={onSignal} aria-label="Sinyal" size="sm"
          options={[{ value: '', label: 'Hepsi' }, { value: 'log', label: 'Log' }, { value: 'trace', label: 'Trace' }, { value: 'metric', label: 'Metrik' }]} />
      )}
      {sc.data && (() => {
        const sum = mlStatusSummary(sc.data, signal, Math.floor(Date.now() / 1000));
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
          <h3 className="mt-1 mb-0 font-semibold">Dedektörler</h3>
          <DataTable<DetectorRow>
            storageKey="anomalies-ml-detectors-v2" columns={DCOLS} rows={detectorRows(sc.data, signal)}
            state={{ kind: 'empty', message: 'Dedektör yok' }} sort={{ initial: { id: 'state', dir: 'desc' } }}
            getRowKey={r => r.detector} ariaLabel="Dedektörler"
            renderCell={(r, c) => {
              switch (c.id) {
                case 'detector': return ML_DETECTOR_TR[r.detector] ?? r.detector;
                case 'signal': return r.signal === 'log' || r.signal === 'trace' || r.signal === 'metric'
                  ? <SignalKindBadge kind={r.signal} /> : (SIGNAL_TR[r.signal] ?? r.signal);
                case 'state': return r.enabled ? 'Açık' : <span className="text-muted-foreground">Kapalı</span>;
                case 'events': return r.events.toLocaleString('tr-TR');
                case 'hit': {
                  const v = detectorVerdictText(r);
                  return v || <span className="text-muted-foreground" title="Bu dedektörün olaylarından henüz hiçbiri “gerçek / değil” diye işaretlenmedi">—</span>;
                }
                case 'gate': {
                  const g = detectorPromoteText(r);
                  return g.text ? <span title={g.title}>{g.text}</span> : <span className="text-muted-foreground" title={g.title}>—</span>;
                }
                default: return null;
              }
            }} />
          <p className="m-0 text-[length:var(--fs-sm)] text-muted-foreground">
            Dedektörler anomali kaydeder. Bir dedektörün kendiliğinden Problem açması için otomatik terfi anahtarının açık olması
            ve olaylarının yeterince değerlendirilip doğrulanmış olması gerekir (Settings › Anomaly). Ayrıntı “Teknik detay”da.
          </p>
          <details data-testid="ml-tech-details" className="rounded-md border border-border px-3 py-2">
            <summary className="cursor-pointer text-[length:var(--fs-sm)] font-semibold text-muted-foreground">Teknik detay</summary>
            <div className="mt-2 flex flex-col gap-3">
              <div className="grid gap-2 md:grid-cols-4" data-testid="ml-status-tiles">
                {statusTiles(sc.data, signal, Math.floor(Date.now() / 1000)).map(t => (
                  <div key={t.k} className="rounded-md border border-border bg-card px-3 py-2">
                    <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{t.k}</div>
                    <div className={`text-lg font-semibold ${t.warn ? 'text-[color:var(--warn)]' : ''}`}>{t.v}</div>
                    <div className="text-xs text-muted-foreground">{t.s}</div>
                  </div>
                ))}
              </div>
              {(sc.data.tick?.kinds || sc.data.baseline) && (
                <div className="flex flex-col gap-0.5 text-xs text-muted-foreground" data-testid="ml-kind-ticks">
                  {kindTickLines(sc.data.tick, signal).map(l => (
                    <div key={l.kind} className={l.warn ? 'text-[color:var(--warn)]' : ''}>{l.text}</div>
                  ))}
                  {(() => {
                    const b = baselineStatusLine(sc.data.baseline);
                    return b && <div data-testid="ml-baseline-line" className={b.warn ? 'text-[color:var(--warn)]' : ''}>{b.text}</div>;
                  })()}
                </div>
              )}
              <DataTable<DetectorRow>
                storageKey="anomalies-ml-detectors-tech" columns={DTECH} rows={detectorRows(sc.data, signal)}
                state={{ kind: 'empty', message: 'Dedektör yok' }} sort={{ initial: { id: 'detector', dir: 'asc' } }}
                getRowKey={r => r.detector} ariaLabel="Dedektörler, teknik detay"
                renderCell={(r, c) => {
                  switch (c.id) {
                    case 'detector': return ML_DETECTOR_TR[r.detector] ?? r.detector;
                    case 'tick': return r.tick ? `${(r.tick.sourceMs / 1000).toFixed(1)} sn` : '—';
                    case 'services': return r.tick ? r.tick.services : '—';
                    case 'candidates': return r.tick ? r.tick.candidates : '—';
                    case 'written': return r.tick ? r.tick.written : '—';
                    case 'gate': return r.gate?.ok ? 'sağlandı' : (r.gate?.reasons ?? []).join(' · ') || '—';
                    default: return null;
                  }
                }} />
            </div>
          </details>
        </>
      )}
      <h3 className="mt-2 font-semibold">Olaylar · son 24 saat</h3>
      <DataTable<MLEvent>
        storageKey="anomalies-ml" columns={COLS} rows={rows} state={state}
        getRowHref={e => mlEventHref(e.id)}
        sort={{ initial: { id: 'last', dir: 'desc' } }}
        getRowKey={e => e.id} renderCell={renderCell}
        rowActions={{
          label: 'Eylemler',
          triggerLabel: e => `${e.service} geri bildirim`,
          isDisabled: e => busyId === e.id,
          items: e => [
            { id: 'real', label: 'Gerçek anomali', onSelect: () => { void verdict(e, 'anomaly'); }, disabled: e.verdict === 'anomaly' },
            { id: 'not', label: 'Anomali değil (not ile)', onSelect: () => { void verdict(e, 'not_anomaly'); }, disabled: e.verdict === 'not_anomaly' },
          ],
        }} />
      {ev.data?.truncated && <p className="text-xs text-muted-foreground">Liste 200 satırda kesildi.</p>}
      <MLPeriodReplay signal={signal} />
    </div>
  );
}
