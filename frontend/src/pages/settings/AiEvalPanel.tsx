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
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { Link, useLocation, useSearchParams } from 'react-router-dom';
import { Spinner, Empty } from '@/components/Spinner';
import { QueryErrorInline } from '@/components/QueryError';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { ToggleGroup, ToggleGroupItem } from '@/components/shadcn/toggle-group';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { SelectField } from '@/components/coremetry/Field';
import { KeyValue } from '@/components/coremetry/KeyValue';
import { SectionHead } from '@/components/coremetry/SectionHead';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/coremetry/overlay';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { useEvalsetCatalog, useEvalRuns, useEvalRun, useEvalCompare, useStartEvalRun, useCancelEvalRun } from '@/lib/queries';
import type { AIToolset, EvalCaseResult, EvalCompare, EvalRunSummary, EvalSurfaceSummary } from '@/lib/types';
import { FlashBox, humanize } from './shared';
import {
  activeRun, agentMetaLine, caseBadgeLabel, caseReason, compareCandidates, compareGroups, compareSummary, evalHref, expectLines, failingCases,
  fmtAvg1, fmtDec2, fmtRunTime, fmtSec1, fmtSignedInt, fmtTokensK, isFinished, passDeltas, pickDefaultProfile, pickDefaultRunId,
  progressPct, progressText, routingHint, RUN_STATUS_VIEW, runStatusView, runToolsetView, selectionCaseCount, toggleSurface, toolsetLabel,
  trailRows, withEvalParams,
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
  // v0.10.1316 — ajan vakalarının ortalaması (null = yüzeyde ajan vakası yok → soluk "—").
  { id: 'toolCalls', label: 'Araç çağrısı', sortValue: r => r.avgToolCalls ?? -1, numeric: true, width: 120 },
  { id: 'tokens',    label: 'Token',        sortValue: r => r.avgTokens ?? -1, numeric: true, width: 100 },
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
  // v0.10.1311 — serbest döngü yüzeylerinin araç seti (full ↔ simple kıyası); sonda —
  // döngü yüzeyi olmayan / eski koşu soluk "—".
  { id: 'toolset', label: 'Araç seti', sortValue: r => r.toolset ?? '', naturalDir: 'asc', width: 110 },
];

type TableStateView = DataTableStateInput<unknown>;

// Faz 2 dilim 1 R11 — eski `.evs-*` sınıflarının utility karşılıkları (aynı
// ölçüler: --sp-3 = 1.5, --sp-4 = 2, --sp-5 = 2.5, --sp-6 = 3, --sp-7 = 4).
const PRE = 'mt-1.5 max-h-80 overflow-auto rounded-sm border border-(--border) bg-(--bg2) px-2.5 py-2 text-[length:var(--fs-xs)] leading-[1.45] whitespace-pre-wrap [overflow-wrap:anywhere] text-(--text)';
const LIST = 'm-0 list-disc pl-4 text-[length:var(--fs-sm)] leading-[1.6]';
const NOTE = 'mt-1.5 text-[length:var(--fs-xs)]';
// Kalan vaka noktası = başarısız koşu noktası (`status-dot-outage`, hiza utility).
const FAIL_DOT = RUN_STATUS_VIEW.failed.dot;
const ROW = 'mt-3 flex flex-wrap items-center gap-2';
// Yüzey çiplerinin "Tümü" değeri (ToggleGroup çoklu; boş seçim = tümü).
const ALL = '\u0000all';
// SegmentedControl '' değeri taşıyamaz (Radix tekli: '' = seçimsiz) → otomatik
// araç seti arayüzde 'auto', gövdede yine ''.
type ToolsetPick = 'auto' | Exclude<AIToolset, ''>;

/** undefined = DataTableCell'in soluk "—"sı (renderCell boş değer sözleşmesi). */
const shown = <V,>(v: V | null | undefined): V | undefined => (v == null || v === '' ? undefined : v);

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
  // v0.10.1311 — bir sonraki koşunun araç seti ('' = otomatik: üretim
  // profilinin araç seti, canlı serbest döngüyle aynı çözüm). Form alanı.
  const [toolset, setToolset] = useState<AIToolset>('');
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
  // ToggleGroup çoklu → bugünkü çip anlamı: "Tümü" seçimi boşaltır (zaten
  // seçiliyken tık bir şey yapmaz), yüzey çipi toggleSurface ile aç/kapa.
  const chipValue = picked.length === 0 ? [ALL] : picked;
  const onChips = (next: string[]) => {
    const added = next.find(v => !chipValue.includes(v));
    const removed = chipValue.find(v => !next.includes(v));
    const hit = added ?? removed;
    if (!hit || hit === ALL) { if (added === ALL) setPicked([]); return; }
    setPicked(p => toggleSurface(p, hit, order));
  };

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
    startM.mutate({ surfaces: picked, toolset }, {
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

  // Hücreler (kolonlar, sıra, genişlikler aynı; coremetry DataTable renderCell).
  const surfaceCell = (r: EvalSurfaceSummary, col: ColumnDef<EvalSurfaceSummary>): ReactNode => {
    switch (col.id) {
      case 'surface': return r.surface;
      case 'pass': return r.pass;
      case 'fail': return r.fail;
      case 'unknown': return shown(r.unknownEntities);
      case 'latency': return shown(fmtSec1(r.avgLatencyMs));
      case 'toolCalls': return shown(fmtAvg1(r.avgToolCalls));
      case 'tokens': return shown(fmtTokensK(r.avgTokens));
      default: return undefined;
    }
  };
  const caseCell = (c: EvalCaseResult, col: ColumnDef<EvalCaseResult>): ReactNode => {
    switch (col.id) {
      // T9/S4 — nokta tek başına durum taşımaz: bölüm başlığı ("Kalan") ve
      // gerekçe metni yanında; ekran okuyucuya ayrıca.
      case 'id': return (
        <>
          <span className={FAIL_DOT} aria-hidden="true" />
          {c.id}
          <span className="sr-only">{c.error ? ' — hata' : ' — kaldı'}</span>
        </>
      );
      case 'surface': return c.surface;
      case 'reason': return shown(caseReason(c));
      default: return undefined;
    }
  };
  const runCell = (r: RunRow, col: ColumnDef<RunRow>): ReactNode => {
    switch (col.id) {
      case 'started': return <span title={`${r.id} · ${r.startedBy || '—'}`}>{fmtRunTime(r.startedAt)}</span>;
      case 'version': return r.appVersion ? <span title={`prompt ${r.promptVersion || '—'}`}>{r.appVersion}</span> : undefined;
      case 'model': return shown(r.model);
      case 'status': { const view = runStatusView(r.status); return <><span className={view.dot} aria-hidden="true" />{view.label}</>; }
      case 'pass': return `${r.pass} / ${r.total}`;
      case 'delta': return r.delta === null ? undefined : fmtSignedInt(r.delta);
      case 'toolset': { const v = runToolsetView(r); return v.text ? <span title={v.title}>{v.text}</span> : undefined; }
      default: return undefined;
    }
  };

  return (
    <div className="max-w-[1100px]">
      {/* Kart gruplaması aynı (D3): `.card` + başlık/altbilgi şeridi (eski ui/Card ölçüleri). */}
      <div className="card">
        <div className="mb-2.5 border-b border-(--divider) pb-2 text-[length:var(--fs-md)] font-semibold">Değerlendirme (evalset)</div>
        <p className="mb-3 mt-0 text-[length:var(--fs-sm)] leading-normal text-(--text2)">
          Gömülü evalset vakalarını üretimle aynı yoldan — yapılandırılmış profil(ler), aynı sistem
          istemleri — sunucuda sırayla koşturur ve deterministik puanlar (içerik, uydurma ad, niyet,
          RCA hükmü, rubrik). Ajan vakaları (ChatLoop) canlı CoSRE döngüsünden — gerçek araç kataloğu,
          tur ve çağrı tavanlarıyla — geçer; araç yolu da puanlanır. Çağrılar /ai'da ayrı etiketlenir;
          üretim sayılarını ve bütçeyi şişirmez.
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
            {routed && <div className={`field-hint ${NOTE}`}>Farklı profile yönlenen: {routed}</div>}
            {/* R11: Chip pill'leri → shadcn ToggleGroup çoklu (aria-pressed düğmeler, aynı anlam). */}
            <ToggleGroup type="multiple" variant="outline" size="sm" spacing={1.5} value={chipValue} onValueChange={onChips}
              aria-label="Koşulacak yüzeyler" className="mt-3 flex-wrap [&>*]:rounded-full [&>*]:px-3">
              <ToggleGroupItem value={ALL}>Tümü {catalog.total}</ToggleGroupItem>
              {surfaces.map(s => (
                <ToggleGroupItem key={s.surface} value={s.surface}>{s.surface} {s.cases}</ToggleGroupItem>
              ))}
            </ToggleGroup>
            {/* v0.10.1311 — araç seti yalnız serbest döngü yüzeylerine uygulanır;
                kümede yoksa (katalog toolset '') seçici gösterilmez. */}
            {catalog.toolset && (
              <div className={ROW}>
                <span className="field-hint mt-0">Araç seti</span>
                <SegmentedControl<ToolsetPick> size="sm" aria-label="Araç seti" value={toolset || 'auto'}
                  onChange={v => setToolset(v === 'auto' ? '' : v)}
                  options={[
                    { value: 'auto', label: `Otomatik (${toolsetLabel(catalog.toolset) ?? '—'})`, title: 'Üretim profilinin araç seti: açık ayar, yoksa model adı ≤12B → basit' },
                    { value: 'simple', label: 'Basit', title: 'query_metric / search_logs / search_traces / list_metric_labels anılmaz' },
                    { value: 'full', label: 'Tam' },
                  ]} />
                <span className="field-hint mt-0">Serbest döngü yüzeylerine uygulanır: tek çağrılı vakada (ChatCodeResolve) prompt varyantı, ajan vakasında (ChatLoop) modele giden araç kataloğu da değişir.</span>
              </div>
            )}
          </>
        )}
        <div className={ROW}>
          <Button onClick={start} disabled={!canRun} loading={startM.isPending}>Koş</Button>
          {active && (
            <Button variant="outline" onClick={cancel} loading={cancelM.isPending}>Durdur</Button>
          )}
          <span className="field-hint mt-0">{blockReason || (catalog ? `${selCount} vaka · sırayla koşar` : '')}</span>
        </div>
        {/* v0.10.940 — süre son yoklamanın saatine göre (dataUpdatedAt): ayrı
            bir saniye sayacı (setInterval) yok; 10 s'lik yoklama zaten
            yeniden çiziyor ve gizli sekmede RQ ile birlikte durur. */}
        {active && <RunProgress run={active} nowMs={runsQ.dataUpdatedAt || Date.now()} />}
        {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
        <div className="mt-2.5 border-t border-(--divider) pt-2 text-[length:var(--fs-xs)] text-(--text3)">
          <Link to="/ai?source=evalset">Bu koşuların çağrıları /ai'da, Kaynak: Değerlendirme</Link>
        </div>
      </div>

      <SectionHead title={`${runParam ? 'Seçili koşu' : 'Son koşu'} · yüzey özeti`}
        meta={selectedRun ? runMeta(selectedRun) : undefined} />
      {selectedRun?.status === 'failed' && selectedRun.error && (
        <div className={`is-err ${NOTE}`}>Koşu başarısız: {selectedRun.error}</div>
      )}
      <div className="table-wrap">
        <DataTable<EvalSurfaceSummary> storageKey="settings-ai-eval-surfaces" columns={SURFACE_COLS} rows={surfaceRows}
          getRowKey={r => r.surface} renderCell={surfaceCell} state={summaryState} />
      </div>

      <SectionHead title="Kalan vakalar" meta={detail ? `${failing.length} vaka` : undefined} />
      <div className="table-wrap">
        <DataTable<EvalCaseResult> storageKey="settings-ai-eval-cases" columns={CASE_COLS} rows={failing}
          getRowHref={c => evalHref(pathname, searchParams, { run: selectedId, case: c.id })} rowLinkReplace
          getRowKey={c => c.id} renderCell={caseCell} state={casesState} />
      </div>

      <SectionHead title="Koşu geçmişi ve karşılaştırma"
        meta={runs.length ? `son ${runs.length} koşu · en çok 20 tutulur` : undefined} />
      <div className="table-wrap">
        <DataTable<RunRow> storageKey="settings-ai-eval-runs" columns={RUN_COLS} rows={runRows}
          sort={{ initial: { id: 'started', dir: 'desc' } }}
          // Koşu değişince açık çekmece kapanır; taban hedefle aynı olamaz.
          getRowHref={r => evalHref(pathname, searchParams, { run: r.id, case: null, cmp: cmpParam === r.id ? null : cmpParam })}
          rowLinkReplace selection={runSelection} renderCell={runCell} state={runsState} />
      </div>

      <div data-evs-cmp-pick="" className="mt-3 max-w-[520px]">
        <SelectField label="Şununla karşılaştır" value={cmpBase ?? ''}
          onChange={e => setParams({ cmp: e.target.value || null })}
          disabled={!selectedFinished || candidates.length === 0}
          hint={cmpHint(selectedRun, candidates.length)}>
          <option value="">— seç —</option>
          {candidates.map(r => (
            <option key={r.id} value={r.id}>
              {`${fmtRunTime(r.startedAt)} · ${r.appVersion || '—'} · ${r.model || '—'}${toolsetLabel(r.toolset) ? ` · ${toolsetLabel(r.toolset)}` : ''} · ${r.pass} / ${r.total}`}
            </option>
          ))}
        </SelectField>
      </div>
      {cmpBase && (
        cmp ? <CompareView cmp={cmp} hrefFor={caseHref} />
          : cmpQ.isError ? <QueryErrorInline text={humanize(cmpQ.error)} onRetry={() => { void cmpQ.refetch(); }} />
          : <Spinner label="Karşılaştırılıyor" />
      )}

      {/* R11 (D9, Q-IA9 kabul): ui/Drawer → coremetry Sheet, sağ kenar, sabit 680 px
          (sürükle-boyutla yok); bölümler SectionHead. Kapanış ?case='i siler. */}
      <Sheet open={!!caseParam} onOpenChange={o => { if (!o) setParams({ case: null }); }}>
        <SheetContent side="right" aria-label="Vaka ayrıntısı" aria-describedby={undefined}
          className="w-full gap-0 overflow-y-auto p-0 sm:w-[680px] sm:max-w-[680px]">
          <SheetHeader className="flex-row flex-wrap items-center gap-2 border-b border-(--divider) px-4 py-3 pr-12">
            <SheetTitle className="contents text-[length:var(--fs-md)] font-semibold">
              {caseParam && <CaseHeader id={caseParam} c={openCase} />}
            </SheetTitle>
          </SheetHeader>
          <div className="px-4 py-4">
            {openCase ? <CaseBody c={openCase} />
              : detail ? (
                <Empty compact icon="◇" title="Vaka bu koşuda yok">
                  Bağlantı başka bir koşuya ait olabilir; koşu geçmişinden seç.
                </Empty>
              )
              : detailQ.isError ? <QueryErrorInline text={humanize(detailQ.error)} onRetry={() => { void detailQ.refetch(); }} />
              : <Spinner label="Vaka yükleniyor" />}
          </div>
        </SheetContent>
      </Sheet>
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
  const ts = runToolsetView(r).text; // v0.10.1311
  if (ts) parts.push(`araç seti ${ts}`);
  if (r.rubricMean > 0) parts.push(`rubrik ${fmtDec2(r.rubricMean)}`);
  if (r.infra) parts.push(`${r.infra} altyapı hatası (puanlanmadı)`); // v0.10.1476
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
  // R11: hesaplanan genişlik satır içi stil değil, CSS değişkeni (coremetry
  // DataTable'ın ⋯ çapası deseni) — `w-[var(--evs-pct,0%)]`.
  const fillRef = useRef<HTMLSpanElement>(null);
  useLayoutEffect(() => { fillRef.current?.style.setProperty('--evs-pct', `${pct}%`); }, [pct]);
  return (
    <div className="mt-3 flex flex-col gap-1.5">
      {/* İz --bg3, dolgu --accent (rol: DOLGU, v0.10.920 renk rolleri). */}
      <div className="h-1.5 overflow-hidden rounded-(--radius-xs) bg-(--bg3)" role="progressbar" aria-label="Değerlendirme ilerlemesi"
        aria-valuemin={0} aria-valuemax={run.total} aria-valuenow={run.done}
        aria-valuetext={`${run.done} / ${run.total} vaka`}>
        <span ref={fillRef} className="block h-full w-[var(--evs-pct,0%)] bg-(--accent) transition-[width] duration-300" />
      </div>
      <div className="text-[length:var(--fs-sm)] tabular-nums text-(--text2)">{progressText(run, nowMs)}</div>
    </div>
  );
}

function CaseHeader({ id, c }: { id: string; c: EvalCaseResult | null }) {
  // Rozet: kalan kırmızı (sapma), geçen / atlanan nötr (T9 — yeşil yok).
  return (
    <>
      {c && (
        !c.ok && !c.skipped
          ? <StatusBadge status="critical">{caseBadgeLabel(c)}</StatusBadge>
          : <Badge variant="secondary">{caseBadgeLabel(c)}</Badge>
      )}
      {c?.mode === 'agent' && <Badge variant="secondary" title="Canlı ajan döngüsünde koştu">ajan</Badge>}
      <span className="mono">{id}</span>
      {c && <span className="field-hint mt-0 font-normal">{c.surface}</span>}
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
      {c.why && <p className="mb-3 mt-0 text-[length:var(--fs-sm)] leading-normal text-(--text2)">{c.why}</p>}
      <CaseSection title="Özet">
        <KeyValue items={[
          { id: 'surface', k: 'Yüzey', v: c.surface },
          { id: 'profile', k: 'Profil', v: c.profileId, mono: true },
          { id: 'model', k: 'Model', v: c.model, mono: true },
          { id: 'toolset', k: 'Araç seti', v: toolsetLabel(c.toolset) }, // v0.10.1311 — döngü dışı yüzey "—"
          { id: 'latency', k: 'Süre', v: `${fmtSec1(c.latencyMs)} sn` },
          { id: 'unknown', k: 'Uydurma ad', v: `${c.unknownEntities}${maxUnknown !== undefined && maxUnknown !== null ? ` (sınır ${maxUnknown})` : ''}` },
          { id: 'rubric', k: 'Rubrik', v: c.skipped ? null : fmtDec2(c.rubricTotal) },
          ...(c.skipped ? [{ id: 'skip', k: 'Atlama nedeni', v: c.skipReason }] : []),
        ]} />
      </CaseSection>
      {c.agent && <AgentTrail trail={c.agent} />}
      {(fails.length > 0 || errExtra) && (
        <CaseSection title="Neden">
          <ul data-evs-fails="" className={`${LIST} text-(--err)`}>
            {fails.map((f, i) => <li key={i}>{f}</li>)}
            {errExtra && <li>{errExtra}</li>}
          </ul>
        </CaseSection>
      )}
      <CaseSection title="Beklenti">
        {lines.length > 0
          ? <ul className={LIST}>{lines.map((l, i) => <li key={i}>{l}</li>)}</ul>
          : <div className="field-hint">Beklenti yok — yalnız cevap üretimi ölçülür.</div>}
        <pre className={PRE}>{JSON.stringify(c.expect ?? {}, null, 2)}</pre>
      </CaseSection>
      <CaseSection title="Girdi">
        <pre className={PRE}>{c.input || '(boş)'}</pre>
        {c.inputTruncated && <div className="field-hint">kırpıldı — ilk 8 KiB</div>}
      </CaseSection>
      <CaseSection title="Model cevabı">
        <pre className={PRE}>{c.answer || '(boş)'}</pre>
        {c.answerTruncated && <div className="field-hint">kırpıldı — ilk 16 KiB</div>}
      </CaseSection>
    </>
  );
}

/** v0.10.1316 — ajan vakasının araç izi: künye + sıralı adım listesi (≤12
 *  sabit sıralı adım; tablo değil). Renk yalnız hatalı adımda. */
function AgentTrail({ trail }: { trail: NonNullable<EvalCaseResult['agent']> }) {
  const rows = trailRows(trail);
  return (
    <CaseSection title="Araç izi">
      <div className="text-[length:var(--fs-sm)] tabular-nums text-(--text)">{agentMetaLine(trail)}</div>
      {(trail.overruns ?? []).length > 0 && (
        <div className="field-hint text-(--warn)">Ölçü aşımı (vakayı kaldırmaz): {(trail.overruns ?? []).join(' · ')}</div>
      )}
      {rows.length > 0 ? (
        <ol data-evs-trail="" className="mt-2 mb-0 list-decimal pl-4 text-[length:var(--fs-sm)] leading-normal [&>li+li]:mt-2">
          {rows.map(r => (
            <li key={r.key}>
              <div className="flex flex-wrap items-baseline gap-1.5">
                <span className="mono">{r.tool}</span>
                {r.origin && <span className="field-hint mt-0">{r.origin}</span>}
                <span className={r.status === 'err' ? 'is-err' : 'field-hint mt-0'}>{r.statusLabel}</span>
                {r.meta && <span className="field-hint mt-0">{r.meta}</span>}
              </div>
              {r.args && <div className="mono mt-0.5 text-[length:var(--fs-xs)] [overflow-wrap:anywhere] text-(--text2)">{r.args}</div>}
              {r.preview && <div className="mt-1 max-h-24 overflow-auto rounded-sm border border-(--border) bg-(--bg2) px-2 py-1 text-[length:var(--fs-xs)] whitespace-pre-wrap [overflow-wrap:anywhere] text-(--text2)">{r.preview}</div>}
            </li>
          ))}
        </ol>
      ) : <div className="field-hint">Araç çağrılmadı — cevap araçsız üretildi.</div>}
      {trail.stepsTruncated && <div className="field-hint">ilk {rows.length} adım gösteriliyor</div>}
    </CaseSection>
  );
}

function CompareView({ cmp, hrefFor }: { cmp: EvalCompare; hrefFor: (caseId: string) => string }) {
  const groups = compareGroups(cmp);
  return (
    <div className="mt-2.5 flex flex-col gap-2.5">
      <div className="text-[length:var(--fs-sm)] tabular-nums text-(--text)">{compareSummary(cmp)}</div>
      {cmp.note && <div className={cmp.comparable ? 'field-hint' : 'field-hint text-(--warn)'}>{cmp.note}</div>}
      {groups.length === 0
        ? <div className="field-hint">Vaka düzeyinde fark yok.</div>
        : groups.map(g => (
          <div key={g.key}>
            <div className={`mb-1 text-[length:var(--fs-xs)] font-semibold ${g.tone === 'err' ? 'text-(--err)' : 'text-(--text2)'}`}>
              {g.title} · {g.items.length}
            </div>
            <ul className={LIST}>
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

/** R11 (D9): Sheet bölümü — eski DrawerSection'ın yerine SectionHead başlığı. */
function CaseSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="mb-4">
      <SectionHead title={title} />
      {children}
    </section>
  );
}
