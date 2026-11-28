// ImpactReplay — v0.10.2392: etki kuralı geri oynatma (GET /api/ml/impact-replay,
// api/ml_impact_replay.go). Anomalies › ML › Teknik detay içinde, Davis ↔ ML
// karşılaştırmasının yanında kapalı bölüm; YALNIZ admin (operatör aracı,
// MLTab kapıyı tutar) ve tembel parça (bundle). Seçim (gün, dedektör) +
// "Oynat" düğmesi: istek yalnız düğmeyle, yoklama yok. Admin ekranı:
// çevrilmez (proje kuralı). Yaklaşıklık uyarısı her zaman görünür.
import { useState } from 'react';
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { Button } from '@/components/shadcn/button';
import { Empty, Spinner } from '@/components/Spinner';
import { useImpactReplay } from '@/lib/queries';
import type { ImpactReplayDetector, ImpactReplayEpisode, ImpactReplayResult } from '@/lib/types';
import { fmtDateTime, fmtDurShort } from '@/lib/utils';
import { SearchX } from 'lucide-react';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

type Days = '1' | '3' | '7' | '14';
const DAY_OPTS: { value: Days; label: string }[] = [
  { value: '1', label: '1 gün' }, { value: '3', label: '3 gün' }, { value: '7', label: '7 gün' }, { value: '14', label: '14 gün' },
];
const DET_OPTS: { value: ImpactReplayDetector; label: string }[] = [
  { value: 'failure_rate', label: 'Hata oranı' }, { value: 'response_time', label: 'Yanıt süresi' }, { value: 'consumer_lag', label: 'Consumer lag' },
];
const MEASURE_TR: Record<string, string> = {
  failure_rate: 'hata oranı', response_median: 'medyan yanıt', response_slow: 'en yavaş %10', consumer_lag: 'consumer lag',
};

/** SAF (test): ölçünün değeri biriminde. */
export function replayValue(measure: string, v: number): string {
  if (measure === 'failure_rate') return `%${(v * 100).toLocaleString('tr-TR', { maximumFractionDigits: 2 })}`;
  if (measure === 'response_median' || measure === 'response_slow') return `${Math.round(v).toLocaleString('tr-TR')} ms`;
  return Math.round(v).toLocaleString('tr-TR');
}

/** SAF (test): özet cümlesi. */
export function replaySummary(d: ImpactReplayResult): string {
  const t = d.totals;
  if (t.problems === 0) return `Son ${d.days} günde bu kural Problem açmazdı (${d.candidates} aday servis oynatıldı).`;
  const p3 = t.p3 ? `, P3 ${t.p3}` : '';
  return `Son ${d.days} günde ${t.problems} Problem açardı (P1 ${t.p1}, P2 ${t.p2}${p3}) · ${t.services} servis · medyan süre ${fmtDurShort(t.medianSec)} · ${d.candidates} aday oynatıldı.`;
}

const prioRank: Record<string, number> = { P1: 3, P2: 2, P3: 1 };

const COLS: ColumnDef<ImpactReplayEpisode>[] = [
  { id: 'service', label: 'Servis', width: 200, minWidth: 120, flex: true, priority: 1, sortValue: r => r.service, naturalDir: 'asc' },
  { id: 'measure', label: 'Ölçü', width: 130, priority: 2, sortValue: r => r.measure, naturalDir: 'asc' },
  { id: 'start', label: 'Başlangıç', width: 130, priority: 1, numeric: true, sortValue: r => r.start },
  { id: 'duration', label: 'Süre', width: 90, priority: 1, numeric: true, sortValue: r => r.durationSec },
  { id: 'peak', label: 'Tepe / olağan', width: 170, priority: 1, numeric: true, sortValue: r => (r.normal > 0 ? r.peak / r.normal : r.peak) },
  { id: 'priority', label: 'Öncelik', width: 80, priority: 1, sortValue: r => prioRank[r.priority] ?? 0 },
];

export default function ImpactReplaySection() {
  const [days, setDays] = useState<Days>('7');
  const [det, setDet] = useState<ImpactReplayDetector>('failure_rate');
  const [req, setReq] = useState<{ days: number; detector: string } | null>(null);
  const q = useImpactReplay(req);
  const d = q.data;
  return (
    <details data-testid="impact-replay" className="rounded-md border border-border px-3 py-2">
      <summary className="cursor-pointer text-[length:var(--fs-sm)] font-semibold text-muted-foreground">
        Etki kuralı geri oynatma
      </summary>
      <div className="mt-2 flex flex-col gap-2">
        <p className="m-0 text-xs text-muted-foreground">
          Seçilen etki kuralı son N günde hangi serviste, ne zaman, ne kadar ateşlerdi ve kaç Problem açardı.
          Yaklaşık: kuralın 1 dakikalık örnekleri saklanmadığı için dedektörün 5 dakikalık serisi aynı kuralla yargılanır;
          5 dakikadan kısa olaylar eksik sayılabilir.
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <SegmentedControl<Days> value={days} onChange={setDays} options={DAY_OPTS} aria-label="Gün" size="sm" />
          <SegmentedControl<ImpactReplayDetector> value={det} onChange={setDet} options={DET_OPTS} aria-label="Dedektör" size="sm" />
          <Button size="sm" variant="outline" loading={q.isFetching}
            onClick={() => setReq({ days: Number(days), detector: det })}>Oynat</Button>
        </div>
        {q.isLoading && <Spinner />}
        {q.error && <Empty icon={<SearchX size={28} strokeWidth={1.5} />} title="Oynatma alınamadı">Bir süre sonra yeniden deneyin.</Empty>}
        {d && (
          <>
            <p className="m-0 text-sm" data-testid="impact-replay-summary">{replaySummary(d)}</p>
            {d.notes.length > 0 && (
              <ul className="m-0 flex list-none flex-col gap-0.5 p-0 text-xs text-muted-foreground" data-testid="impact-replay-notes">
                {d.notes.map(n => <li key={n}>{n}</li>)}
              </ul>
            )}
            {d.skippedTotal > 0 && (
              <p className="m-0 text-xs text-muted-foreground" data-testid="impact-replay-skipped"
                title={d.skipped.map(s => `${s.service}${s.subject ? ` (${s.subject})` : ''}: ${s.reason}`).join('\n')}>
                {`${d.skippedTotal} aday tabanı yetersiz olduğu için yargılanamadı: ${d.skipped.slice(0, 5).map(s => s.service).join(', ')}${d.skippedTotal > 5 ? '…' : ''}`}
              </p>
            )}
            <DataTable<ImpactReplayEpisode>
              storageKey="anomalies-impact-replay" columns={COLS} rows={d.episodes} contentVisibility={d.episodes.length > 100}
              state={{ kind: 'empty', message: 'Bu pencerede kural hiç ateşlemezdi.' }} sort={{ initial: { id: 'start', dir: 'desc' } }}
              getRowKey={r => `${r.service}|${r.subject ?? ''}|${r.start}`} ariaLabel="Geri oynatma epizotları"
              renderCell={(r, c) => {
                switch (c.id) {
                  case 'service': return (
                    <EntityLink to={{ kind: 'service', name: r.service, opts: { range: { fromNs: (r.start - 1800) * 1e9, toNs: (r.end + 1800) * 1e9 } } }} title={r.subject || r.service}>
                      {r.subject ? `${r.service} · ${r.subject.split('#')[1] ?? r.subject}` : r.service}
                    </EntityLink>
                  );
                  case 'measure': return MEASURE_TR[r.measure] ?? r.measure;
                  case 'start': return fmtDateTime(r.start * 1000);
                  case 'duration': return `${fmtDurShort(r.durationSec)}${r.open ? ' (sürüyor)' : ''}`;
                  case 'peak': return (
                    <span title={`eşik ${replayValue(r.measure, r.threshold)}`}>
                      <span className="cell-err">{replayValue(r.measure, r.peak)}</span>{` / ${replayValue(r.measure, r.normal)}`}
                    </span>
                  );
                  case 'priority': return <span title={r.priorityReason} className={r.priority === 'P1' ? 'cell-err' : undefined}>{r.priority}</span>;
                  default: return null;
                }
              }} />
          </>
        )}
      </div>
    </details>
  );
}
