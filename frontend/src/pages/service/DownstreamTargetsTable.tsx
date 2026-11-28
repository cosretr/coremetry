// DownstreamTargetsTable — follow-up 2026-11-21 (operatör onayı): "Giden
// çağrılar · hedefe göre". Kaynak /api/services/{name}/downstream-red — VM
// HTTP client histogramı (server_address başına istek/sn, p95, hata %,
// baskın hata) + HttpClient havuz durumu. Prod span'leri upstream
// örneklenmiş; bu sayım örnekleme öncesi TAM sayımdır. Hedef topolojide
// bilinen bir servise çözülüyorsa servis sayfasına bağlanır, aksi adres.
// LAZY chunk (ServiceRuntimeSection yükler); sunucu 60 s önbellekli.
import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import type { DownstreamTarget } from '@/lib/types';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

const fmtNum = (v: number, d = 2) => (Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(d));

/** SAF — havuz satırı (yoksa null). */
export function poolLine(pool: { leased: number; max: number; pending: number } | undefined, tpl: string): string | null {
  if (!pool) return null;
  return tpl.replace('{l}', fmtNum(pool.leased, 0)).replace('{m}', fmtNum(pool.max, 0)).replace('{p}', fmtNum(pool.pending, 1));
}

export default function DownstreamTargetsTable({ service, from, to, env, cluster }: {
  service: string; from: number; to: number; env?: string; cluster?: string;
}) {
  const tr = useT();
  const q = useQuery({
    queryKey: ['svc-downstream-red', service, from, to, env ?? '', cluster ?? ''],
    queryFn: () => api.serviceDownstreamRED(service, from, to, { env, cluster }),
    enabled: !!service,
    staleTime: 60_000,
  });
  const cols = useMemo<ColumnDef<DownstreamTarget>[]>(() => [
    { id: 'target', label: tr('ds.target'), width: 320, minWidth: 180, flex: true, priority: 1, sortValue: r => r.service || r.address },
    { id: 'rps', label: tr('ds.rps'), width: 90, minWidth: 72, numeric: true, priority: 1, sortValue: r => r.rps },
    { id: 'p95', label: tr('ds.p95'), width: 90, minWidth: 72, numeric: true, priority: 1, sortValue: r => r.p95Ms },
    { id: 'err', label: tr('ds.err'), width: 80, minWidth: 64, numeric: true, priority: 1, sortValue: r => r.errorPct },
    { id: 'dom', label: tr('ds.domErr'), width: 220, minWidth: 120, priority: 2, sortValue: r => r.dominantError ?? '' },
  ], [tr]);
  const d = q.data;
  if (d && !d.available) return null; // VM yok — bölüm çizilmez (span topolojisi Bağımlılıklar'da)
  const pool = poolLine(d?.pool, tr('ds.pool'));
  return (
    <div className="card" data-testid="downstream-targets">
      <div className="ov-card-h">
        <h3>{tr('ds.title')}</h3>
        <span className="badge b-gray" title={tr('ds.src').replace('{w}', String(Math.round((d?.windowSec ?? 900) / 60)))}>VM OTel · client</span>
        {d?.errorSource === 'status_code' && <span className="badge b-gray" title={tr('ds.errSrc.status_code')}>5xx</span>}
      </div>
      <div className="ov-card-b">
        {pool && <div className="kc-line mono" data-testid="downstream-pool">{pool}</div>}
        <DataTable<DownstreamTarget> storageKey="svc-downstream-targets" columns={cols} rows={d?.targets ?? []}
          ariaLabel={tr('ds.title')}
          getRowKey={r => r.address}
          sort={{ initial: { id: 'rps', dir: 'desc' } }}
          state={q.isPending ? { kind: 'loading' }
            : q.isError ? { kind: 'error', onRetry: () => void q.refetch(), message: tr('ds.err.read') }
              : { kind: 'empty', message: tr('ds.none') }}
          renderCell={(r, c) => {
            switch (c.id) {
              case 'target': return r.service
                ? <span><EntityLink to={{ kind: 'service', name: r.service }}>{r.service}</EntityLink> <span className="mono cell-muted">{r.address}</span></span>
                : <span className="mono" title={r.address}>{r.address}</span>;
              case 'rps': return fmtNum(r.rps);
              case 'p95': return r.p95Ms < 0 ? '—' : fmtNum(r.p95Ms, 1);
              case 'err': return r.errorPct > 0 ? <span className={r.errorPct >= 5 ? 'cell-err' : undefined}>{fmtNum(r.errorPct, 1)}</span> : '0';
              case 'dom': return r.dominantError
                ? <span className="mono" title={r.dominantError}>{r.dominantError} ({fmtNum(r.dominantErrorPct ?? 0, 0)}%)</span>
                : '—';
            }
            return null;
          }} />
      </div>
    </div>
  );
}
