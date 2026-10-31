// LogMLTab — /anomalies "Log ML" sekmesi (log ML dilim 0). components/coremetry
// altında çünkü Tailwind sınıfları yalnız burada üretiliyor (features/ değil).
// Eski dedektörler kendi sekmesinde; burada yalnız log_ml_events (gölge) +
// satır başına «gerçek / değil» + not (anomaly_verdicts 'logml:' önekli) ve
// üstte dedektör karnesi (30 gün isabet + örnek). Tablo: coremetry DataTable.
import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { Badge } from '@/components/shadcn/badge';
import { api } from '@/lib/api';
import { keys, useLogMLEvents, useLogMLScorecard } from '@/lib/queries';
import { fmtDateTime } from '@/lib/utils';
import type { LogMLEvent, LogMLDetectorScore } from '@/lib/types';

export const LOG_ML_DETECTOR_TR: Record<string, string> = {
  log_volume: 'Log hacmi', error_rate: 'Hata oranı', service_silent: 'Servis sustu',
  template_spike: 'Şablon spike', rare_template: 'Nadir şablon', rare_error_code: 'Nadir hata kodu',
  pod_outlier: 'Aykırı pod', change_point: 'Kırılma',
};

/** Karne satırı metni — SAF (test): örnek yoksa isabet yazılmaz (S4). */
export function scoreLabel(s: LogMLDetectorScore): string {
  const name = LOG_ML_DETECTOR_TR[s.detector] ?? s.detector;
  if (s.samples <= 0) return `${name}: ${s.events} olay · karar yok`;
  return `${name}: ${s.events} olay · %${Math.round(s.hitRate * 100)} isabet (${s.samples} karar)`;
}

export type AnomaliesTab = 'detectors' | 'logml';
/** SAF: bilinmeyen değer → detectors (eski linkler bozulmaz). */
export function resolveAnomaliesTab(raw: string | null): AnomaliesTab {
  return raw === 'logml' ? 'logml' : 'detectors';
}

export function AnomaliesTabBar({ value, onChange }: { value: AnomaliesTab; onChange: (t: AnomaliesTab) => void }) {
  return (
    <div className="mb-3">
      <SegmentedControl<AnomaliesTab> value={value} onChange={onChange} aria-label="Anomali kaynağı"
        options={[{ value: 'detectors', label: 'Dedektörler' }, { value: 'logml', label: 'Log ML' }]} />
    </div>
  );
}

const fmtNum = (n: number) => (Number.isInteger(n) ? String(n) : n.toFixed(2));
const fmtTs = (ns: number) => fmtDateTime(new Date(ns / 1e6));

const COLS: ColumnDef<LogMLEvent>[] = [
  { id: 'detector', label: 'Dedektör', width: 130, sortValue: e => e.detector, naturalDir: 'asc' },
  { id: 'service', label: 'Servis', width: 200, sortValue: e => e.service, naturalDir: 'asc' },
  { id: 'part', label: 'Bölüm', width: 160, sortValue: e => e.level || e.pod || e.code || '' },
  { id: 'observed', label: 'Gözlenen / taban', width: 150, numeric: true, sortValue: e => e.observed },
  { id: 'score', label: 'Skor', width: 80, numeric: true, sortValue: e => e.score },
  { id: 'started', label: 'Başlangıç', width: 150, sortValue: e => e.startedAt },
  { id: 'last', label: 'Son görülme', width: 150, sortValue: e => e.lastSeen },
  { id: 'stage', label: 'Aşama', width: 90, sortValue: e => e.stage },
  { id: 'fb', label: 'Geri bildirim', width: 200, sortValue: e => (e.verdict === 'anomaly' ? 2 : e.verdict ? 1 : 0) },
];

export function LogMLTab() {
  const qc = useQueryClient();
  const ev = useLogMLEvents();
  const sc = useLogMLScorecard();
  const [busyId, setBusyId] = useState<string | null>(null);
  const rows = ev.data?.items ?? [];

  const verdict = async (e: LogMLEvent, v: 'anomaly' | 'not_anomaly') => {
    const note = v === 'not_anomaly' ? (window.prompt('Not (isteğe bağlı): neden anomali değil?') ?? '') : '';
    setBusyId(e.id);
    try {
      await api.putLogMlVerdict(e.id, { verdict: v, note, detector: e.detector, service: e.service });
      await qc.invalidateQueries({ queryKey: keys.anomalies.logMl });
    } finally {
      setBusyId(null);
    }
  };

  const renderCell = (e: LogMLEvent, col: ColumnDef<LogMLEvent>) => {
    switch (col.id) {
      case 'detector': return LOG_ML_DETECTOR_TR[e.detector] ?? e.detector;
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
    : ev.error ? { kind: 'error' as const, message: 'Log ML olayları okunamadı' }
      : { kind: 'empty' as const, message: 'Log ML olayı yok — dedektörler Settings › Anomaly › Log ML\'den açılır; olaylar gölge olarak buraya düşer, Problem üretmez.' };

  return (
    <div data-testid="logml-tab" className="flex flex-col gap-3">
      <div className="text-sm text-muted-foreground">
        <b>Dedektör karnesi · son {sc.data?.windowDays ?? 30} gün</b>
        {sc.isLoading && <span> · yükleniyor…</span>}
        {sc.data && sc.data.detectors.length === 0 && <span> · henüz olay yok</span>}
        {sc.data && sc.data.detectors.length > 0 && (
          <ul className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
            {sc.data.detectors.map(s => <li key={s.detector}>{scoreLabel(s)}{sc.data?.promotion[s.detector]?.enabled ? ' · terfi açık' : ''}</li>)}
          </ul>
        )}
      </div>
      <DataTable<LogMLEvent>
        storageKey="anomalies-logml" columns={COLS} rows={rows} state={state}
        sort={{ initial: { id: 'last', dir: 'desc' } }}
        getRowKey={e => e.id} renderCell={renderCell}
        rowActions={{
          label: 'Geri bildirim',
          triggerLabel: e => `${e.service} geri bildirim`,
          isDisabled: e => busyId === e.id,
          items: e => [
            { id: 'real', label: 'Gerçek anomali', onSelect: () => { void verdict(e, 'anomaly'); }, disabled: e.verdict === 'anomaly' },
            { id: 'not', label: 'Anomali değil (not ile)', onSelect: () => { void verdict(e, 'not_anomaly'); }, disabled: e.verdict === 'not_anomaly' },
          ],
        }} />
      {ev.data?.truncated && <p className="text-xs text-muted-foreground">Liste 200 satırda kesildi.</p>}
    </div>
  );
}
