// SREEvalCompare — v0.10.1450 — AI SRE program 2a: seçili koşu bir yeniden
// koşuysa (rerunOf) taban ↔ bu koşu karşılaştırması (/ai › SREEvalPanel).
//
//   - Manşet satırı: "bağımsız" vakalar (onaylayan CoSRE önerisini aynen
//     kabul ETMEDİ — operatör kararı 2); ikinci satır tüm vakalar (kontrol:
//     deterministik yol aynı → Δ ≈ 0 beklenir, Δ ≠ 0 veri kayması / gerileme).
//   - Kalibrasyon: depolanan skor + kapsamdan deterministik sonuç; maliyet
//     (tekrar LLM'siz); uyum (top-1 türü / sonuç aynı mı).
//   - Vaka çifti tablosu (useDataTable, ≤50 satır); satır problemi açar.
// Hesap Go'da (sre.CompareEvalRuns); burada yalnız biçim. Kendi yoklaması yok.
import { Link } from 'react-router';
import { Badge, StatTile } from '@/components/ui';
import { stopRowClick } from '@/lib/a11y';
import { useSREEvalCompare } from '@/lib/queries/sreEval';
import type { SREEvalCompareBlock, SREEvalPackSide, SREEvalPair, SREEvalPairSide } from '@/lib/types';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef, type DataTable } from '@/components/ui/DataTable';
import { HYP_KIND_LABEL, PAIR_HIT_LABEL, categoryLabel, coveragePct, detOutcomeText, durationTR, evalDelta, evalPct, ratioText } from './sreEvalView';

// v0.10.1494 — paketli tarafın özeti: ort./en çok token, kırpılan vaka, okuyucu kapsamı.
function packSideText(s: SREEvalPackSide): string {
  if (s.cases === 0) return 'künyeli vaka yok';
  return `${s.cases} vaka · ort ${s.avgTokens ?? '—'} / en çok ${s.maxTokens} token · kırpılan ${s.trimmedCases} · okuyucu veri ${s.readerData} / yok ${s.readerNoData} / hata ${s.readerFailed}`;
}

const HIT_TONE = { top1: 'success', top3: 'info', miss: 'warning', unscored: 'neutral' } as const;
const hitRank = (s: SREEvalPairSide) => (s.hit === 'top1' ? 3 : s.hit === 'top3' ? 2 : s.hit === 'miss' ? 1 : 0);

const PAIR_COLS: ColumnDef<SREEvalPair>[] = [
  { id: 'problem',  label: 'Problem',  sortValue: p => p.displayId || p.problemId, naturalDir: 'asc', width: 110, minWidth: 90, priority: 1 },
  { id: 'service',  label: 'Servis',   sortValue: p => p.service, naturalDir: 'asc', width: 150, minWidth: 100, priority: 2 },
  { id: 'expected', label: 'Beklenen', sortValue: p => p.category, naturalDir: 'asc', width: 115, minWidth: 90, priority: 1 },
  { id: 'base',     label: 'Taban top-1', sortValue: p => hitRank(p.base), flex: true, minWidth: 170, priority: 1 },
  { id: 'cand',     label: 'Bu koşu top-1', sortValue: p => hitRank(p.cand), flex: true, minWidth: 170, priority: 1 },
  { id: 'outcome',  label: 'Det. sonuç (taban → bu)', sortValue: p => p.cand.detOutcome?.value ?? '', naturalDir: 'asc', width: 210, minWidth: 150, priority: 2 },
  { id: 'cause',    label: 'Onaylı neden', sortValue: p => p.finalCause, naturalDir: 'asc', width: 200, minWidth: 130, priority: 3 },
  { id: 'flags',    label: 'Not', sortValue: p => (p.coverageDrift ? 2 : 0) + (p.acceptedSuggestion ? 1 : 0), width: 150, minWidth: 110, priority: 2 },
];

function BlockTiles({ title, b, hint }: { title: string; b: SREEvalCompareBlock; hint: string }) {
  const row = (label: string, base: number | null, cand: number | null, d: number | null, n: string, pct = evalPct) => (
    <StatTile label={`${title} · ${label}`}>
      <span className="mono">{pct(base)} → {pct(cand)}</span>{' '}
      <span className={d ? 'field-hint' : 'field-hint cell-empty'}>Δ {evalDelta(d, pct === evalPct ? 100 : 1)} · {n}</span>
    </StatTile>
  );
  return (
    <div className="stat-grid" title={hint}>
      {row('Top-1', b.base.top1Acc, b.cand.top1Acc, b.deltaTop1, `${b.base.top1Hits}/${b.base.scored} → ${b.cand.top1Hits}/${b.cand.scored}`)}
      {row('Top-3', b.base.top3Acc, b.cand.top3Acc, b.deltaTop3, `${b.base.top3Hits}/${b.base.scored} → ${b.cand.top3Hits}/${b.cand.scored}`)}
      {row('Varlık', b.base.entityAcc, b.cand.entityAcc, b.deltaEntity, `${b.base.entityHits}/${b.base.entityScored} → ${b.cand.entityHits}/${b.cand.entityScored}`)}
      {row('Kapsam', b.base.coveragePct, b.cand.coveragePct, b.deltaCoverage, `${b.cases} vaka`, coveragePct)}
    </div>
  );
}

function SideCell({ s, expected }: { s: SREEvalPairSide; expected: string }) {
  if (!s.top1) return <span className="cell-empty">{PAIR_HIT_LABEL[s.hit]}</span>;
  return (
    <span className="dt-trunc">
      <Badge tone={s.top1.category === expected ? 'success' : 'neutral'} title={`${categoryLabel(s.top1.category)} · skor ${s.top1.score.toFixed(2)}`}>
        {HYP_KIND_LABEL[s.top1.kind] ?? s.top1.kind}
      </Badge>{' '}
      <Badge tone={HIT_TONE[s.hit]}>{PAIR_HIT_LABEL[s.hit]}</Badge>
    </span>
  );
}

function PairRow({ p, dt }: { p: SREEvalPair; dt: DataTable<SREEvalPair> }) {
  return (
    <tr>
      <DataTableCell dt={dt} col="problem" row={p}>
        <Link to={`/problems?problem=${encodeURIComponent(p.problemId)}`} onClick={stopRowClick} className="mono">{p.displayId || p.problemId}</Link>
      </DataTableCell>
      <DataTableCell dt={dt} col="service" row={p} value={p.service || null} />
      <DataTableCell dt={dt} col="expected" row={p} value={categoryLabel(p.category)} />
      <DataTableCell dt={dt} col="base" row={p}><SideCell s={p.base} expected={p.category} /></DataTableCell>
      <DataTableCell dt={dt} col="cand" row={p}><SideCell s={p.cand} expected={p.category} /></DataTableCell>
      <DataTableCell dt={dt} col="outcome" row={p} value={`${detOutcomeText(p.base.detOutcome)} → ${detOutcomeText(p.cand.detOutcome)}`} />
      <DataTableCell dt={dt} col="cause" row={p} value={p.finalCause || null} title={p.finalCause || undefined} />
      <DataTableCell dt={dt} col="flags" row={p}>
        {p.coverageDrift && <Badge tone="warning" title="iki koşuda veri döndüren adım sayısı farklı (saklama / veri kayması)">kayma</Badge>}{' '}
        {p.acceptedSuggestion && <Badge tone="neutral" title="onaylayan CoSRE önerisini değiştirmeden kabul etti — manşet dışı">öneri aynen</Badge>}
      </DataTableCell>
    </tr>
  );
}

export function SREEvalCompare({ base, cand }: { base: string; cand: string }) {
  const q = useSREEvalCompare(base, cand);
  const c = q.data;
  const dt = useDataTable<SREEvalPair>({ storageKey: 'ai-sre-eval-compare', columns: PAIR_COLS, rows: c?.pairs ?? [] });
  const cb = c?.calibration.base;
  const cc = c?.calibration.cand;
  return (
    <>
      <div className="ov-card-h sre-eval-sec">
        <h3>Taban → Bu koşu</h3>
        <span className="ov-sub">{c?.note ?? 'aynı dondurulmuş küme, aynı çıpalar'}</span>
      </div>
      {q.isError && <p className="field-error" role="alert">{q.error instanceof Error ? q.error.message.replace(/^HTTP \d+: /, '') : 'karşılaştırılamadı'}</p>}
      {c && (
        <>
          <BlockTiles title={c.independentKnown ? 'Bağımsız (manşet)' : 'Bağımsız — bilinmiyor'} b={c.independent}
            hint="onaylayanın CoSRE önerisini aynen kabul etmediği vakalar" />
          <BlockTiles title="Tüm vakalar (kontrol)" b={c.control} hint="deterministik yol aynı: Δ ≠ 0 veri kayması ya da gerileme" />
          <div className="stat-grid">
            <StatTile label="Kesin + doğru (det. sonuç)">
              <span className="mono">{ratioText(cb?.confirmedPrecision)} → {ratioText(cc?.confirmedPrecision)}</span>
            </StatTile>
            <StatTile label="Kaçınca çekimser">
              <span className="mono">{ratioText(cb?.abstainOnMiss)} → {ratioText(cc?.abstainOnMiss)}</span>
            </StatTile>
            <StatTile label="Kendinden emin yanlış" tone={(cc?.confidentWrong ?? 0) > 0 ? 'warn' : undefined}>
              <span className="mono">{cb?.confidentWrong ?? 0} → {cc?.confidentWrong ?? 0}</span>
            </StatTile>
            <StatTile label="Maliyet">
              <span className="mono">{c.cost.cand.llmFree ? 'LLM 0' : `LLM ${c.cost.cand.llmCalls}`}</span>{' '}
              <span className="field-hint">{c.cost.cand.replayed} tekrar · süre {durationTR(c.cost.base.wallMs)} → {durationTR(c.cost.cand.wallMs)}</span>
            </StatTile>
            {c.pack && (c.pack.base.on || c.pack.cand.on) && (
              <StatTile label="Bağlam paketi (taban → bu)">
                <span className="mono">{c.pack.base.on ? 'açık' : 'kapalı'} → {c.pack.cand.on ? 'açık' : 'kapalı'}</span>{' '}
                <span className="field-hint">
                  {packSideText(c.pack.cand.on ? c.pack.cand : c.pack.base)}
                </span>
              </StatTile>
            )}
            <StatTile label="Uyum (top-1 / sonuç aynı)">
              <span className="mono">{ratioText(c.agreement.top1Same)} · {ratioText(c.agreement.detOutcomeSame)}</span>{' '}
              <span className="field-hint">{c.driftCases.length ? `kayma ${c.driftCases.length} vaka` : 'kayma yok'}</span>
            </StatTile>
          </div>
        </>
      )}
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.length === 0
              ? <DataTableState dt={dt} {...(q.isError ? { kind: 'error' as const } : c === undefined ? { kind: 'loading' as const } : { kind: 'empty' as const, message: 'Kümede vaka yok' })} />
              : dt.sortedRows.map(p => <PairRow key={p.caseId || p.problemId} p={p} dt={dt} />)}
          </tbody>
        </table>
      </div>
    </>
  );
}
