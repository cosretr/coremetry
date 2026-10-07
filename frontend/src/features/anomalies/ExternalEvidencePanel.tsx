// ExternalEvidencePanel — v0.10.230 (Influx D5, audit §4 UI).
//
// kind=external problemde RootCausePanel'in yerine geçer: topoloji/servis
// tabanlı analiz yok, D4'ün root_cause_hypotheses(anchor=problem)'e
// yazdığı kanıt zinciri çizilir — metrik şeridi (metric_points `ext:*`
// serisi, TimeChart/uPlot), trace tablosu, pod tablosu, log imzaları.
// Hepsi mevcut atomlarla (useDataTable, badge, Spinner/Empty); yeni
// primitive yok. Hipotez /rootcause zarfından okunur (aynı FINAL satır).
// Fetch yalnız bu sayfa açıkken; polling yok (kanıt 5 dk'da bir güncellenir).
// v0.10.967 — tablo standardı T12 (dilim 5): üç kanıt tablosunun "yok"
// paragrafları tablonun İÇİNDE boş satır (başlık kalır). Yükleniyor / hata /
// "henüz toplanmadı" panel düzeyinde kalır: hipotez zarfı panelin tamamını
// (metrik kararı, sayım şeridi, üç tablo) tanımlıyor.

import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { Spinner, Empty } from '@/components/Spinner';
import { externalSummaryKind, externalSummaryNote } from '@/lib/problemSubject'; // v0.10.598
import { useDataTable, DataTableHead, DataTableColgroup, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { TimeChart } from '@/components/charts/TimeChart';
import { fmtDateTime } from '@/lib/utils';
import { fmtDur } from '@/components/traces/shared';
import { traceHref } from '@/lib/traceHref';
import type { Problem, PodHit, LogSignature } from '@/lib/types';
import { traceRows, endpointRows, evidenceCounts, pickSeries, labelKeys, labelValues, toChart, sevTone, type ExternalTraceRow, type ExternalEndpointRow } from './externalEvidence';

const BASELINE_LOOKBACK_NS = 3 * 3600e9;

const TRACE_COLS: ColumnDef<ExternalTraceRow>[] = [
  { id: 'time', label: 'Zaman', width: 176, sortValue: r => r.startNs, numeric: true },
  { id: 'trace', label: 'Trace', width: 150 },
  { id: 'root', label: 'Kök servis · op', width: 260, sortValue: r => `${r.rootService ?? ''} ${r.rootOp ?? ''}` },
  { id: 'err', label: 'Hata servis · op', width: 260, sortValue: r => `${r.errorService ?? ''} ${r.errorOp ?? ''}` },
  { id: 'dur', label: 'Süre', width: 90, numeric: true, sortValue: r => r.durationNs },
  { id: 'spans', label: 'Span', width: 70, numeric: true, sortValue: r => r.spans },
  { id: 'status', label: 'Durum', width: 100, sortValue: r => (r.missing ? 2 : r.errorSpans > 0 ? 0 : 1), numeric: true },
];

// v0.10.1004 — ilgili endpoint'ler (kanıt trace'lerinin geçtiği endpoint).
const ENDPOINT_COLS: ColumnDef<ExternalEndpointRow>[] = [
  { id: 'service', label: 'Servis', width: 260, sortValue: r => r.service },
  { id: 'path', label: 'Endpoint', width: 420, sortValue: r => r.path },
  { id: 'traces', label: 'Trace', width: 80, numeric: true, sortValue: r => r.traces },
  { id: 'errors', label: 'Hatalı', width: 80, numeric: true, sortValue: r => r.errorTraces },
];

const POD_COLS: ColumnDef<PodHit>[] = [
  { id: 'pod', label: 'Pod', width: 320, sortValue: r => r.pod },
  { id: 'count', label: 'Satır', width: 90, numeric: true, sortValue: r => r.count },
  { id: 'last', label: 'Son görülme', width: 176, numeric: true, sortValue: r => r.lastSeenNs },
];

const SIG_COLS: ColumnDef<LogSignature>[] = [
  { id: 'sev', label: 'Şiddet', width: 90, sortValue: r => r.severity },
  { id: 'tpl', label: 'İmza (örnek mesaj başlıkta)', width: 520, sortValue: r => r.template },
  { id: 'count', label: 'Kayıt', width: 80, numeric: true, sortValue: r => r.count },
  { id: 'traces', label: 'Trace', width: 80, numeric: true, sortValue: r => r.traceCount },
];

export function ExternalEvidencePanel({ problem, window: win }: {
  problem: Problem;
  window?: { fromNs: number; toNs: number };
}) {
  // v0.10.598 — ÖZET Problem'ler (tavan/düştü/küme) hipotez satırı HİÇ
  // almaz; onlar için "kanıt henüz toplanmadı" bir vaat, /rootcause isteği
  // boşa. Türü kural önekinden tanı, isteği hiç atma, dürüst açıklama çiz.
  const summary = externalSummaryKind(problem.ruleId);
  const rc = useQuery({
    // v0.10.1119 — yalnız hypothesis okunur: çekirdek uç (bubbleUp taraması yok).
    queryKey: ['problem-rootcause-core', problem.id],
    queryFn: () => api.problemRootCauseCore(problem.id),
    staleTime: 30_000,
    enabled: !summary,
  });
  const deep = rc.data?.hypothesis?.deep;
  const ext = deep?.external;
  const keys = useMemo(() => labelKeys(ext?.labels), [ext?.labels]);
  const values = useMemo(() => labelValues(ext?.labels), [ext?.labels]);

  // Şerit penceresi: problem penceresi + 3 saat baseline geriye. probWindow
  // basamaklı (problemWindowNs) — anahtar her render değişmez.
  const range = useMemo(() => {
    const to = win?.toNs ?? ext?.windowToNs ?? problem.startedAt;
    const anchor = Math.min(win?.fromNs ?? to, ext?.windowFromNs ?? to, problem.startedAt);
    return { from: anchor - BASELINE_LOOKBACK_NS, to };
  }, [win?.fromNs, win?.toNs, ext?.windowFromNs, ext?.windowToNs, problem.startedAt]);

  const mq = useQuery({
    queryKey: ['ext-evidence-series', ext?.source, ext?.query, values, range.from, range.to],
    enabled: !!ext,
    staleTime: 30_000,
    queryFn: ({ signal }) => api.metricQueryFull({
      name: `ext:${ext!.query}`,
      service: ext!.source,
      filters: JSON.stringify(keys.map((k, i) => ({ k, op: '=', v: [values[i]] }))),
      agg: 'max',
      step: 60,
      from: range.from,
      to: range.to,
    }, signal),
  });
  const chart = useMemo(() => toChart(pickSeries(mq.data?.series ?? null, [])), [mq.data]);

  const tRows = useMemo(() => traceRows(deep), [deep]);
  const epRows = useMemo(() => endpointRows(deep), [deep]); // v0.10.1004
  const pods = deep?.affectedPods ?? [];
  const sigs = deep?.logSignatures ?? [];
  const dtT = useDataTable<ExternalTraceRow>({ storageKey: 'ext-evidence-traces', columns: TRACE_COLS, rows: tRows });
  const dtE = useDataTable<ExternalEndpointRow>({ storageKey: 'ext-evidence-endpoints', columns: ENDPOINT_COLS, rows: epRows });
  const dtP = useDataTable<PodHit>({ storageKey: 'ext-evidence-pods', columns: POD_COLS, rows: pods });
  const dtS = useDataTable<LogSignature>({ storageKey: 'ext-evidence-sigs', columns: SIG_COLS, rows: sigs });

  if (summary) {
    const n = externalSummaryNote(summary);
    return <Empty icon="ℹ" title={n.title}>{n.body}</Empty>;
  }
  if (rc.isPending) return <Spinner />;
  if (rc.error) {
    return <div style={{ fontSize: 12, color: 'var(--err)' }}>Kanıt zinciri yüklenemedi — {String(rc.error)}</div>;
  }
  if (!ext) {
    return (
      <Empty icon="⏳" title="Kanıt henüz toplanmadı">
        Dış kaynak anomalisi açıldığında kanıt (SORGU 2 → trace/pod/log) ilk poll'da toplanır ve 5 dk'da bir tazelenir.
        Kaynağın enrich sorgusu tanımlı değilse yalnız metrik kanıtı gelir.
      </Empty>
    );
  }
  const c = evidenceCounts(deep);
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      {/* 1. Metrik kararı */}
      <div>
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, flexWrap: 'wrap' }}>
          <span className="pb-headline" style={{ fontSize: 24 }}>{Math.round(ext.current)}</span>
          <span className="mono" style={{ color: 'var(--text3)', fontSize: 13 }}>
            / baseline {ext.median.toFixed(1)} · MAD {ext.mad.toFixed(2)} · {ext.z.toFixed(1)}σ
          </span>
          <span className="mono" style={{ color: 'var(--text3)', fontSize: 11, marginLeft: 'auto' }}>
            {ext.source} · {ext.query} · güncellendi {fmtDateTime(new Date(ext.updatedNs / 1e6))}
          </span>
        </div>
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginTop: 6 }}>
          {keys.map((k, i) => (
            <span key={k} className="pb-pill"><span className="mono">{k}={values[i]}</span></span>
          ))}
        </div>
        {/* v0.10.898 (Oracle Aşama 3 dilim E) — özne kaynağı dürüstçe: trace'ten / ≈ öğrenilmiş / bilinmiyor. */}
        {ext.subjectNote && (
          <div style={{ fontSize: 12, color: 'var(--text3)', marginTop: 6 }}>
            özne: {ext.subjectSource === 'learned' || ext.subjectSource === 'function_code' ? '≈ ' : ''}{ext.subjectNote}
          </div>
        )}
        {ext.distributions && Object.keys(ext.distributions).length > 0 && (
          <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 4 }}>
            {Object.entries(ext.distributions).map(([field, list]) => (
              <div key={field} style={{ display: 'flex', gap: 6, flexWrap: 'wrap', alignItems: 'baseline', fontSize: 12 }}>
                <span style={{ color: 'var(--text3)', minWidth: 96 }}>{field}</span>
                {list.map(vc => <span key={vc.value} className="pb-pill"><span className="mono">{vc.value}</span> · {vc.count}</span>)}
              </div>
            ))}
          </div>
        )}
        <div style={{ marginTop: 8 }}>
          {mq.isPending ? <Spinner /> : chart ? (
            <TimeChart
              times={chart.times}
              series={[{ key: 'ext', label: ext.query, data: chart.data, color: 'var(--err)', type: 'line' }]}
              height={140}
              leftUnit=""
              hideLegend
            />
          ) : (
            <div style={{ fontSize: 12, color: 'var(--text3)' }}>Seri okunamadı — metric_points'te bu etiket kümesi için nokta yok.</div>
          )}
        </div>
      </div>

      {/* 2. Dürüstlük şeridi */}
      <div style={{ fontSize: 12, color: 'var(--text2)' }}>
        SORGU 2: {c.errors > c.rows ? `${c.errors} hata (${c.rows} satır)` : `${c.rows} satır`} · {c.traces} trace id{c.invalid > 0 ? ` (${c.invalid} geçersiz düşürüldü)` : ''} · {c.withSpans} trace CH'de bulundu · {c.pods} pod · {c.signatures} log imzası
        {(ext.notes ?? []).length > 0 && (
          <ul style={{ margin: '4px 0 0 16px', padding: 0, color: 'var(--text3)' }}>
            {ext.notes!.map((n, i) => <li key={i}>{n}</li>)}
          </ul>
        )}
      </div>

      {/* 3. Trace'ler */}
      <EvidenceBlock title="İlgili trace'ler" count={tRows.length}>
        <div className="table-wrap">
          <table {...dtT.tableProps}>
            <DataTableColgroup dt={dtT} />
            <DataTableHead dt={dtT} />
            <tbody>
              {dtT.sortedRows.length === 0 ? <DataTableState dt={dtT} kind="empty" message="Trace kanıtı yok" /> : dtT.sortedRows.map(r => (
                <tr key={r.traceId}>
                  <td className="mono">{r.startNs > 0 ? fmtDateTime(new Date(r.startNs / 1e6)) : '—'}</td>
                  <td className="mono"><Link to={traceHref(r.traceId, { tab: 'logs' })} className="sec" title={`${r.traceId} — Logs sekmesi (Oracle satırları)`}>{r.traceId.slice(0, 12)}…</Link></td>
                  <td title={`${r.rootService ?? ''} ${r.rootOp ?? ''}`}>{r.rootService ? <>{r.rootService} <span style={{ color: 'var(--text3)' }}>· {r.rootOp}</span></> : '—'}</td>
                  <td title={`${r.errorService ?? ''} ${r.errorOp ?? ''}`}>{r.errorService ? <>{r.errorService} <span style={{ color: 'var(--text3)' }}>· {r.errorOp}</span></> : '—'}</td>
                  <td className="num">{r.durationNs > 0 ? fmtDur(r.durationNs / 1e6) : '—'}</td>
                  <td className="num">{r.spans || '—'}</td>
                  <td>{r.missing
                    ? <span className="badge b-gray" title="Trace id dış kaynaktan geldi ama bu pencerede CH'de span'i yok (retention ya da henüz gelmedi)">CH'de yok</span>
                    : r.errorSpans > 0
                      ? <span className="badge b-err">{`${r.errorSpans} hata`}</span>
                      : <span style={{ color: 'var(--text3)' }}>ok</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </EvidenceBlock>

      {/* v0.10.1004 — ilgili endpoint'ler: kanıt trace'lerinin geçtiği endpoint
          (özne servis aynı kalır). Kardeş bloklar gibi hep çizilir; kanıt yoksa
          boş durumu tablonun içinde. */}
      <EvidenceBlock title="İlgili endpoint'ler" count={epRows.length}>
        <div className="table-wrap">
          <table {...dtE.tableProps}>
            <DataTableColgroup dt={dtE} />
            <DataTableHead dt={dtE} />
            <tbody>
              {dtE.sortedRows.length === 0 ? <DataTableState dt={dtE} kind="empty" message="Endpoint kanıtı yok — trace'ler Coremetry'de bulunamadı ya da giriş span'i taşımıyor" /> : dtE.sortedRows.map(r => (
                <tr key={`${r.service}|${r.path}|${r.rpc ? 'rpc' : 'http'}`}>
                  <td title={r.service}>{r.service}</td>
                  <td className="mono" title={r.path}>
                    <Link to={r.href} className="sec" title={`${r.path} — endpoint sayfası (olay penceresi)`}>{r.path}</Link>
                    {r.rpc ? <span style={{ color: 'var(--text3)' }}> · rpc</span> : null}
                  </td>
                  <td className="num">{r.traces}</td>
                  <td className="num">{r.errorTraces > 0 ? <span className="badge b-err">{r.errorTraces}</span> : <span style={{ color: 'var(--text3)' }}>0</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </EvidenceBlock>

      {/* 4. Pod'lar */}
      <EvidenceBlock title="Etkilenen pod'lar" count={pods.length}>
        <div className="table-wrap">
          <table {...dtP.tableProps}>
            <DataTableColgroup dt={dtP} />
            <DataTableHead dt={dtP} />
            <tbody>
              {dtP.sortedRows.length === 0 ? <DataTableState dt={dtP} kind="empty" message="Pod kanıtı yok — kaynak pod kimliği döndürmedi" /> : dtP.sortedRows.map(r => (
                <tr key={r.pod}>
                  <td className="mono" title={r.pod}>{r.pod}</td>
                  <td className="num">{r.count}</td>
                  <td className="mono">{r.lastSeenNs > 0 ? fmtDateTime(new Date(r.lastSeenNs / 1e6)) : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </EvidenceBlock>

      {/* 5. Log imzaları */}
      <EvidenceBlock title="Log imzaları (WARN+)" count={sigs.length}>
        <div className="table-wrap">
          <table {...dtS.tableProps}>
            <DataTableColgroup dt={dtS} />
            <DataTableHead dt={dtS} />
            <tbody>
              {dtS.sortedRows.length === 0 ? <DataTableState dt={dtS} kind="empty" message="Log imzası yok" /> : dtS.sortedRows.map(r => (
                <tr key={r.hash}>
                  <td><span className={`badge ${sevTone(r.severity)}`}>{r.severity || '—'}</span></td>
                  <td className="mono" title={r.sample}>{r.template}</td>
                  <td className="num">{r.count}</td>
                  <td className="num">{r.traceCount}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </EvidenceBlock>
    </div>
  );
}

function EvidenceBlock({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  return (
    <div>
      <div style={{ fontSize: 10.5, fontWeight: 700, letterSpacing: 0.4, textTransform: 'uppercase', color: 'var(--text3)', marginBottom: 6 }}>
        {title} <span className="mono" style={{ fontWeight: 400 }}>· {count}</span>
      </div>
      {children}
    </div>
  );
}
