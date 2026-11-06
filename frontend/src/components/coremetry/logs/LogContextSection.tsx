// coremetry/logs/LogContextSection — v0.10.1881 (Log Search Faz 3 dilim 3.2; audit
// §8 "Çevre loglar sheet içinde (uç değişmez)", mockup görünüm 2 "Çevre loglar").
// Satır detayı çekmecesinin bölümü: pivot kaydın önceki / sonraki satırları,
// kapsam "Aynı pod" / "Aynı servis". Uç DEĞİŞMEDİ: GET /api/logs/context
// (iki paralel arama, sunucu 15 sn önbellek, n ≤ 200).
//
// ES tutumluluğu: bölüm KAPALI başlar; istek yalnız operatör açınca gider.
// Genişlik BASAMAKLI (±5 → ±50 → ±200; önbellek anahtarı kardinalitesi sınırlı),
// React Query sinyali (kapanış / kapsam değişimi uçuştaki aramayı keser),
// staleTime = sunucu TTL. Satıra tık o kaydı çekmecede açar. Saf sunum + tek
// sorgu; ui/ importu ve satır içi stil yok.
import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ChevronRight } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { api } from '@/lib/api';
import { podOfLog } from '@/lib/logPod';
import { firstLine, severityLabel, severityStatus } from '@/lib/logDetail';
import { fmtClock } from '@/lib/utils';
import type { LogRow } from '@/lib/types';

export const CONTEXT_RUNGS = [5, 50, 200] as const;
type Rung = typeof CONTEXT_RUNGS[number];
type Scope = 'pod' | 'service';

export interface LogContextSectionProps {
  pivot: LogRow;
  env?: string;
  /** Satıra tık → o kaydı çekmecede aç. */
  onOpenRow?(row: LogRow): void;
  /** "Bu pencereyi Logs'ta aç" — dönen satırların penceresi (ns) + servis. */
  onOpenWindow?(fromNs: number, toNs: number, service: string): void;
  /** Başlangıçta açık mı (testler / derin link). */
  defaultOpen?: boolean;
}

const clock = (ns: number) => {
  const ms = ns / 1e6;
  return `${fmtClock(ms)}.${String(Math.floor(ms % 1000)).padStart(3, '0')}`;
};

export function LogContextSection({ pivot, env, onOpenRow, onOpenWindow, defaultOpen = false }: LogContextSectionProps) {
  const pod = podOfLog(pivot);
  const [open, setOpen] = useState(defaultOpen);
  const [scope, setScope] = useState<Scope>(pod ? 'pod' : 'service');
  const [n, setN] = useState<Rung>(CONTEXT_RUNGS[0]);
  const svc = pivot.serviceName || undefined;
  const q = useQuery({
    queryKey: ['logs', 'context', pivot.timestamp, svc, env, scope === 'pod' ? pod : '', n],
    queryFn: ({ signal }) => api.logsContext({ ts: pivot.timestamp, service: svc, env: env || undefined, pod: scope === 'pod' && pod ? pod : undefined, n }, signal),
    enabled: open,
    staleTime: 15_000, // sunucu önbelleği
    retry: 1,
  });
  const d = q.data;
  const rows: LogRow[] = d ? [...(d.before ?? []), ...(d.after ?? [])] : [];
  const hasPivot = rows.some(r => r.id === pivot.id);
  const all = hasPivot ? rows : [...rows, pivot].sort((a, b) => a.timestamp - b.timestamp);
  const nextRung = CONTEXT_RUNGS.find(r => r > n);
  return (
    <section aria-label="Çevre loglar" className="grid gap-2" data-testid="log-context">
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="xs" aria-expanded={open} onClick={() => setOpen(o => !o)} className="px-1 font-semibold"
          data-testid="log-context-toggle">
          <ChevronRight className={open ? 'rotate-90' : undefined} /> Çevre loglar
        </Button>
        {open && (
          <>
            <SegmentedControl size="sm" aria-label="Çevre kapsamı" value={scope} onChange={v => setScope(v)}
              options={[
                { value: 'pod', label: 'Aynı pod', disabled: !pod, title: pod ? `pod ${pod}` : 'Bu kaydın pod bilgisi yok' },
                { value: 'service', label: 'Aynı servis', title: svc ? `servis ${svc}` : undefined },
              ]} />
            <span className="text-[length:var(--fs-xs)] text-(--text3)">±{n} gösteriliyor</span>
          </>
        )}
      </div>
      {open && (
        <>
          {q.isLoading && <p className="m-0 text-[length:var(--fs-xs)] text-(--text3)" role="status">Çevre loglar yükleniyor…</p>}
          {q.isError && <p className="m-0 text-[length:var(--fs-xs)] text-status-critical" role="alert">Çevre loglar okunamadı.</p>}
          {d?.degraded && <p className="m-0 text-[length:var(--fs-xs)] text-status-warn" role="status">⚠ {d.reason ?? 'log backend yavaş/erişilemez'} — çevre okunamadı</p>}
          {d && !d.degraded && (
            <ol className="m-0 grid list-none gap-px rounded-sm border p-0 font-mono text-[length:var(--fs-xs)]" data-testid="log-context-rows">
              {all.map(r => {
                const isPivot = r.id === pivot.id;
                return (
                  <li key={`${r.timestamp}:${r.id}`} aria-current={isPivot ? 'true' : undefined}
                    className={'grid grid-cols-[auto_auto_minmax(0,1fr)] items-center gap-2 px-2 py-0.5 '
                      + (isPivot ? 'bg-(--accent-soft) font-semibold' : '')}>
                    <span className="text-(--text3) tabular-nums">{clock(r.timestamp)}</span>
                    <StatusBadge size="sm" status={severityStatus(r.severity)}>{severityLabel(r)}</StatusBadge>
                    {onOpenRow && !isPivot ? (
                      <Button variant="ghost" size="xs" className="h-auto justify-start truncate px-0 font-mono font-normal" title={r.body}
                        onClick={() => onOpenRow(r)}><span className="truncate">{firstLine(r.body, 200)}</span></Button>
                    ) : <span className="truncate" title={r.body}>{firstLine(r.body, 200)}</span>}
                  </li>
                );
              })}
            </ol>
          )}
          <div className="flex flex-wrap gap-1.5">
            {nextRung && (
              <Button variant="outline" size="sm" onClick={() => setN(nextRung)} disabled={!d || !!d.degraded}>
                Daha geniş (±{nextRung})
              </Button>
            )}
            {onOpenWindow && rows.length > 0 && svc && (
              <Button variant="ghost" size="sm"
                onClick={() => onOpenWindow(Math.min(...all.map(r => r.timestamp)), Math.max(...all.map(r => r.timestamp)), svc)}>
                Bu pencereyi Logs'ta aç
              </Button>
            )}
          </div>
        </>
      )}
    </section>
  );
}
