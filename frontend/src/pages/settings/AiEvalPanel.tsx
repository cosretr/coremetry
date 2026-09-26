// AiEvalPanel — v0.10.940 (operatör onayı 2026-09-26, mockup
// coremetry-evalset.html + K1–K3): Settings › CoSRE › Değerlendirme.
// Gömülü evalset vakalarını SUNUCUDA, üretimle aynı yoldan koşturan panel:
// yapılandırılmış profil(ler), aynı istemler, aynı puanlama (içerik /
// uydurma ad / niyet / RCA hükmü / rubrik). Operatör: "Ücretli sağlayıcı yol
// local llm modeline bağlıyız" → onay adımı, ücretli sağlayıcı diyaloğu
// YOK; Koş tek tık. Çağrılar /ai'da `evalset-*` yüzeyiyle ayrı etiketlenir
// (K2) — üretim sayılarını şişirmez; link aşağıda (?source=evalset).
//
// URL tek doğruluk kaynağı (frontend-conventions §4): `?tab=eval` AiTab'ın,
// `?run=` seçili koşu, `?case=` vaka çekmecesi, `?cmp=` karşılaştırma
// tabanı — hepsi replace ile yazılır ve DOĞRUDAN URL'den türetilir (yerel
// kopya yok → içe aktarma efekti ve imza koruması da gereksiz). Yerel state
// yalnız yüzey çipleri (bir sonraki koşunun FORMU, görünüm değil) ve
// eylem geri bildirimi.
//
// Yoklama queries/aiEval.ts'te: yalnız bir koşu `running` iken 10 s.
// Tablolar tablo standardında (dt.tableProps / DataTableCell / DataTableHead /
// DataTableState): satır içi hücre stili, sayı hücresinde mono, `is-fit`
// YOK — tableUnityRatchet tavanda, bu dosya hiçbir sayaca bir şey eklemez.
// Saf kararlar (seçim, Fark, profil, süre, gruplama) aiEval.ts'te, testli.
import { useEffect, useMemo, useState } from 'react';
import { Link, useLocation, useSearchParams } from 'react-router-dom';
import { Spinner, Empty } from '@/components/Spinner';
import { QueryErrorInline } from '@/components/QueryError';
import { Badge, Button, Card, Chip, Drawer, DrawerSection, KeyValue, SectionHead, SelectField } from '@/components/ui';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState,
  type ColumnDef, type DataTableStateKind,
} from '@/components/ui/DataTable';
import { useEvalsetCatalog, useEvalRuns, useEvalRun, useEvalCompare, useStartEvalRun, useCancelEvalRun } from '@/lib/queries';
import type { EvalCaseResult, EvalCompare, EvalRunSummary, EvalSurfaceSummary } from '@/lib/types';
import { FlashBox, humanize } from './shared';
import {
  activeRun, caseReason, compareCandidates, compareGroups, compareSummary, evalHref, expectLines, failingCases,
  fmtDec2, fmtRunTime, fmtSec1, fmtSignedInt, isFinished, passDeltas, pickDefaultProfile, pickDefaultRunId,
  progressPct, progressText, routingHint, runStatusView, selectionCaseCount, toggleSurface, withEvalParams,
} from './aiEval';

// v0.10.940 — koşu satırı + "Fark" (passDeltas). Fark satıra gömülü: kolon
// tanımı modül düzeyinde sabit kalsın (kimliği değişen kolon dizisi
// columnLayoutSig'i her render tazelerdi — operatörün genişlikleri kaçar).
type RunRow = EvalRunSummary & { delta: number | null };

// Yüzey özeti (T4: sayılar sağa yaslı, arayüz fontunda; T9: renk yalnız
// Kaldı > 0'da). Uydurma ad null = ölçülmedi → DataTableCell soluk "—".
const SURFACE_COLS: ColumnDef<EvalSurfaceSummary>[] = [
  { id: 'surface', label: 'Yüzey',        sortValue: r => r.surface, naturalDir: 'asc', width: 220, flex: true },
  { id: 'pass',    label: 'Geçti',        sortValue: r => r.pass, numeric: true, width: 90 },
  { id: 'fail',    label: 'Kaldı',        sortValue: r => r.fail, numeric: true, width: 90, tone: r => (r.fail > 0 ? 'err' : undefined) },
  { id: 'unknown', label: 'Uydurma ad',   sortValue: r => r.unknownEntities ?? -1, numeric: true, width: 120 },
  { id: 'latency', label: 'Ort. süre sn', sortValue: r => r.avgLatencyMs, numeric: true, width: 120 },
];

// Kalan vakalar: kimlik mono (T5), gerekçe sarar (T11 'wrap'). Satır =
// gerçek link (?case=) — orta tık yeni sekmede aynı çekmece (T7).
const CASE_COLS: ColumnDef<EvalCaseResult>[] = [
  { id: 'id',      label: 'Vaka',  sortValue: c => c.id, naturalDir: 'asc', mono: true, width: 320 },
  { id: 'surface', label: 'Yüzey', sortValue: c => c.surface, naturalDir: 'asc', width: 150 },
  { id: 'reason',  label: 'Neden', sortValue: c => caseReason(c), naturalDir: 'asc', truncate: 'wrap', width: 360, flex: true },
];

// Koşu geçmişi: Sürüm/Model kimlik gibi (mono); Durum nokta + metin (T9);
// Fark: eksi kırmızı, artı --text2, kıyas yoksa soluk "—".
const RUN_COLS: ColumnDef<RunRow>[] = [
  { id: 'started', label: 'Zaman', sortValue: r => r.startedAt, width: 150 },
  { id: 'version', label: 'Sürüm', sortValue: r => r.appVersion, naturalDir: 'asc', mono: true, width: 120 },
  { id: 'model',   label: 'Model', sortValue: r => r.model, naturalDir: 'asc', mono: true, width: 180, flex: true },
  { id: 'status',  label: 'Durum', sortValue: r => r.status, naturalDir: 'asc', width: 140, tone: r => runStatusView(r.status).tone },
  { id: 'pass',    label: 'Geçti', sortValue: r => r.pass, numeric: true, width: 100 },
  { id: 'delta',   label: 'Fark',  sortValue: r => r.delta, numeric: true, width: 80,
    tone: r => (r.delta === null ? undefined : r.delta < 0 ? 'err' : r.delta > 0 ? 'muted' : undefined) },
];

interface TableStateView { kind: DataTableStateKind; message?: string; onRetry?: () => void }

export function AiEvalPanel() {
  const [searchParams, setSearchParams] = useSearchParams();
  const { pathname } = useLocation();
  const catalogQ = useEvalsetCatalog();
  const runsQ = useEvalRuns();
  const startM = useStartEvalRun();
  const cancelM = useCancelEvalRun();
  // Bir sonraki koşunun yüzey seçimi ([] = tümü). URL'de değil: görünümü
  // değiştirmiyor, yalnız Koş'un gövdesini — form alanı gibi.
  const [picked, setPicked] = useState<string[]>([]);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const catalog = catalogQ.data;
  const surfaces = useMemo(() => catalog?.surfaces ?? [], [catalog]);
  const runs = useMemo(() => runsQ.data ?? [], [runsQ.data]);
  const active = activeRun(runs);

  const runParam = searchParams.get('run');
  const caseParam = searchParams.get('case');
  const cmpParam = searchParams.get('cmp');
  const selectedId = runParam || pickDefaultRunId(runs);
  const detailQ = useEvalRun(selectedId);
  // v0.10.940 — global `placeholderData: keepPreviousData` (main.tsx):
  // koşu değişince ÖNCEKİ koşunun vakaları yer tutucu olarak gelir. Kimliği
  // tutmayan veri yok sayılır — başka koşunun vakaları bu koşunun başlığı
  // altında bir an bile görünmesin.
  const detail = detailQ.data?.run?.id === selectedId ? detailQ.data : undefined;
  // Liste satırı önce: iki sorgu aynı aralıkla yoklar, özet için listenin
  // durumu yeterli; listede olmayan (21. ve daha eski) koşu detaydan gelir.
  const listRun = runs.find(r => r.id === selectedId) ?? null;
  const selectedRun: EvalRunSummary | null = listRun ?? detail?.run ?? null;

  // v0.10.940 — liste koşunun bittiğini detaydan ÖNCE görürse detayı hemen
  // tazele: son vakalar + tam `cases` kaydı bir yoklama turu beklemesin.
  // Bağımlılık durum dizgeleri; aynı durumda tekrar tetiklenmez (döngü yok).
  const listStatus = listRun?.status;
  const detailStatus = detail?.run?.status;
  const refetchDetail = detailQ.refetch;
  useEffect(() => {
    if (listStatus && detailStatus && listStatus !== detailStatus) void refetchDetail();
  }, [listStatus, detailStatus, refetchDetail]);

  const setParams = (patch: Record<string, string | null>) =>
    setSearchParams(prev => withEvalParams(prev, patch), { replace: true });

  const failing = useMemo(() => failingCases(detail?.cases ?? []), [detail]);
  const surfaceRows = useMemo(() => selectedRun?.bySurface ?? [], [selectedRun]);
  const runRows = useMemo<RunRow[]>(() => {
    const d = passDeltas(runs);
    return runs.map(r => ({ ...r, delta: d[r.id] ?? null }));
  }, [runs]);
  // Seçili koşu tablonun TEK seçili görünümünü alır (T2, row-selected).
  const runSelection = useMemo(() => ({
    mode: 'single' as const,
    getRowId: (r: RunRow) => r.id,
    value: new Set(selectedId ? [selectedId] : []),
  }), [selectedId]);

  const surfaceDt = useDataTable<EvalSurfaceSummary>({
    storageKey: 'settings-ai-eval-surfaces', columns: SURFACE_COLS, rows: surfaceRows,
  });
  const caseDt = useDataTable<EvalCaseResult>({
    storageKey: 'settings-ai-eval-cases', columns: CASE_COLS, rows: failing,
    getRowHref: c => evalHref(pathname, searchParams, { run: selectedId, case: c.id }),
    rowLinkReplace: true,
  });
  const runDt = useDataTable<RunRow>({
    storageKey: 'settings-ai-eval-runs', columns: RUN_COLS, rows: runRows,
    initialSort: { id: 'started', dir: 'desc' },
    // Koşu değişince açık çekmece kapanır; taban hedefle aynı olamaz.
    getRowHref: r => evalHref(pathname, searchParams, { run: r.id, case: null, cmp: cmpParam === r.id ? null : cmpParam }),
    rowLinkReplace: true,
    selection: runSelection,
  });

  // ── Karşılaştırma: seçili (bitmiş) koşu HEDEF, seçicideki koşu TABAN ──
  const selectedFinished = !!selectedRun && isFinished(selectedRun.status);
  const candidates = useMemo(() => compareCandidates(runs, selectedId), [runs, selectedId]);
  const cmpBase = selectedFinished && cmpParam && cmpParam !== selectedId ? cmpParam : null;
  const cmpQ = useEvalCompare(cmpBase, selectedFinished ? selectedId : null);
  const cmp = cmpQ.data && cmpQ.data.base?.id === cmpBase && cmpQ.data.head?.id === selectedId ? cmpQ.data : undefined;

  // ── Başlık: varsayılan profil + ondan ayrılan yüzeyler ──
  // v0.10.940 — kataloğun ŞİMDİKİ varsayılanı; son koşunun profili değil
  // (o, koşunun başladığı anın yönlendirmesi — bkz. pickDefaultProfile).
  const prof = pickDefaultProfile(catalog);
  const routed = prof ? routingHint(surfaces, prof.profileId) : '';
  const order = useMemo(() => surfaces.map(s => s.surface), [surfaces]);
  const selCount = selectionCaseCount(surfaces, picked);

  // Koş'un kapalı olma SEBEBİ düğmenin yanında yazılı (devre dışı düğme
  // sebepsiz kalmasın). Sıra: AI hazır değil > koşu sürüyor > boş seçim.
  const blockReason = !catalog ? ''
    : !catalog.ready ? 'CoSRE yapılandırılmamış ya da kapalı — Sağlayıcı ve profiller sekmesinden aç.'
    : active ? 'Bir koşu sürüyor — bitince ya da durdurunca yeniden koşabilirsin.'
    : selCount === 0 ? 'Seçili yüzeylerde vaka yok.'
    : '';
  const canRun = !!catalog && !blockReason;

  const start = () => {
    setFlash(null);
    startM.mutate(picked, {
      // Yeni koşu seçili olsun: açık ?run= / ?case= / ?cmp= eski koşuya aitti.
      onSuccess: () => setParams({ run: null, case: null, cmp: null }),
      onError: e => setFlash({ kind: 'err', text: humanize(e) }),
    });
  };
  const cancel = () => {
    if (!active) return;
    setFlash(null);
    cancelM.mutate(active.id, {
      onSuccess: () => setFlash({ kind: 'ok', text: 'Durdurma istendi — koşu birkaç saniye içinde «Durduruldu» olur.' }),
      // 409: koşu bu sunucuda sürmüyor ya da bitti — sunucunun metni aynen.
      onError: e => setFlash({ kind: 'err', text: humanize(e) }),
    });
  };

  // ── Tablo durumları (T12: tablonun İÇİNDE, başlık kalır) ──
  const noRunsYet: TableStateView = { kind: 'empty', message: 'Henüz koşu yok — Koş ile başlat' };
  const runsState: TableStateView = runsQ.isPending ? { kind: 'loading' }
    : runsQ.isError ? { kind: 'error', message: humanize(runsQ.error), onRetry: () => { void runsQ.refetch(); } }
    : noRunsYet;
  const summaryState: TableStateView = selectedRun
    ? { kind: 'empty', message: selectedRun.status === 'running'
        ? 'Bu koşunun henüz yüzey özeti yok — ilk vaka bitince dolar'
        : 'Bu koşu yüzey özeti bırakmadı' }
    : runParam && detailQ.isError ? { kind: 'error', message: humanize(detailQ.error), onRetry: () => { void detailQ.refetch(); } }
    : runParam && detailQ.isPending ? { kind: 'loading' }
    : runsState;
  const casesState: TableStateView = !selectedId ? runsState
    : !detail ? (detailQ.isError
      ? { kind: 'error', message: humanize(detailQ.error), onRetry: () => { void detailQ.refetch(); } }
      : { kind: 'loading' })
    : { kind: 'empty', message: emptyCasesText(detail.run, (detail.cases ?? []).length) };

  const openCase = caseParam ? (detail?.cases ?? []).find(c => c.id === caseParam) ?? null : null;
  const caseHref = (id: string) => evalHref(pathname, searchParams, { run: selectedId, case: id });

  return (
    <div className="evs-panel">
      <Card
        header="Değerlendirme (evalset)"
        footer={<Link to="/ai?source=evalset">Bu koşuların çağrıları /ai'da, Kaynak: Değerlendirme</Link>}>
        <p className="evs-intro">
          Gömülü evalset vakalarını üretimle aynı yoldan — yapılandırılmış profil(ler), aynı sistem
          istemleri — sunucuda sırayla koşturur ve deterministik puanlar (içerik, uydurma ad, niyet,
          RCA hükmü, rubrik). Çağrılar /ai'da ayrı etiketlenir; üretim sayılarını ve bütçeyi şişirmez.
        </p>
        {catalogQ.isPending && <Spinner label="Katalog yükleniyor" />}
        {catalogQ.isError && <QueryErrorInline text={humanize(catalogQ.error)} onRetry={() => { void catalogQ.refetch(); }} />}
        {catalog && (
          <>
            <KeyValue items={[
              { id: 'profile', k: 'Profil', v: prof ? `${prof.profileLabel || prof.profileId} (${prof.provider})` : null, title: prof?.profileId },
              { id: 'model', k: 'Model', v: prof?.model, mono: true },
              { id: 'endpoint', k: 'Uç', v: prof?.baseUrl || prof?.provider, mono: !!prof?.baseUrl },
              { id: 'prompt', k: 'Prompt sürümü', v: catalog.promptVersion, mono: true, title: `uygulama ${catalog.appVersion}` },
            ]} />
            {routed && <div className="field-hint evs-note">Farklı profile yönlenen: {routed}</div>}
            <div className="evs-chips" role="group" aria-label="Koşulacak yüzeyler">
              <Chip pill active={picked.length === 0} onClick={() => setPicked([])}>Tümü {catalog.total}</Chip>
              {surfaces.map(s => (
                <Chip key={s.surface} pill active={picked.includes(s.surface)}
                  onClick={() => setPicked(p => toggleSurface(p, s.surface, order))}>
                  {s.surface} {s.cases}
                </Chip>
              ))}
            </div>
          </>
        )}
        <div className="evs-actions">
          <Button variant="primary" onClick={start} disabled={!canRun} loading={startM.isPending}>Koş</Button>
          {active && (
            <Button variant="secondary" onClick={cancel} loading={cancelM.isPending}>Durdur</Button>
          )}
          <span className="field-hint">{blockReason || (catalog ? `${selCount} vaka · sırayla koşar` : '')}</span>
        </div>
        {/* v0.10.940 — süre son yoklamanın saatine göre (dataUpdatedAt): ayrı
            bir saniye sayacı (setInterval) yok; 10 s'lik yoklama zaten
            yeniden çiziyor ve gizli sekmede RQ ile birlikte durur. */}
        {active && <RunProgress run={active} nowMs={runsQ.dataUpdatedAt || Date.now()} />}
        {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
      </Card>

      <SectionHead title={`${runParam ? 'Seçili koşu' : 'Son koşu'} · yüzey özeti`}
        meta={selectedRun ? runMeta(selectedRun) : undefined} />
      {selectedRun?.status === 'failed' && selectedRun.error && (
        <div className="is-err evs-note">Koşu başarısız: {selectedRun.error}</div>
      )}
      <div className="table-wrap">
        <table {...surfaceDt.tableProps}>
          <DataTableColgroup dt={surfaceDt} />
          <DataTableHead dt={surfaceDt} />
          <tbody>
            {surfaceDt.sortedRows.length ? surfaceDt.sortedRows.map((r, i) => (
              <tr key={r.surface} {...surfaceDt.rowProps(i, r)}>
                <DataTableCell dt={surfaceDt} col="surface" row={r} value={r.surface} />
                <DataTableCell dt={surfaceDt} col="pass" row={r} value={r.pass} />
                <DataTableCell dt={surfaceDt} col="fail" row={r} value={r.fail} />
                <DataTableCell dt={surfaceDt} col="unknown" row={r} value={r.unknownEntities} />
                <DataTableCell dt={surfaceDt} col="latency" row={r} value={fmtSec1(r.avgLatencyMs)} />
              </tr>
            )) : <DataTableState dt={surfaceDt} {...summaryState} />}
          </tbody>
        </table>
      </div>

      <SectionHead title="Kalan vakalar" meta={detail ? `${failing.length} vaka` : undefined} />
      <div className="table-wrap">
        <table {...caseDt.tableProps}>
          <DataTableColgroup dt={caseDt} />
          <DataTableHead dt={caseDt} />
          <tbody>
            {caseDt.sortedRows.length ? caseDt.sortedRows.map((c, i) => (
              <tr key={c.id} {...caseDt.rowProps(i, c)}>
                {/* T9/S4 — nokta tek başına durum taşımaz: bölüm başlığı
                    ("Kalan") ve gerekçe metni yanında; ekran okuyucuya ayrıca. */}
                <DataTableCell dt={caseDt} col="id" row={c} value={c.id}>
                  <span className="evs-dot status-dot status-dot-outage" aria-hidden="true" />
                  {c.id}
                  <span className="sr-only">{c.error ? ' — hata' : ' — kaldı'}</span>
                </DataTableCell>
                <DataTableCell dt={caseDt} col="surface" row={c} value={c.surface} />
                <DataTableCell dt={caseDt} col="reason" row={c} value={caseReason(c)} />
              </tr>
            )) : <DataTableState dt={caseDt} {...casesState} />}
          </tbody>
        </table>
      </div>

      <SectionHead title="Koşu geçmişi ve karşılaştırma"
        meta={runs.length ? `son ${runs.length} koşu · en çok 20 tutulur` : undefined} />
      <div className="table-wrap">
        <table {...runDt.tableProps}>
          <DataTableColgroup dt={runDt} />
          <DataTableHead dt={runDt} />
          <tbody>
            {runDt.sortedRows.length ? runDt.sortedRows.map((r, i) => {
              const view = runStatusView(r.status);
              return (
                <tr key={r.id} {...runDt.rowProps(i, r)}>
                  <DataTableCell dt={runDt} col="started" row={r} value={fmtRunTime(r.startedAt)}
                    title={`${r.id} · ${r.startedBy || '—'}`} />
                  <DataTableCell dt={runDt} col="version" row={r} value={r.appVersion}
                    title={`prompt ${r.promptVersion || '—'}`} />
                  <DataTableCell dt={runDt} col="model" row={r} value={r.model} />
                  <DataTableCell dt={runDt} col="status" row={r} value={view.label}>
                    <span className={view.dot} aria-hidden="true" />{view.label}
                  </DataTableCell>
                  <DataTableCell dt={runDt} col="pass" row={r} value={`${r.pass} / ${r.total}`} />
                  <DataTableCell dt={runDt} col="delta" row={r} value={r.delta === null ? null : fmtSignedInt(r.delta)} />
                </tr>
              );
            }) : <DataTableState dt={runDt} {...runsState} />}
          </tbody>
        </table>
      </div>

      <div className="evs-cmp-pick">
        <SelectField label="Şununla karşılaştır" value={cmpBase ?? ''}
          onChange={e => setParams({ cmp: e.target.value || null })}
          disabled={!selectedFinished || candidates.length === 0}
          hint={cmpHint(selectedRun, candidates.length)}>
          <option value="">— seç —</option>
          {candidates.map(r => (
            <option key={r.id} value={r.id}>
              {`${fmtRunTime(r.startedAt)} · ${r.appVersion || '—'} · ${r.model || '—'} · ${r.pass} / ${r.total}`}
            </option>
          ))}
        </SelectField>
      </div>
      {cmpBase && (
        cmp ? <CompareView cmp={cmp} hrefFor={caseHref} />
          : cmpQ.isError ? <QueryErrorInline text={humanize(cmpQ.error)} onRetry={() => { void cmpQ.refetch(); }} />
          : <Spinner label="Karşılaştırılıyor" />
      )}

      {caseParam && (
        <Drawer onClose={() => setParams({ case: null })} width={680} ariaLabel="Vaka ayrıntısı"
          header={<CaseHeader id={caseParam} c={openCase} />}>
          {openCase ? <CaseBody c={openCase} />
            : detail ? (
              <Empty compact icon="◇" title="Vaka bu koşuda yok">
                Bağlantı başka bir koşuya ait olabilir; koşu geçmişinden seç.
              </Empty>
            )
            : detailQ.isError ? <QueryErrorInline text={humanize(detailQ.error)} onRetry={() => { void detailQ.refetch(); }} />
            : <Spinner label="Vaka yükleniyor" />}
        </Drawer>
      )}
    </div>
  );
}

/** v0.10.940 — karşılaştırma seçicisinin ipucu, seçicinin NEDEN kapalı
 *  olduğunu duruma göre söyler: sürmekte olan koşu bitince kıyaslanır;
 *  başarısız / yarım kalan koşu hiç kıyaslanmaz — "bitince" demek, hiç
 *  gelmeyecek bir anı bekletirdi. */
function cmpHint(run: EvalRunSummary | null, candidateCount: number): string | undefined {
  if (!run) return undefined;
  if (run.status === 'running') return 'Seçili koşu bitince karşılaştırılabilir.';
  if (!isFinished(run.status)) return 'Başarısız ya da yarım kalan koşu karşılaştırılamaz — bitmiş bir koşu seç.';
  if (candidateCount === 0) return 'Karşılaştırmak için bitmiş başka bir koşu gerekir.';
  return `Hedef seçili koşu (${fmtRunTime(run.startedAt)}); seçtiğin koşu taban.`;
}

/** Bölüm başlığının sağ ucu: zaman · durum · geçen · rubrik ortalaması. */
function runMeta(r: EvalRunSummary): string {
  const parts = [fmtRunTime(r.startedAt), runStatusView(r.status).label, `${r.pass} / ${r.total} geçti`];
  if (r.rubricMean > 0) parts.push(`rubrik ${fmtDec2(r.rubricMean)}`);
  return parts.join(' · ');
}

/** v0.10.940 — "Kalan vakalar" boşken NEDEN boş olduğunu söyle: koşu başka
 *  sunucuda sürüyorsa ara sonuçlar orada (sunucu `cases: []` döner) —
 *  "kalan vaka yok" demek yanlış bir güven verirdi. */
function emptyCasesText(run: EvalRunSummary, caseCount: number): string {
  if (run.status === 'running') {
    return caseCount === 0 && run.done > 0
      ? 'Ara sonuçlar koşuyu yürüten sunucuda — koşu bitince burada'
      : 'Şimdiye dek kalan vaka yok';
  }
  if (run.status === 'failed') return 'Koşu başarısız bitti — vaka sonucu yok';
  if (run.status === 'abandoned') return 'Koşu yarım kaldı — vaka sonucu kaydedilmedi';
  return 'Kalan vaka yok — koşulan her vaka geçti ya da atlandı';
}

function RunProgress({ run, nowMs }: { run: EvalRunSummary; nowMs: number }) {
  const pct = progressPct(run);
  return (
    <div className="evs-progress-row">
      <div className="evs-progress" role="progressbar" aria-label="Değerlendirme ilerlemesi"
        aria-valuemin={0} aria-valuemax={run.total} aria-valuenow={run.done}
        aria-valuetext={`${run.done} / ${run.total} vaka`}>
        <span className="evs-progress__fill" style={{ width: `${pct}%` }} />
      </div>
      <div className="evs-progress__text">{progressText(run, nowMs)}</div>
    </div>
  );
}

function CaseHeader({ id, c }: { id: string; c: EvalCaseResult | null }) {
  // Rozet: kalan kırmızı (sapma), geçen / atlanan nötr (T9 — yeşil yok).
  return (
    <>
      {c && (
        <Badge tone={!c.ok && !c.skipped ? 'danger' : 'neutral'}>
          {c.skipped ? 'atlandı' : c.ok ? 'geçti' : c.error ? 'hata' : 'kaldı'}
        </Badge>
      )}
      <span className="mono">{id}</span>
      {c && <span className="field-hint">{c.surface}</span>}
    </>
  );
}

function CaseBody({ c }: { c: EvalCaseResult }) {
  const lines = expectLines(c.expect);
  const fails = c.fails ?? [];
  const errExtra = c.error && !fails.some(f => f.includes(c.error)) ? c.error : '';
  const maxUnknown = c.expect?.maxUnknownEntities;
  return (
    <>
      {c.why && <p className="evs-why">{c.why}</p>}
      <DrawerSection title="Özet">
        <KeyValue items={[
          { id: 'surface', k: 'Yüzey', v: c.surface },
          { id: 'profile', k: 'Profil', v: c.profileId, mono: true },
          { id: 'model', k: 'Model', v: c.model, mono: true },
          { id: 'latency', k: 'Süre', v: `${fmtSec1(c.latencyMs)} sn` },
          { id: 'unknown', k: 'Uydurma ad', v: `${c.unknownEntities}${maxUnknown !== undefined && maxUnknown !== null ? ` (sınır ${maxUnknown})` : ''}` },
          { id: 'rubric', k: 'Rubrik', v: c.skipped ? null : fmtDec2(c.rubricTotal) },
          ...(c.skipped ? [{ id: 'skip', k: 'Atlama nedeni', v: c.skipReason }] : []),
        ]} />
      </DrawerSection>
      {(fails.length > 0 || errExtra) && (
        <DrawerSection title="Neden">
          <ul className="evs-fails">
            {fails.map((f, i) => <li key={i}>{f}</li>)}
            {errExtra && <li>{errExtra}</li>}
          </ul>
        </DrawerSection>
      )}
      <DrawerSection title="Beklenti">
        {lines.length > 0
          ? <ul className="evs-expect">{lines.map((l, i) => <li key={i}>{l}</li>)}</ul>
          : <div className="field-hint">Beklenti yok — yalnız cevap üretimi ölçülür.</div>}
        <pre className="evs-pre">{JSON.stringify(c.expect ?? {}, null, 2)}</pre>
      </DrawerSection>
      <DrawerSection title="Girdi">
        <pre className="evs-pre">{c.input || '(boş)'}</pre>
        {c.inputTruncated && <div className="field-hint">kırpıldı — ilk 8 KiB</div>}
      </DrawerSection>
      <DrawerSection title="Model cevabı">
        <pre className="evs-pre">{c.answer || '(boş)'}</pre>
        {c.answerTruncated && <div className="field-hint">kırpıldı — ilk 16 KiB</div>}
      </DrawerSection>
    </>
  );
}

function CompareView({ cmp, hrefFor }: { cmp: EvalCompare; hrefFor: (caseId: string) => string }) {
  const groups = compareGroups(cmp);
  return (
    <div className="evs-cmp">
      <div className="evs-cmp__summary">{compareSummary(cmp)}</div>
      {cmp.note && <div className={cmp.comparable ? 'field-hint' : 'field-hint evs-note--warn'}>{cmp.note}</div>}
      {groups.length === 0
        ? <div className="field-hint">Vaka düzeyinde fark yok.</div>
        : groups.map(g => (
          <div key={g.key}>
            <div className={g.tone === 'err' ? 'evs-cmp__title evs-cmp__title--err' : 'evs-cmp__title'}>
              {g.title} · {g.items.length}
            </div>
            <ul className="evs-cmp__list">
              {g.items.map(it => (
                <li key={it.id}>
                  <Link to={hrefFor(it.id)} replace className="mono">{it.id}</Link>
                  <span className="field-hint"> {it.surface}{it.detail ? ` · ${it.detail}` : ''}</span>
                </li>
              ))}
            </ul>
          </div>
        ))}
    </div>
  );
}
