// AnomalyWindowTable — v0.10.162 (etüt «anomali işaretleri» seçenek C'nin
// tablosu, A'nın bantlarına aşılandı; dilim 1, sıfır backend). Servis
// sayfasındaki RED grafiklerinin altında, PENCEREDEKİ anomaliler: Tür ·
// Desen · Başlangıç · Son görülme · Tepe · Durum · Geri bildirim · eylemler.
// Çakışan anomaliler iki satır — lane/hit-test kodu gerekmez, tablo ayırt
// eder. v0.10.181 (dilim 2): «Anomali» / «Değil» KARARI anomaly_verdicts'e
// (PUT /api/anomalies/{id}/verdict); «Değil» ayrıca mevcut susturmayı zincirler
// (onMute → çağıran hem kararı hem susturmayı yazar). → mevcut
// AnomalyDetailDrawer (?anomaly=). Yalnız pencerede anomali varsa çizilir.
import { Badge, LinkButton } from '@/components/ui';
import { useDataTable, DataTableHead, DataTableColgroup, type ColumnDef } from '@/components/ui/DataTable';
import { fmtDateTime } from '@/lib/utils';
import { ANOMALY_KIND_COLOR, anomalyKindLabel, silenceKey } from '@/lib/anomalyRegions';
import { isMLAnomaly } from '@/lib/anomalyHref';
import { anomalyRatioLabel } from '@/lib/anomalyBaseline'; // v0.10.1792
import { SnoozeButton } from './SnoozeButton';
import { t as tr, tf, useLang, type Lang } from '@/lib/i18n';
import type { AnomalyEvent, AnomalySilence } from '@/lib/types';

interface Row { e: AnomalyEvent; silence?: AnomalySilence }

// v0.10.2272 — metinler arayüz dilinde (lib/i18n anomalyWindow.*; TR metin aynı).
const colsFor = (lang: Lang): ColumnDef<Row>[] => {
  const t = (k: string) => tr(`anomalyWindow.col.${k}`, lang);
  return [
    { id: 'kind', label: t('kind'), width: 130, sortValue: r => r.e.kind, naturalDir: 'asc' },
    { id: 'pattern', label: t('pattern'), width: 300, sortValue: r => r.e.pattern, naturalDir: 'asc' },
    { id: 'started', label: t('started'), width: 150, sortValue: r => r.e.startedAt },
    { id: 'last', label: t('last'), width: 150, sortValue: r => r.e.lastSeen },
    { id: 'peak', label: t('peak'), width: 80, numeric: true, sortValue: r => r.e.peakRatio },
    { id: 'status', label: t('status'), width: 90, sortValue: r => r.e.status },
    { id: 'fb', label: t('feedback'), width: 210, sortValue: r => (r.e.verdict === 'anomaly' ? 2 : r.e.verdict === 'not_anomaly' || r.silence ? 1 : 0) },
    { id: 'act', label: t('actions'), kind: 'actions', width: 260 },
  ];
};
const COLS = { tr: colsFor('tr'), en: colsFor('en') } as const;

export function AnomalyWindowTable({ events, silences, canEdit, onOpen, onMute, onVerdict, truncated }: {
  events: AnomalyEvent[];
  silences: AnomalySilence[] | undefined;
  canEdit: boolean;
  onOpen: (id: string) => void;
  onMute: (e: AnomalyEvent, durationSec: number) => void;
  /** v0.10.181 — «Anomali» kararı (not_anomaly kararını onMute zinciri yazar) */
  onVerdict: (e: AnomalyEvent, verdict: 'anomaly' | 'not_anomaly') => void;
  /** /api/anomalies/events limit=200'e dayandı — liste tam değil */
  truncated: boolean;
}) {
  const lang = useLang();
  const t = (k: string) => tr(`anomalyWindow.${k}`, lang);
  const byFp = new Map<string, AnomalySilence>();
  for (const s of silences ?? []) if (s.active) byFp.set(s.fingerprint, s);
  const rows: Row[] = events.map(e => ({ e, silence: byFp.get(e.id) ?? byFp.get(silenceKey(e)) }));
  const dt = useDataTable<Row>({ storageKey: 'svc-anomaly-window', columns: COLS[lang], rows, initialSort: { id: 'started', dir: 'desc' } });
  // v0.10.973 — tablo standardı T12 muafı (tarif §2 bilinçli kendini gizleme): durum satırı
  // yok, çünkü tablo boş olamaz — Overview onu yalnız pencerede anomali varken çizer (operatör kararı).
  if (rows.length === 0) return null;
  return (
    <>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.map(({ e, silence }) => (
              <tr key={e.id} style={silence ? { color: 'var(--text3)' } : undefined}>
                <td>
                  <span aria-hidden="true" style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 2, background: silence ? 'var(--text3)' : (ANOMALY_KIND_COLOR[e.kind] ?? 'var(--warn)'), marginRight: 6, verticalAlign: 'middle' }} />
                  {anomalyKindLabel(e.kind, lang)}
                </td>
                <td className="mono" title={e.pattern}>{e.pattern}</td>
                <td className="mono">{fmtDateTime(new Date(e.startedAt / 1e6))}</td>
                <td className="mono">{fmtDateTime(new Date(e.lastSeen / 1e6))}</td>
                <td className="num">{anomalyRatioLabel(e, e.peakRatio, lang)}</td>
                {/* v0.10.929 (K5) — active normal durum → nötr; cleared geçiş → yeşil (streams/çekmece ile tek kural). */}
                <td>{e.status === 'active' ? <Badge tone="neutral">active</Badge> : <Badge tone="success">cleared</Badge>}</td>
                <td>
                  {/* v0.10.929 (K5) — anomaliyi teyit etmek iyi haber değil: karar rozeti nötr. */}
                  {e.verdict === 'anomaly' && <Badge tone="neutral" title={`${e.verdictBy ?? ''}${e.verdictAt ? ` · ${fmtDateTime(new Date(e.verdictAt / 1e6))}` : ''}`}>{t('verdictAnomaly')}</Badge>}
                  {e.verdict === 'not_anomaly' && <Badge tone="neutral" title={`${e.verdictBy ?? ''}${e.verdictAt ? ` · ${fmtDateTime(new Date(e.verdictAt / 1e6))}` : ''}`}>{t('verdictNot')}</Badge>}
                  {silence && <Badge tone="neutral" style={e.verdict ? { marginLeft: 6 } : undefined} title={`${silence.createdBy} · ${fmtDateTime(new Date(silence.createdAt / 1e6))}${silence.reason ? ` · ${silence.reason}` : ''}`}>{silence.untilAt > 0 ? tf('anomalyWindow.mutedUntil', lang, { t: fmtDateTime(new Date(silence.untilAt / 1e6)) }) : t('muted')}</Badge>}
                  {!e.verdict && !silence && <span className="field-hint">—</span>}
                </td>
                <td className="col-actions">
                  <LinkButton onClick={() => onOpen(e.id)} title={t(isMLAnomaly(e) ? 'openMl' : 'openDrawer')}>{t('details')}</LinkButton>
                  {canEdit && e.verdict !== 'anomaly' && (
                    <span style={{ marginLeft: 8 }}>
                      <LinkButton onClick={() => onVerdict(e, 'anomaly')} title={t('anomalyTitle')}>{t('anomaly')}</LinkButton>
                    </span>
                  )}
                  {/* v0.10.2080 — ML olayı: susturma yok, «Değil» yalnız ML kararı */}
                  {canEdit && isMLAnomaly(e) && e.verdict !== 'not_anomaly' && (
                    <span style={{ marginLeft: 8 }}>
                      <LinkButton onClick={() => onVerdict(e, 'not_anomaly')} title={t('notMlTitle')}>{t('not')}</LinkButton>
                    </span>
                  )}
                  {canEdit && !isMLAnomaly(e) && !silence && e.verdict !== 'not_anomaly' && (
                    <span style={{ marginLeft: 8 }}>
                      <SnoozeButton label={t('notMute')} title={t('notMuteTitle')} onMute={sec => onMute(e, sec)} />
                    </span>
                  )}
                  {/* v0.10.184 (inceleme #5) — zaten susturulmuş olayda «değil» kararı yine verilebilsin (susturma değil, yalnız kayıt) */}
                  {canEdit && silence && e.verdict !== 'not_anomaly' && (
                    <span style={{ marginLeft: 8 }}>
                      <LinkButton onClick={() => onVerdict(e, 'not_anomaly')} title={t('notMutedTitle')}>{t('not')}</LinkButton>
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="pod-cap">
        <code className="mono">GET /api/anomalies/events</code> {tf('anomalyWindow.caption', lang, { trunc: truncated ? t('captionTruncated') : '' })}
      </div>
    </>
  );
}
