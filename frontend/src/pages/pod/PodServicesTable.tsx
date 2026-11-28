// PodServicesTable — v0.10.160 (A anatomisi §4). Bu pod'dan geçen servisler:
// /api/entity/services (entity_seen_5m MV). v0.10.135 PodEntityPanel'in link
// listesi tabloya terfi etti (operatör kuralı: tablo > kart/çip). «Pod» sütunu
// YOK — pod kapsamında services[].pods hep 1 (inceleme: B mockup hatası).
// Son görülme rows[] (pod × servis) üzerinden. Her satır → servis sayfası +
// bu pod'a süzülmüş Traces.
import { useMemo } from 'react';
import { Link } from 'react-router';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateProps,
} from '@/components/ui/DataTable';
import { fmtNum, fmtDateTime } from '@/lib/utils';
import { t, tf, useLang, type Lang } from '@/lib/i18n';
import { tracesPivotHref } from '@/lib/pivotHref';
import type { EntityServicesResponse, TimeRange } from '@/lib/types';
import { EntityLink } from '@/components/coremetry/entity/EntityLink';

interface Row { service: string; spans: number; errors: number; avgMs: number; lastSeen?: string }

// v0.10.943 (tablo standardı dilim 3) — hata sayısı kolon tonu (T9); satır
// sonu "Traces →" bağlantısı eylem kolonu (T8, id/width aynı).
// v0.10.2246 — başlıklar arayüz dilinde (podpage.col.*).
const cols = (lang: Lang): ColumnDef<Row>[] => [
  { id: 'service', label: t('podpage.col.service', lang), width: 260, sortValue: r => r.service, naturalDir: 'asc' },
  { id: 'spans', label: t('podpage.col.spans', lang), width: 100, numeric: true, sortValue: r => r.spans },
  { id: 'errors', label: t('podpage.col.errors', lang), width: 90, numeric: true, sortValue: r => r.errors, tone: r => (r.errors > 0 ? 'err' : undefined) },
  { id: 'errpct', label: t('podpage.col.errPct', lang), width: 90, numeric: true, sortValue: r => (r.spans ? r.errors / r.spans : 0) },
  { id: 'avg', label: t('podpage.col.avgMs', lang), width: 90, numeric: true, sortValue: r => r.avgMs },
  { id: 'last', label: t('podpage.col.lastSeen', lang), width: 160, sortValue: r => r.lastSeen ?? '' },
  { id: 'links', label: t('podpage.col.actions', lang), kind: 'actions', width: 120 },
];

function ms(v: number): string {
  if (v <= 0) return '—';
  return v < 10 ? v.toFixed(1) : v.toFixed(0);
}

export function PodServicesTable({ data, pending, error, pod, spanCluster, pageRange }: {
  data?: EntityServicesResponse;
  pending: boolean;
  error: unknown;
  pod: string;
  /** span tarafı cluster değeri (Traces linki için; boşsa anahtar yazılmaz) */
  spanCluster: string;
  pageRange: TimeRange;
}) {
  const lastByService = new Map<string, string>();
  for (const r of data?.rows ?? []) if (r.pod === pod) lastByService.set(r.service, r.lastSeen);
  const rows: Row[] = (data?.services ?? []).map(s => ({ ...s, lastSeen: lastByService.get(s.service) }));
  const lang = useLang();
  const columns = useMemo(() => cols(lang), [lang]);
  const dt = useDataTable<Row>({ storageKey: 'pod-services', columns, rows, initialSort: { id: 'spans', dir: 'desc' } });
  // v0.10.954 — tablo standardı T12: erken dönüşler (Spinner / Empty) kalktı;
  // yükleniyor / hata / boş tablonun İÇİNDE, başlık durur. Sıra ve koşullar
  // eskisiyle aynı; tablo çevresindeki notlar yalnız satır varken.
  // v0.10.190 — sebep listesi dürüst: MV kurulu değilse (dış Distributed
  // prod'da 0011 operatör migration'ı) de satır yoktur; alttaki trace
  // listesi span gösteriyorsa sorun span değil MV'dir.
  const showRows = !pending && !error && rows.length > 0;
  const state: Omit<DataTableStateProps<Row>, 'dt'> =
    pending ? { kind: 'loading' }
    : error ? { kind: 'error', message: tf('podpage.svc.loadErr', lang, { err: String(error) }) }
    : { kind: 'empty', message: t('podpage.svc.empty', lang) };
  const filters = JSON.stringify([{ k: 'k8s.pod.name', op: '=', v: [pod] }]);
  return (
    <>
      {/* v0.10.190 — namespace'siz span satırları pod adıyla eşlendi; varsayım ilan edilir */}
      {showRows && (data?.nsMissingRows ?? 0) > 0 && (
        <div className="pod-cap">{tf('podpage.svc.nsMissing', lang, { n: data!.nsMissingRows! })}</div>
      )}
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {!showRows ? <DataTableState dt={dt} {...state} /> : dt.sortedRows.map(r => {
              // Pencere tracesPivotHref'te ZORUNLU (inceleme #9) — ham ?range= boşken /traces kendi 30m'sine düşerdi.
              const tracesHref = tracesPivotHref({ window: pageRange, service: r.service, cluster: spanCluster || undefined, filters });
              const errPct = r.spans ? (100 * r.errors) / r.spans : 0;
              return (
                <tr key={r.service}>
                  <td><EntityLink to={{ kind: 'service', name: r.service, opts: { range: pageRange } }} className="sec">{r.service}</EntityLink></td>
                  <td className="num">{fmtNum(r.spans)}</td>
                  <DataTableCell dt={dt} col="errors" row={r} value={fmtNum(r.errors)} />
                  <td className="num">{errPct.toFixed(2)}%</td>
                  <td className="num">{ms(r.avgMs)}</td>
                  <td className="mono">{r.lastSeen ? fmtDateTime(new Date(r.lastSeen)) : '—'}</td>
                  <DataTableCell dt={dt} col="links" row={r}><Link to={tracesHref} className="sec" title={t('podpage.svc.tracesTitle', lang)}>Traces →</Link></DataTableCell>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {showRows && (
        <div className="pod-cap">
          <code className="mono">GET /api/entity/services</code>{t('podpage.svc.note', lang)}
        </div>
      )}
    </>
  );
}
