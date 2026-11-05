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
import { fmtDateTime } from '@/lib/utils';
import type { MLEvent, MLDetectorScore, MLGate, MLBackfillProgress, MLTickStatus, MLSignalTick, MLScorecard } from '@/lib/types';
import { Link } from 'react-router-dom';
import { mlHeadline, mlValuePair } from '@/lib/mlReplay';
import { MLPeriodReplay } from './MLPeriodReplay';
import { mlEventHref } from '@/lib/anomalyHref';

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

const SIGNAL_TR: Record<string, string> = { log: 'Log', trace: 'Trace', metric: 'Metrik' };

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
    { k: 'Son tik', v: t ? `${ago} dk önce` : 'yok', s: t ? `${Math.round(t.durationMs / 1000)} sn · ${t.events} olay${t.docs ? ` · ${t.docs.toLocaleString('tr-TR')} belge` : ''}${t.partial ? ` · ${t.partial} kısmi` : ''}` : 'tik yok', warn: !!t?.lastError },
    { k: 'Log geçmişi', v: bf ? `${bf.done + miss} / ${bf.total} sa` : '—', s: bf ? (bf.complete ? 'tamam' : bf.running ? 'sürüyor' : 'duraklı') + (miss ? ` · eksik ${miss}` : '') : 'kayıt yok' },
    { k: 'Olay · 30 gün', v: String(events), s: samples ? `${samples} karar` : 'karar yok · terfi kapalı' },
    { k: 'VM bekleme', v: bo.length ? `${bo.length} aile` : 'yok', s: bo.map(b => `${b.family} · ${Math.max(1, Math.round((b.until - nowSec) / 60))} dk`).join(', ') || 'tüm aileler açık', warn: bo.length > 0 },
  ];
}

const DCOLS: ColumnDef<DetectorRow>[] = [
  { id: 'detector', label: 'Dedektör', width: 170, sortValue: r => r.detector, naturalDir: 'asc' },
  { id: 'signal', label: 'Sinyal', width: 70, sortValue: r => r.signal, naturalDir: 'asc' },
  { id: 'state', label: 'Durum', width: 80, sortValue: r => (r.enabled ? 1 : 0) },
  { id: 'tick', label: 'Son tik', width: 80, numeric: true, sortValue: r => r.tick?.sourceMs ?? -1 },
  { id: 'services', label: 'Servis', width: 70, numeric: true, sortValue: r => r.tick?.services ?? -1 },
  { id: 'candidates', label: 'Aday', width: 60, numeric: true, sortValue: r => r.tick?.candidates ?? -1 },
  { id: 'written', label: 'Yazıldı', width: 70, numeric: true, sortValue: r => r.tick?.written ?? -1 },
  { id: 'events', label: 'Olay 30g', width: 80, numeric: true, sortValue: r => r.events },
  { id: 'hit', label: 'İsabet', width: 100, numeric: true, sortValue: r => r.hitRate },
  { id: 'gate', label: 'Terfi kapısı', flex: true, minWidth: 180, sortValue: r => (r.gate?.ok ? 0 : 1) },
];

export type AnomaliesTab = 'detectors' | 'ml';
/** SAF: bilinmeyen değer → detectors (eski linkler bozulmaz). */
export function resolveAnomaliesTab(raw: string | null): AnomaliesTab {
  return raw === 'ml' ? 'ml' : 'detectors';
}

export function AnomaliesTabBar({ value, onChange }: { value: AnomaliesTab; onChange: (t: AnomaliesTab) => void }) {
  return (
    <div className="mb-3">
      <SegmentedControl<AnomaliesTab> value={value} onChange={onChange} aria-label="Anomali kaynağı"
        options={[{ value: 'detectors', label: 'Dedektörler' }, { value: 'ml', label: 'ML' }]} />
    </div>
  );
}

const fmtNum = (n: number) => (Number.isInteger(n) ? String(n) : n.toFixed(2));
const fmtTs = (ns: number) => fmtDateTime(new Date(ns / 1e6));

const COLS: ColumnDef<MLEvent>[] = [
  // v0.10.1779 (operatör mockup): Durum, Süre, insan dili Olay (gerçek link), Servis, Dedektör, birimli değer.
  { id: 'stage', label: 'Durum', width: 90, sortValue: e => e.stage, naturalDir: 'asc' },
  { id: 'duration', label: 'Süre', width: 80, numeric: true, sortValue: e => e.lastSeen - e.startedAt },
  { id: 'headline', label: 'Olay', flex: true, minWidth: 240, sortValue: e => mlHeadline(e) , naturalDir: 'asc' },
  { id: 'service', label: 'Servis', width: 200, sortValue: e => e.service, naturalDir: 'asc' },
  { id: 'detector', label: 'Dedektör', width: 150, sortValue: e => e.detector, naturalDir: 'asc' },
  { id: 'observed', label: 'Gözlenen / taban', width: 150, numeric: true, sortValue: e => e.observed },
  { id: 'started', label: 'Başlangıç', width: 150, sortValue: e => e.startedAt },
  { id: 'last', label: 'Son görülme', width: 150, sortValue: e => e.lastSeen },
  { id: 'fb', label: 'Geri bildirim', width: 130, sortValue: e => (e.verdict === 'anomaly' ? 2 : e.verdict ? 1 : 0) },
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
      case 'stage': return <Badge variant="outline">{stageLabel(e.stage)}</Badge>;
      case 'duration': return durationLabel(e.lastSeen - e.startedAt);
      case 'headline': return <Link to={mlEventHref(e.id)} className="font-semibold" title={e.level || e.pod || e.code || ''}>{mlHeadline(e)}</Link>;
      case 'service': return <span className="font-mono" title={e.service}>{e.service}</span>;
      case 'detector': return ML_DETECTOR_TR[e.detector] ?? e.detector;
      case 'observed': return mlValuePair(e, e.windowSec || 300);
      case 'started': return <span className="font-mono">{fmtTs(e.startedAt)}</span>;
      case 'last': return <span className="font-mono">{fmtTs(e.lastSeen)}</span>;
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
      {sc.data && (
        <div className="grid gap-2 md:grid-cols-4" data-testid="ml-status-tiles">
          {statusTiles(sc.data, signal, Math.floor(Date.now() / 1000)).map(t => (
            <div key={t.k} className="rounded-md border border-border bg-card px-3 py-2">
              <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{t.k}</div>
              <div className={`text-lg font-semibold ${t.warn ? 'text-[color:var(--warn)]' : ''}`}>{t.v}</div>
              <div className="text-xs text-muted-foreground">{t.s}</div>
            </div>
          ))}
        </div>
      )}
      {sc.data && (
        <DataTable<DetectorRow>
          storageKey="anomalies-ml-detectors" columns={DCOLS} rows={detectorRows(sc.data, signal)}
          state={{ kind: 'empty', message: 'Dedektör yok' }} sort={{ initial: { id: 'events', dir: 'desc' } }}
          getRowKey={r => r.detector}
          renderCell={(r, c) => {
            switch (c.id) {
              case 'detector': return ML_DETECTOR_TR[r.detector] ?? r.detector;
              case 'signal': return SIGNAL_TR[r.signal] ?? r.signal;
              case 'state': return <Badge variant="outline">{r.enabled ? 'açık' : 'kapalı'}</Badge>;
              case 'tick': return r.tick ? `${(r.tick.sourceMs / 1000).toFixed(1)} sn` : '—';
              case 'services': return r.tick ? r.tick.services : '—';
              case 'candidates': return r.tick ? r.tick.candidates : '—';
              case 'written': return r.tick ? r.tick.written : '—';
              case 'events': return r.events;
              case 'hit': return r.samples > 0 ? `%${Math.round(r.hitRate * 100)} (${r.samples})` : 'karar yok';
              case 'gate': return r.gate?.ok ? 'geçildi' : (r.gate?.reasons ?? []).join(' · ') || '—';
              default: return null;
            }
          }} />
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
