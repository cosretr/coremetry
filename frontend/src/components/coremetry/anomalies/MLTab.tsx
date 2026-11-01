// MLTab — /anomalies "Log ML" sekmesi (log ML dilim 0). components/coremetry
// altında çünkü Tailwind sınıfları yalnız burada üretiliyor (features/ değil).
// Eski dedektörler kendi sekmesinde; burada yalnız ml_events (gölge) +
// satır başına «gerçek / değil» + not (anomaly_verdicts 'ml:' önekli) ve
// üstte dedektör karnesi (30 gün isabet + örnek). Tablo: coremetry DataTable.
import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { Badge } from '@/components/shadcn/badge';
import { api } from '@/lib/api';
import { keys, useMLEvents, useMLScorecard } from '@/lib/queries';
import { fmtDateTime } from '@/lib/utils';
import type { MLEvent, MLDetectorScore, MLGate, MLBackfillProgress, MLTickStatus } from '@/lib/types';
import { MLReplaySheet } from './MLReplaySheet';
import { MLPeriodReplay } from './MLPeriodReplay';

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
  if (t.backoff.length) parts.push(`VM bekleme: ${t.backoff.map(b => `${b.family} (${Math.max(1, Math.round((b.until - nowSec) / 60))} dk)`).join(', ')}`);
  if (t.lastError) parts.push(`hata: ${t.lastError}`);
  return parts.join(' · ');
}

export type AnomaliesTab = 'detectors' | 'ml';
/** SAF: bilinmeyen değer → detectors (eski linkler bozulmaz). */
export function resolveAnomaliesTab(raw: string | null): AnomaliesTab {
  return raw === 'ml' ? 'ml' : 'detectors';
}

export function AnomaliesTabBar({ value, onChange }: { value: AnomaliesTab; onChange: (t: AnomaliesTab) => void }) {
  return (
    <div className="mb-3">
      <SegmentedControl<AnomaliesTab> value={value} onChange={onChange} aria-label="Anomali kaynağı"
        options={[{ value: 'detectors', label: 'Dedektörler' }, { value: 'ml', label: 'Log ML' }]} />
    </div>
  );
}

const fmtNum = (n: number) => (Number.isInteger(n) ? String(n) : n.toFixed(2));
const fmtTs = (ns: number) => fmtDateTime(new Date(ns / 1e6));

const COLS: ColumnDef<MLEvent>[] = [
  { id: 'signal', label: 'Sinyal', width: 70, sortValue: e => e.signal, naturalDir: 'asc' },
  { id: 'detector', label: 'Dedektör', width: 150, sortValue: e => e.detector, naturalDir: 'asc' },
  { id: 'service', label: 'Servis', width: 200, sortValue: e => e.service, naturalDir: 'asc' },
  { id: 'part', label: 'Bölüm', width: 160, sortValue: e => e.level || e.pod || e.code || '' },
  { id: 'observed', label: 'Gözlenen / taban', width: 150, numeric: true, sortValue: e => e.observed },
  { id: 'score', label: 'Skor', width: 80, numeric: true, sortValue: e => e.score },
  { id: 'started', label: 'Başlangıç', width: 150, sortValue: e => e.startedAt },
  { id: 'last', label: 'Son görülme', width: 150, sortValue: e => e.lastSeen },
  { id: 'stage', label: 'Aşama', width: 90, sortValue: e => e.stage },
  { id: 'fb', label: 'Geri bildirim', width: 200, sortValue: e => (e.verdict === 'anomaly' ? 2 : e.verdict ? 1 : 0) },
];

export function MLTab({ signal = '', onSignal }: { signal?: MLSignal; onSignal?: (s: MLSignal) => void }) {
  const qc = useQueryClient();
  const ev = useMLEvents(signal);
  const sc = useMLScorecard();
  const [busyId, setBusyId] = useState<string | null>(null);
  const rows = ev.data?.items ?? [];
  // Replay seçimi URL'de (paylaşılabilir; replace:true).
  const [params, setParams] = useSearchParams();
  const replayId = params.get('replay');
  const setReplay = (id: string | null) => setParams(p => {
    const n = new URLSearchParams(p);
    if (id) n.set('replay', id); else n.delete('replay');
    return n;
  }, { replace: true });

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
      case 'signal': return e.signal === 'trace' ? 'Trace' : e.signal === 'metric' ? 'Metrik' : 'Log';
      case 'detector': return ML_DETECTOR_TR[e.detector] ?? e.detector;
      case 'service': return <span className="font-mono" title={e.service}>{e.service}</span>;
      case 'part': return <span className="font-mono">{e.level || e.pod || e.code || '—'}</span>;
      case 'observed': return `${fmtNum(e.observed)} / ${fmtNum(e.baseline)} ${e.direction === 'down' ? '↓' : '↑'}`;
      case 'score': return e.score.toFixed(1);
      case 'started': return <span className="font-mono">{fmtTs(e.startedAt)}</span>;
      case 'last': return <span className="font-mono">{fmtTs(e.lastSeen)}</span>;
      case 'stage': return <Badge variant="outline">{e.stage === 'promoted' ? 'terfi' : 'gölge'}</Badge>;
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
      <div className="text-sm text-muted-foreground">
        <b>Dedektör karnesi · son {sc.data?.windowDays ?? 30} gün</b>
        {sc.isLoading && <span> · yükleniyor…</span>}
        {sc.data && sc.data.detectors.length === 0 && <span> · henüz olay yok</span>}
        {(sc.data?.backfill ?? []).filter(b => !signal || b.signal === signal).map(b => <span key={b.signal} data-testid="ml-backfill"> · {backfillLabel(b)}</span>)}
        {sc.data && <div className="mt-1" data-testid="ml-tick" title={sc.data.tick?.lastError}>{tickLabel(sc.data.tick, Math.floor(Date.now() / 1000))}</div>}
        {sc.data && (sc.data.bySignal?.length ?? 0) > 0 && (
          <ul className="mt-1 flex flex-wrap gap-x-4 gap-y-1" data-testid="ml-signal-score">
            {(sc.data.bySignal ?? []).filter(s => !signal || s.detector === signal).map(s => <li key={s.detector}><b>{signalScoreLabel(s)}</b></li>)}
          </ul>
        )}
        {sc.data && sc.data.detectors.length > 0 && (
          <ul className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
            {sc.data.detectors.filter((s: MLDetectorScore) => !signal || (sc.data?.signals[s.detector] ?? 'log') === signal).map((s: MLDetectorScore) => (
              <li key={s.detector} title={gateLabel(sc.data?.gates?.[s.detector])}>{scoreLabel(s)}{sc.data?.promotion[s.detector]?.enabled ? ' · terfi açık' : ''}</li>
            ))}
          </ul>
        )}
      </div>
      <DataTable<MLEvent>
        storageKey="anomalies-ml" columns={COLS} rows={rows} state={state}
        sort={{ initial: { id: 'last', dir: 'desc' } }}
        getRowKey={e => e.id} renderCell={renderCell}
        rowActions={{
          label: 'Eylemler',
          triggerLabel: e => `${e.service} geri bildirim`,
          isDisabled: e => busyId === e.id,
          items: e => [
            { id: 'replay', label: 'Replay', onSelect: () => setReplay(e.id) },
            { id: 'real', label: 'Gerçek anomali', onSelect: () => { void verdict(e, 'anomaly'); }, disabled: e.verdict === 'anomaly' },
            { id: 'not', label: 'Anomali değil (not ile)', onSelect: () => { void verdict(e, 'not_anomaly'); }, disabled: e.verdict === 'not_anomaly' },
          ],
        }} />
      {ev.data?.truncated && <p className="text-xs text-muted-foreground">Liste 200 satırda kesildi.</p>}
      <MLPeriodReplay signal={signal} />
      <MLReplaySheet id={replayId} onClose={() => setReplay(null)} />
    </div>
  );
}
