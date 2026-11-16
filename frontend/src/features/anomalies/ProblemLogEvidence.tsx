import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { DisclosureButton } from '@/components/ui/DisclosureButton';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableState, type DataTableColumn, type DataTableStateProps } from '@/components/ui/DataTable';
import { useLogsPatterns } from '@/lib/queries/logs';
import { logsHref, type LogsPivot } from '@/lib/logsUrl';
import { sevClass, sevName } from '@/lib/utils';
import type { LogPatternGroup, LogPatternsResult } from '@/lib/types';
import { problemWindowNs } from './problemOffenders';
import { coveredLabel } from '@/components/LogPatternsPanel';
import { t, tf, useLang, type Lang } from '@/lib/i18n'; // v0.10.2286 — metinler arayüz dilinde (TR aynı)

// ProblemLogEvidence — v0.10.452 (log arama denetimi C1): Problem
// detayında "Log kanıtı" — problem penceresinde servisin ERROR+ log
// desenleri ve sayımı. ES MALİYETİ (operatör kısıtı 2026-09-06):
//   • varsayılan KAPALI, açılınca TEK /api/logs/patterns isteği,
//   • örnek tavanı 500 (1 ES sayfası; /logs panelinin 2000'i 4 sayfa),
//   • pencere problemWindowNs (dakika basamağı → sunucu önbelleği
//     paylaşılır; probWindow her render'da yeni Date.now() üretir —
//     v0.5.184 sınıfı, o yalnız linkler için),
//   • staleTime = sunucu TTL (30 sn), yeniden açmak istek atmaz.
// Dürüstlük: degraded → partial → boş sırası (v0.10.415 sözleşmesi);
// total tavana dayandıysa "≥". Satır pivotu /logs'a aynı pencere +
// ERROR+ + desen sorgusu ile, Desenler paneli açık (?panel=patterns).
// v0.10.967 — tablo standardı T12 (dilim 5): yükleniyor / hata / degraded /
// boş tablonun İÇİNDE (başlık kalır). degraded ("hiçbir sayı gerçek değil")
// tabloyu eskiden de gizliyordu: okunamadı demek, hata satırı olur. Yenileme
// hatası önbellekteki bayat desenleri artık gizler (eskiden hata rozeti ile
// bayat satırlar yan yanaydı); kısmi-sonuç rozeti ve sayım satırı da bayat
// yanıtı anlattığı için onlar da yalnız hatasız yanıtta.
export const PROBLEM_LOG_SAMPLE = 500;
export const PROBLEM_LOG_SEVERITY = 17;

export function patternsTotalLabel(d: Pick<LogPatternsResult, 'total' | 'totalIsLowerBound' | 'sampled' | 'cap' | 'distinct'>, lang: Lang = 'tr'): string {
  const tot = `${d.totalIsLowerBound ? '≥ ' : ''}${d.total.toLocaleString()} ERROR+ log`;
  const cap = d.sampled >= d.cap ? tf('plog.total.cap', lang, { n: d.cap.toLocaleString() }) : '';
  return `${tot} · ${tf('plog.total.sampled', lang, { n: d.sampled.toLocaleString(), cap })} · ${tf('plog.total.distinct', lang, { n: d.distinct })}`;
}

// v0.10.2286 — kolon etiketleri arayüz dilinde (plog.col.*); id / genişlik / sıralama aynı.
const cols = (lang: Lang): DataTableColumn<LogPatternGroup>[] => [
  { id: 'sev', label: t('plog.col.sev', lang), width: 84, sortValue: r => r.severity },
  { id: 'tpl', label: t('plog.col.tpl', lang), width: 460, sortValue: r => r.template, naturalDir: 'asc', flex: true },
  { id: 'count', label: t('plog.col.count', lang), width: 76, numeric: true, sortValue: r => r.count },
  { id: 'svc', label: t('plog.col.svc', lang), width: 70, numeric: true, sortValue: r => r.serviceCount },
];

export function ProblemLogEvidence({ service, startedAt, resolvedAt, linkWindow }: {
  service: string;
  startedAt: number;
  resolvedAt?: number | null;
  /** Link penceresi (probWindow) — sorgu penceresi değil. */
  linkWindow: LogsPivot['window'];
}) {
  const [open, setOpen] = useState(false);
  const lang = useLang();
  const columns = useMemo(() => cols(lang), [lang]);
  const win = useMemo(() => problemWindowNs(startedAt, resolvedAt ?? undefined, Date.now()), [startedAt, resolvedAt]);
  const q = useLogsPatterns({ service, from: win.fromNs, to: win.toNs, severity: PROBLEM_LOG_SEVERITY, limit: 20, sample: PROBLEM_LOG_SAMPLE }, open);
  const d = q.data;
  const rows = d?.groups ?? [];
  const dt = useDataTable<LogPatternGroup>({ storageKey: 'problem-log-evidence', columns, rows });
  const showRows = !!d && !q.isError && !d.degraded && rows.length > 0;
  const tableState: Omit<DataTableStateProps<LogPatternGroup>, 'dt'> =
    q.isPending ? { kind: 'loading' }
    : q.isError ? (q.error instanceof Error && q.error.message
      ? { kind: 'error', message: tf('plog.err', lang, { e: q.error.message }) }
      : { kind: 'error' })
    : d?.degraded ? {
      kind: 'error',
      message: tf('plog.degraded', lang, { r: d.reason ?? 'degraded' }),
    }
    : { kind: 'empty', message: tf('plog.empty', lang, { svc: service }) };
  return (
    <div>
      <DisclosureButton expanded={open} onClick={() => setOpen(v => !v)}
        title={t('plog.toggle.title', lang)}>
        {t('plog.toggle', lang)}
      </DisclosureButton>
      {open && (
        <div style={{ marginTop: 8 }}>
          {d?.partial && !d.degraded && !q.isError && (
            <div className="badge b-warn" title={t('plog.partial.title', lang)}>{t('plog.partial', lang)}{d.shardsFailed ? ` · ${d.shardsFailed} shard` : ''}</div>
          )}
          <div className="table-wrap">
            <table>
              <DataTableColgroup dt={dt} />
              <DataTableHead dt={dt} />
              <tbody>
                {!showRows ? <DataTableState dt={dt} {...tableState} /> : dt.sortedRows.map(r => (
                  <tr key={r.hash}>
                    <td><span className={`badge ${sevClass(r.severity)}`}>{sevName(r.severity)}</span></td>
                    <td className="mono" title={r.sample}>
                      {/* v0.10.945 — örnek satır Link başlığında: hücre title'ı iç bağlantının kendi title'ıyla gölgeleniyordu (T7 göçü). */}
                      {r.query
                        ? <Link to={logsHref({ window: linkWindow, service, severity: PROBLEM_LOG_SEVERITY, q: r.query, panel: 'patterns' })}
                            title={`${r.sample}\n${t('plog.row.title', lang)}`}>{r.template}</Link>
                        : r.template}
                    </td>
                    <td className="num">{r.count.toLocaleString()}</td>
                    <td className="num">{r.serviceCount}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {showRows && d && (
            <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 6 }}>
              {patternsTotalLabel(d, lang)}{coveredLabel(d, lang) ? ` · ${coveredLabel(d, lang)}` : ''}{t('plog.foot', lang)}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
