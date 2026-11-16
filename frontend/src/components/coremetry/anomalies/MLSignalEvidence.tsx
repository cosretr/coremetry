// MLSignalEvidence — v0.10.1873, öğrenme döngüsü Faz 2 parti B (audit §7.1
// "Kanıt" satırları, §7.2 şablon tablosu; mockup görünüm 2–4 "Kanıt").
// Sinyale göre kanıt bölümü:
//   trace  → endpoint tablosu (spanmetrics MV; açılışta),
//   log    → yeni ve artan mesaj kalıpları (logstore; YALNIZ AÇILINCA —
//            ES maliyet disiplini, frontend-design-system §6),
//   metric → aynı servisin diğer metrikleri (ml_series; açılışta).
// Uç: GET /api/ml/{id}/evidence/{kind} (60 sn sunucu cache'i; staleTime ≥
// TTL). Cümleler (karşılaştırma, "önce → sonra") sunucudan; FE yalnız sayı
// biçimler. Tablolar coremetry DataTable; mini grafikler TimeChart (uPlot).
import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Badge } from '@/components/shadcn/badge';
import { DataTable } from '@/components/coremetry/DataTable';
import type { ColumnDef } from '@/components/coremetry/DataTable';
import { TimeChart } from '@/components/charts/TimeChart';
import { Spinner } from '@/components/Spinner';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries/keys';
import type { EndpointEvidenceRow, MetricNeighbor, MLEvidence, MLEvidenceKind, PatternEvidenceRow } from '@/lib/types';
import { t, tf, useLang, type Lang } from '@/lib/i18n'; // v0.10.2291 — kanıt bölümü metni arayüz dilinde

const EVIDENCE_STALE_MS = 60_000; // sunucu TTL'i (mlEvidenceTTL) kadar

/** SAF (test): süre biçimi (ms → "640 ms" / "2,3 sn"). */
export function fmtEvidenceMs(ms: number | undefined, lang: Lang = 'tr'): string {
  if (ms == null || !Number.isFinite(ms) || ms <= 0) return '—';
  const loc = lang === 'tr' ? 'tr-TR' : 'en-US';
  if (ms < 1000) return `${Math.round(ms).toLocaleString(loc)} ms`;
  return tf('aed.unit.s', lang, { n: (ms / 1000).toLocaleString(loc, { maximumFractionDigits: 1 }) });
}
const numLoc = (n: number, lang: Lang) => n.toLocaleString(lang === 'tr' ? 'tr-TR' : 'en-US');

function useEvidence<R>(id: string, kind: MLEvidenceKind, enabled: boolean) {
  return useQuery({
    queryKey: [...keys.anomalies.ml, 'evidence', id, kind],
    queryFn: ({ signal }) => api.mlEvidence<R>(id, kind, signal),
    enabled: enabled && !!id, staleTime: EVIDENCE_STALE_MS, retry: false,
  });
}

function stateOf<R>(q: { isLoading: boolean; error: unknown; data?: MLEvidence<R> }, empty: string, lang: Lang) {
  if (q.isLoading) return { kind: 'loading' as const };
  if (q.error) return { kind: 'error' as const, message: q.error instanceof Error ? q.error.message : String(q.error) };
  if (q.data?.degraded) return { kind: 'error' as const, message: t('mlx.ev.degraded', lang) };
  return { kind: 'empty' as const, message: empty };
}

// v0.10.2291 — kolon başlıkları arayüz dilinde (mlx.ev.*).
const endpointCols = (lang: Lang): ColumnDef<EndpointEvidenceRow>[] => [
  { id: 'route', label: 'Endpoint', flex: true, minWidth: 220, mono: true, sortValue: r => r.route, naturalDir: 'asc' },
  { id: 'calls', label: t('mlx.ev.calls', lang), width: 90, numeric: true, sortValue: r => r.calls },
  { id: 'p95', label: t('mlx.ev.p95now', lang), width: 100, numeric: true, sortValue: r => r.p95Ms, tone: r => (r.deviates ? 'err' : undefined) },
  { id: 'prev', label: t('mlx.ev.p95prev', lang), width: 100, numeric: true, sortValue: r => r.prevP95Ms ?? 0 },
  { id: 'go', label: t('mlx.go', lang), width: 90, sortValue: r => r.route },
];

function EndpointEvidence({ id }: { id: string }) {
  const q = useEvidence<EndpointEvidenceRow>(id, 'endpoints', true);
  const lang = useLang();
  const cols = useMemo(() => endpointCols(lang), [lang]);
  return (
    <div className="rounded-md border border-border bg-card" data-testid="ml-evidence-endpoints">
      <DataTable<EndpointEvidenceRow> storageKey="ml-evidence-endpoints" columns={cols} rows={q.data?.rows ?? []} ariaLabel={t('mlx.ev.endpoints.aria', lang)}
        state={stateOf(q, t('mlx.ev.endpoints.empty', lang), lang)} sort={{ initial: { id: 'calls', dir: 'desc' } }} getRowKey={r => r.route}
        renderCell={(r, c) => {
          switch (c.id) {
            case 'route': return <span title={r.route}>{r.route}{r.focus && <Badge variant="outline" className="ml-1.5">{t('mlx.ev.focus', lang)}</Badge>}</span>;
            case 'calls': return numLoc(r.calls, lang);
            case 'p95': return fmtEvidenceMs(r.p95Ms, lang);
            case 'prev': return fmtEvidenceMs(r.prevP95Ms, lang);
            case 'go': return <Link to={r.href}>Traces ↗</Link>;
            default: return null;
          }
        }} />
      <p className="m-0 border-t border-[color:var(--divider)] px-2.5 py-1.5 text-xs text-muted-foreground">
        {t('mlx.ev.prevNote', lang)}{q.data && q.data.prevOK === false ? t('mlx.ev.unread', lang) : ''}.
      </p>
    </div>
  );
}

const patternCols = (lang: Lang): ColumnDef<PatternEvidenceRow>[] => [
  { id: 'template', label: t('mlx.ev.template', lang), flex: true, minWidth: 260, mono: true, truncate: 'wrap', sortValue: r => r.template, naturalDir: 'asc' },
  { id: 'count', label: t('mlx.ev.lines', lang), width: 80, numeric: true, sortValue: r => r.count },
  { id: 'compare', label: t('mlx.ev.compare', lang), width: 200, sortValue: r => (r.new ? 1 : 0) },
  { id: 'go', label: t('mlx.go', lang), width: 100, sortValue: r => r.template },
];

function PatternEvidence({ id }: { id: string }) {
  const [open, setOpen] = useState(false);
  const q = useEvidence<PatternEvidenceRow>(id, 'patterns', open);
  const lang = useLang();
  const cols = useMemo(() => patternCols(lang), [lang]);
  return (
    <details className="rounded-md border border-border bg-card" data-testid="ml-evidence-patterns"
      onToggle={e => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary className="cursor-pointer px-2.5 py-1.5 text-xs font-semibold">{t('mlx.ev.patterns', lang)} <span className="font-normal text-muted-foreground">{t('mlx.ev.patterns.sub', lang)}</span></summary>
      {open && (
        <>
          <DataTable<PatternEvidenceRow> storageKey="ml-evidence-patterns" columns={cols} rows={q.data?.rows ?? []} ariaLabel={t('mlx.ev.patterns.aria', lang)}
            state={stateOf(q, t('mlx.ev.patterns.empty', lang), lang)} sort={{ initial: { id: 'compare', dir: 'desc' } }} getRowKey={r => r.template}
            renderCell={(r, c) => {
              switch (c.id) {
                case 'template': return (
                  <span>
                    {r.template}{r.new && <Badge variant="outline" className="ml-1.5 border-transparent bg-[color:var(--err-bg)] text-[color:var(--err)]">{t('mlx.ev.firstSeen', lang)}{r.firstSeen ? ` · ${r.firstSeen}` : ''}</Badge>}
                    {r.sample && <details className="mt-1"><summary className="cursor-pointer text-muted-foreground">{t('mlx.ev.sample', lang)}</summary><span className="block font-mono text-xs break-all">{r.sample}</span></details>}
                  </span>
                );
                case 'count': return numLoc(r.count, lang);
                case 'compare': return r.compare || <span className="text-muted-foreground">—</span>;
                case 'go': return <Link to={r.href}>{t('mlx.openLogs', lang)} ↗</Link>;
                default: return null;
              }
            }} />
          {q.data && !q.data.degraded && (
            <p className="m-0 border-t border-[color:var(--divider)] px-2.5 py-1.5 text-xs text-muted-foreground">
              {tf('mlx.ev.capNote', lang, { n: q.data.cap ?? 500 })}{q.data.baseOK === false ? t('mlx.ev.baseUnread', lang) : ''}.
            </p>
          )}
        </>
      )}
    </details>
  );
}

function MetricNeighborCard({ n, onsetSec, endSec }: { n: MetricNeighbor; onsetSec: number; endSec: number }) {
  const lang = useLang();
  return (
    <div className="rounded-md border border-border bg-card px-2.5 py-2" data-testid="ml-evidence-metric">
      <h4 className="m-0 mb-1 truncate text-xs font-semibold" title={n.label}>{n.label}</h4>
      {n.times.length >= 2 && (
        <TimeChart height={72} times={n.times} hideLegend leftUnit={n.unit ?? ''}
          regions={[{ fromSec: onsetSec, toSec: endSec, label: t('mlx.region.anomaly', lang), color: 'var(--err)' }]}
          series={[{ key: 'v', label: n.label, data: n.values, color: 'var(--sig-metric)', type: 'line', width: 1.6 }]} />
      )}
      {n.text && <p className="m-0 mt-1 text-xs">{n.text}</p>}
    </div>
  );
}

function MetricEvidence({ id }: { id: string }) {
  const q = useEvidence<MetricNeighbor>(id, 'metrics', true);
  const lang = useLang();
  if (q.isLoading) return <Spinner />;
  if (q.error) return <p className="m-0 text-sm text-muted-foreground">{t('mlx.ev.metricsErr', lang)}</p>;
  const rows = q.data?.rows ?? [];
  if (rows.length === 0) return <p className="m-0 text-sm text-muted-foreground" data-testid="ml-evidence-metrics-empty">{t('mlx.ev.metricsEmpty', lang)}</p>;
  const onset = (q.data?.from ?? 0) / 1e9, end = (q.data?.to ?? 0) / 1e9;
  return (
    <div className="grid gap-2 md:grid-cols-3" data-testid="ml-evidence-metrics">
      {rows.map(n => <MetricNeighborCard key={n.part} n={n} onsetSec={onset} endSec={end} />)}
    </div>
  );
}

/** SAF (test): sinyal → kanıt türü (bilinmeyen sinyalde bölüm yok). */
export function evidenceKindOf(signal: string): MLEvidenceKind | null {
  return signal === 'trace' ? 'endpoints' : signal === 'log' ? 'patterns' : signal === 'metric' ? 'metrics' : null;
}

export function MLSignalEvidence({ id, signal }: { id: string; signal: string }) {
  switch (evidenceKindOf(signal)) {
    case 'endpoints': return <EndpointEvidence id={id} />;
    case 'patterns': return <PatternEvidence id={id} />;
    case 'metrics': return <MetricEvidence id={id} />;
    default: return null;
  }
}
