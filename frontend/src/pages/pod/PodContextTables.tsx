// PodContextTables — v0.10.160 (A anatomisi §6, §8, §9). Konteynerler (Thanos
// KSM anlık — zaman serisi YOK), kardeş pod'lar (entity siblings ≤50 ×
// /api/clusters/pods topk 500; listede olmayan pod'da faz/restart/cpu/mem
// BİLİNMİYOR), etiketler ve ömür tarihçesi. v0.10.135 PodEntityPanel'in çip/
// link satırları tabloya terfi etti; kardeş tablosunda «Node» sütunu bilinçli
// YOK (siblings EntityRecord döner, runs_on yalnız hedef pod için çözülür —
// inceleme must-fix). Konteyner tablosu ≤ ~10 satır ve sıralanmaz → ham
// <table> meşru; kardeşler useDataTable.
// v0.10.943 (tablo standardı dilim 3, T1) — etiketler bir öznitelik paneli →
// KeyValue; ömür tarihçesi statik tablo kalır (gerekçe tablonun içinde).
import { useMemo, useState } from 'react';
import { Link } from 'react-router';
import { Badge, Button } from '@/components/ui';
import { KeyValue } from '@/components/coremetry/KeyValue';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef, type DataTableStateStaticProps } from '@/components/ui/DataTable';
import { fmtDateTime, fmtBytes } from '@/lib/utils';
import { t, tf, useLang, type Lang } from '@/lib/i18n';
import { fmtCores, podPhaseBadge } from '@/pages/clusters/thresholds';
import { entityHref, entityLiveness } from '@/lib/entityHref';
import type { EntityContainersResponse, EntityRecord, TimeRange } from '@/lib/types';
import type { SiblingRow } from './podPage';
import { waitingReasonTone } from './waitingReason';

// v0.10.929 (K5) — hazır + yeniden başlamamış konteyner normal hâl: nötr.
function containerTone(c: { readyKnown: boolean; ready: boolean; restarts: number; lastTermReason?: string }): 'neutral' | 'danger' | 'warning' {
  if (!c.readyKnown) return 'neutral';
  if (!c.ready) return 'danger';
  return c.restarts > 0 || c.lastTermReason ? 'warning' : 'neutral';
}

export function PodContainersTable({ ctr, pending, containerRecs, error }: {
  ctr?: EntityContainersResponse;
  pending: boolean;
  /** entity çocukları — KSM serisi yokken en azından adlar */
  containerRecs?: EntityRecord[];
  /**
   * v0.10.967 — HTTP okumasının hatası (react-query `error`, yalnız isError iken).
   * Verilmezse ilk yüklemede düşen istek `ctr` undefined + pending false olarak
   * gelir ve "KSM serisi yok" diye okunurdu; tazeleme düşerse önbellekteki eski
   * satırlar hatasız "güncel" görünürdü.
   */
  error?: unknown;
}) {
  const lang = useLang(); // v0.10.2246 — metinler arayüz dilinde (podpage.ctr.*)
  const rows = ctr?.containers ?? [];
  // v0.10.967 — tablo standardı dilim 5 (P-2): `pending` Spinner erken dönüşü
  // kalktı; yükleniyor da tablonun İÇİNDE, başlık durur. Kaynak notu eskisi
  // gibi yalnız yanıt gelince (pending'de hiç yoktu).
  // Thanos hatası (sunucu 200 + `error`, konteyner listesi HER ZAMAN boş —
  // internal/api/entities.go) ve düşen HTTP okuması bu tablonun HATA hâli:
  // gövde "KSM serisi yok" (boş sonuç) DEMEZ. Şerit yalnız okuma düştüğü hâlde
  // satır ekrandayken (tazeleme hatası, önbellekteki son liste) — hata iki kez
  // basılmaz, eski satırlar sessizce "güncel" okunmaz.
  const failed = !!ctr?.error || !!error;
  const failText = ctr?.error ?? (error instanceof Error ? error.message : error ? String(error) : '');
  const showRows = !pending && rows.length > 0;
  const state: Omit<DataTableStateStaticProps, 'colSpan'> = pending
    ? { kind: 'loading' }
    : failed
      ? (ctr?.error ? { kind: 'error', message: tf('podpage.ctr.readErr', lang, { err: ctr.error }) } : { kind: 'error' })
      : { kind: 'empty', message: `${t('podpage.ctr.noKsm', lang)}${containerRecs && containerRecs.length > 0 ? tf('podpage.ctr.names', lang, { names: containerRecs.map(c => c.name).join(', ') }) : ''}` };
  return (
    <>
      {showRows && failed && (
        <div className="pod-cap">
          <Badge tone="warning" title={failText}>{t(ctr?.error ? 'podpage.ctr.thanosFail' : 'podpage.ctr.stale', lang)}</Badge>
        </div>
      )}
      <div className="table-wrap">
        {/* v0.10.943 — statik tablo (T1): pod başına ≤ ~10 konteyner, sıralanmaz. */}
        <table>
          <thead><tr><th>{t('podpage.ctr.name', lang)}</th><th>Ready</th><th className="num">Restarts</th><th>Waiting</th><th>{t('podpage.ctr.lastTerm', lang)}</th></tr></thead>
          <tbody>
            {/* v0.10.967 (P-2) — statik tablonun durum satırı; colSpan = thead'deki 5 <th>. */}
            {!showRows ? <DataTableState colSpan={5} {...state} /> : rows.map(c => (
              <tr key={c.name}>
                <td className="mono">{c.name}</td>
                <td>{c.readyKnown ? <Badge tone={containerTone(c)}>{c.ready ? 'ready' : 'not ready'}</Badge> : <span className="field-hint" title={t('podpage.ctr.noReadySeries', lang)}>?</span>}</td>
                <td className={c.restarts > 0 ? 'num cell-warn' : 'num'}>{c.restarts}</td>
                {/* v0.10.929 (K5) — olağan başlangıç geçişi (ContainerCreating /
                    PodInitializing) nötr; kubelet arıza nedenleri kırmızı. */}
                <td>{c.waitingReason ? <Badge tone={waitingReasonTone(c.waitingReason)}>{c.waitingReason}</Badge> : '—'}</td>
                <td>{c.lastTermReason ? <Badge tone="warning">{c.lastTermReason}</Badge> : '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!pending && (
        <div className="pod-cap">
          <code className="mono">GET /api/entity/containers</code> · kube_pod_container_status_{'{'}ready,restarts_total,waiting_reason,last_terminated_reason{'}'}{t('podpage.ctr.note', lang)}
        </div>
      )}
    </>
  );
}

const sibCols = (lang: Lang): ColumnDef<SiblingRow>[] => [
  { id: 'pod', label: 'Pod', width: 280, sortValue: r => r.name, naturalDir: 'asc' },
  { id: 'status', label: t('podpage.col.status', lang), width: 90, sortValue: r => entityLiveness(r.rec) },
  { id: 'phase', label: t('podpage.col.phase', lang), width: 110, sortValue: r => r.phase ?? '' },
  { id: 'restarts', label: 'Restarts', width: 90, numeric: true, sortValue: r => r.restarts ?? -1, tone: r => ((r.restarts ?? 0) > 0 ? 'warn' : undefined) },
  { id: 'cpu', label: 'CPU', width: 90, numeric: true, sortValue: r => r.cpuCores ?? -1 },
  { id: 'mem', label: t('podpage.col.mem', lang), width: 100, numeric: true, sortValue: r => r.memBytes ?? -1 },
];

export function PodSiblingsTable({ rows, pageRange, at, clusterName, truncated }: {
  rows: SiblingRow[];
  pageRange: TimeRange;
  at?: number;
  clusterName: string;
  /** /api/clusters/pods topk 500'e dayandı — «listede yok» kanıt değil */
  truncated: boolean;
}) {
  const lang = useLang();
  const columns = useMemo(() => sibCols(lang), [lang]);
  const dt = useDataTable<SiblingRow>({ storageKey: 'pod-siblings', columns, rows, initialSort: { id: 'restarts', dir: 'desc' } });
  // v0.10.954 — tablo standardı T12: boşken erken dönüş kalktı; "kardeş pod
  // yok" tablonun İÇİNDE, başlık durur. Kaynak notu yalnız satır varken.
  return (
    <>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.length === 0 ? <DataTableState dt={dt} kind="empty" message={t('podpage.sib.none', lang)} /> : dt.sortedRows.map(r => {
              const live = entityLiveness(r.rec);
              return (
                <tr key={r.rec.id}>
                  <td className="mono" title={`${clusterName} / ${r.rec.namespace ?? ''} / ${r.name}`}>
                    {/* service TAŞINMAZ (inceleme #16): kardeş o servisi çalıştırmıyor olabilir; yanlış RED kapsamı + yanlış geri linki olurdu. */}
                    <Link to={entityHref(r.rec, { range: pageRange, at: at || undefined, clusterName })} className="sec">{r.name}</Link>
                  </td>
                  <td>{live === 'live' ? <Badge>live</Badge> : live === 'stale' ? <Badge tone="warning">stale</Badge> : <Badge tone="danger">gone</Badge>}</td>
                  <td>{r.known && r.phase ? <span className={`badge ${podPhaseBadge(r.phase)}`}>{r.phase}</span> : <span className="field-hint" title={t('podpage.sib.phaseUnknown', lang)}>—</span>}</td>
                  <DataTableCell dt={dt} col="restarts" row={r} title={r.lastTermReason ? tf('podpage.sib.last', lang, { r: r.lastTermReason }) : undefined} value={r.restarts === null ? '—' : r.restarts} />
                  <DataTableCell dt={dt} col="cpu" row={r} value={r.cpuCores === null ? '—' : fmtCores(r.cpuCores)} />
                  <DataTableCell dt={dt} col="mem" row={r} value={r.memBytes === null ? '—' : fmtBytes(r.memBytes)} />
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {rows.length > 0 && (
        <div className="pod-cap">
          {t('podpage.sib.note1', lang)} <code className="mono">/api/entity</code> {t('podpage.sib.note2', lang)} <code className="mono">/api/clusters/pods</code> {t('podpage.sib.note3', lang)}{truncated ? t('podpage.sib.cut', lang) : ''}.
        </div>
      )}
    </>
  );
}

export function PodLabelsTable({ labels }: { labels: Record<string, string> | undefined }) {
  const lang = useLang();
  const [all, setAll] = useState(false);
  const entries = Object.entries(labels ?? {}).sort(([a], [b]) => a.localeCompare(b));
  if (entries.length === 0) return <div className="pod-cap">{t('podpage.lbl.none', lang)}</div>;
  const shown = all ? entries : entries.slice(0, 8);
  return (
    <>
      {/* v0.10.943 — etiket değerleri k8s kimlik belirteçleri (uygulama adı, hash) → mono; anahtar etiket, mono değil. */}
      <KeyValue labelWidth="wide" items={shown.map(([k, v]) => ({ id: k, k, v, mono: true }))} />
      {entries.length > 8 && (
        <div className="pod-cap"><Button variant="secondary" size="xs" onClick={() => setAll(v => !v)}>{all ? t('podpage.lbl.less', lang) : tf('podpage.lbl.more', lang, { n: entries.length - 8 })}</Button></div>
      )}
    </>
  );
}

export function PodLifetimesTable({ lifetimes, atMatch, at }: { lifetimes: EntityRecord[]; atMatch?: boolean; at?: number }) {
  const lang = useLang();
  return (
    <>
      <div className="table-wrap">
        {/* v0.10.943 — statik tablo (T1): aynı ada ait ömürler, sunucu ≤ 50 (pratikte 1–3), sunucu sırası anlamlı; otomatik düzen zaman damgası + uid'yi tam gösterir, dar yarım kolonda kaydırır. */}
        <table>
          <thead><tr><th>Valid from</th><th>Valid to</th><th>{t('podpage.lt.source', lang)}</th><th>uid</th></tr></thead>
          <tbody>
            {lifetimes.map(l => (
              <tr key={`${l.validFrom}|${l.uid ?? ''}`}>
                <td className="mono">{fmtDateTime(new Date(l.validFrom))}</td>
                <td className="mono">{l.validTo ? fmtDateTime(new Date(l.validTo)) : t('podpage.open', lang)}</td>
                <td>{l.source}</td>
                <td className="mono">{l.uid ?? '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="pod-cap">
        {tf('podpage.lt.count', lang, { n: lifetimes.length })}{!!at && atMatch === false ? t('podpage.lt.at', lang) : ''}.
      </div>
    </>
  );
}
