// MLPeriodReplay — dönem replay'i (v0.10.1753): servis + son N gün için
// backtest motorunu UI'dan koşar (POST /api/ml/backtest, salt hesap).
// Seçim URL'de (?bt=<servis>&btDays=, replace); sonuç tekrar koşulana dek
// bellekte. Dedektör özeti + isabet listesi (ilk 100; >100 satır kuralı).
import { useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { Field } from '@/components/coremetry/Field';
import { Button } from '@/components/shadcn/button';
import { ServicePicker } from '@/components/ServicePicker';
import { api } from '@/lib/api';
import { fmtDateTime } from '@/lib/utils';
import type { MLBacktestDetector, MLBacktestHit, MLBacktestResult } from '@/lib/types';
import { ML_DETECTOR_TR, type MLSignal } from './MLTab';

const HIT_SHOW = 100;
const pct = (x: number) => `%${Math.round(x * 100)}`;
const fmtSec = (s: number) => fmtDateTime(new Date(s * 1000));

/** SAF (test): ?btDays= çözümü, 1..14 (varsayılan 7). */
export function resolveBacktestDays(raw: string | null): number {
  const n = Number(raw);
  return Number.isInteger(n) && n >= 1 && n <= 14 ? n : 7;
}

const DCOLS: ColumnDef<MLBacktestDetector>[] = [
  { id: 'detector', label: 'Dedektör', width: 170, sortValue: d => d.detector, naturalDir: 'asc' },
  { id: 'fired', label: 'İsabet', width: 80, numeric: true, sortValue: d => d.fired },
  { id: 'tp', label: 'Gerçek', width: 80, numeric: true, sortValue: d => d.tp },
  { id: 'fp', label: 'Değil', width: 80, numeric: true, sortValue: d => d.fp },
  { id: 'unl', label: 'Etiketsiz', width: 90, numeric: true, sortValue: d => d.unlabeled },
  { id: 'prec', label: 'İsabet oranı', width: 100, numeric: true, sortValue: d => d.precision },
  { id: 'rec', label: 'Yakalama', width: 100, numeric: true, sortValue: d => d.recall },
];
const HCOLS: ColumnDef<MLBacktestHit>[] = [
  { id: 'from', label: 'Başlangıç', width: 150, sortValue: h => h.from },
  { id: 'to', label: 'Bitiş', width: 150, sortValue: h => h.to },
  { id: 'detector', label: 'Dedektör', width: 160, sortValue: h => h.detector, naturalDir: 'asc' },
  { id: 'part', label: 'Bölüm', width: 180, sortValue: h => h.part },
  { id: 'z', label: 'Tepe z', width: 80, numeric: true, sortValue: h => h.peakZ },
  { id: 'label', label: 'Etiket', width: 100, sortValue: h => h.label ?? '' },
];

export function MLPeriodReplay({ signal }: { signal: MLSignal }) {
  const [params, setParams] = useSearchParams();
  const service = params.get('bt') ?? '';
  const days = resolveBacktestDays(params.get('btDays'));
  const [res, setRes] = useState<MLBacktestResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const sig = signal || 'trace';
  const set = (k: string, v: string) => setParams(p => {
    const n = new URLSearchParams(p);
    if (v) n.set(k, v); else n.delete(k);
    return n;
  }, { replace: true });

  const run = async () => {
    setBusy(true); setErr('');
    try {
      const to = Math.floor(Date.now() / 1000);
      setRes(await api.mlBacktest({ signal: sig, service, from: to - days * 86400, to }));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section data-testid="ml-period-replay" className="mt-2">
      <h3 className="mb-1 font-semibold">Dönem replay&apos;i</h3>
      <p className="mb-2 text-xs text-muted-foreground">Seçilen servisin son günleri kayıtlı seri üzerinde bugünkü ayarla yeniden değerlendirilir ({sig} sinyali; üstteki sinyal seçimi değiştirir). Etiketler tüm sinyallerin ortak kararlarıdır.</p>
      <div className="flex flex-wrap items-end gap-2">
        <ServicePicker value={service} onChange={v => set('bt', v)} placeholder="Servis" width={260} />
        <Field type="number" label="Gün (1–14)" small value={String(days)} onChange={e => set('btDays', e.target.value)} />
        <Button size="sm" variant="outline" disabled={!service} loading={busy} onClick={() => { void run(); }}>Çalıştır</Button>
      </div>
      {err && <p className="mt-1 text-xs">{err}</p>}
      {res && (
        <div className="mt-2 flex flex-col gap-2">
          <p className="text-xs text-muted-foreground">{res.series} seri · {res.buckets} kova · config {res.configHash}</p>
          <DataTable<MLBacktestDetector> storageKey="ml-backtest-detectors" columns={DCOLS} rows={res.detectors}
            state={{ kind: 'empty', message: 'Bu dönemde isabet yok.' }} sort={{ initial: { id: 'fired', dir: 'desc' } }}
            getRowKey={d => d.detector}
            renderCell={(d, c) => {
              switch (c.id) {
                case 'detector': return ML_DETECTOR_TR[d.detector] ?? d.detector;
                case 'fired': return d.fired;
                case 'tp': return d.tp;
                case 'fp': return d.fp;
                case 'unl': return d.unlabeled;
                case 'prec': return d.tp + d.fp > 0 ? pct(d.precision) : '—';
                case 'rec': return d.positives > 0 ? `${pct(d.recall)} (${d.covered}/${d.positives})` : '—';
                default: return null;
              }
            }} />
          {res.hits.length > 0 && (
            <DataTable<MLBacktestHit> storageKey="ml-backtest-hits" columns={HCOLS} rows={res.hits.slice(0, HIT_SHOW)}
              state={{ kind: 'empty', message: '' }} sort={{ initial: { id: 'from', dir: 'desc' } }}
              getRowKey={h => `${h.detector}:${h.part}:${h.from}`}
              renderCell={(h, c) => {
                switch (c.id) {
                  case 'from': return <span className="font-mono">{fmtSec(h.from)}</span>;
                  case 'to': return <span className="font-mono">{fmtSec(h.to)}</span>;
                  case 'detector': return ML_DETECTOR_TR[h.detector] ?? h.detector;
                  case 'part': return <span className="font-mono">{h.part}</span>;
                  case 'z': return h.peakZ.toFixed(1);
                  case 'label': return h.label === 'anomaly' ? 'gerçek' : h.label === 'not_anomaly' ? 'değil' : '—';
                  default: return null;
                }
              }} />
          )}
          {(res.hits.length > HIT_SHOW || res.truncated) && <p className="text-xs text-muted-foreground">İlk {HIT_SHOW} isabet gösteriliyor.</p>}
        </div>
      )}
    </section>
  );
}
