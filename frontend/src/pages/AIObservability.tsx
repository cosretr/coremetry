import { Fragment, lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { rowActivation } from '@/lib/a11y'; // v0.10.451 (dış denetim D3 kalan)
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { Button, Drawer, DrawerSection, SegmentedControl } from '@/components/ui';
import { api } from '@/lib/api';
import { cachedPctLabel } from './ai/cachedTokens';
import { ExchangesPanel } from './ai/ExchangesPanel';
import { CosreQualityPanel } from './ai/CosreQualityPanel';
import { RoutingReportPanel } from './ai/RoutingReportPanel'; // v0.10.1464 — haftalık yanlış-yönlendirme adayları
import { SREEvalPanel } from './ai/SREEvalPanel'; // v0.10.1301 — AI SRE Faz 5 doğruluk ölçümü
import { PendingCasesPanel } from './ai/PendingCasesPanel'; // v0.10.1475 — eval'in onaylı vaka girdisi
// CoSRE doğruluk ölçümü A2 — replay notlaması; admin bölümü tembel chunk (paket bütçesi).
const SREReplayPanel = lazy(() => import('./ai/SREReplayPanel').then(m => ({ default: m.SREReplayPanel })));
// v0.10.2468 — başarısız tur inceleme kuyruğu + eval'e terfi (admin; tembel chunk).
const FailedTurnsPanel = lazy(() => import('@/components/coremetry/ai/FailedTurnsPanel').then(m => ({ default: m.FailedTurnsPanel })));
import { useUrlRange } from '@/lib/useUrlRange';
import { rcaPctText, rcaEngineTone, rcaSatisfactionText, rcaBucketLabel, rcaBucketSatisfaction, rcaCalibrationNote } from './ai/rcaQualityView';
import type { RCAVerdictQuality, AIBudgetStatus, NegativeFeedbackCall, RouterGap, RouterGapsView } from '@/lib/types';
import { budgetVerdict, budgetCls } from './ai/aiBudgetView';
import { gapWho, gapChainTitle, GAP_KIND_LABEL, gapKindOptions, filterGapsByKind, type GapKindFilter } from './ai/routerGapsView'; // v0.10.2448 — kim sordu, tur başına satır; v0.10.2465 netleştirme türü + süzgeç
import { timeRangeToNs, tsLong, fmtNum } from '@/lib/utils';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef, type DataTableStateProps } from '@/components/ui/DataTable';
import {
  type AIRateTable, mergeRates, costForCall, fmtCost,
} from '@/lib/ai-rates';
import {
  type AICall, type AIStats, type AICallsTimePoint, type TimeRange, type AICallSource,
  EVALSET_SURFACE_PREFIX,
} from '@/lib/types';
import { Link, useSearchParams } from 'react-router';
import { traceHref } from '@/lib/traceHref';
import { PageControls } from '@/components/coremetry/PageControls';
import { PageShell } from '@/components/coremetry/PageShell';
import { useAuthRole } from '@/components/AuthProvider'; // v0.10.1484
import { t, tf, useLang, useT, type Lang } from '@/lib/i18n';
import { duration } from '@/lib/format/duration';
import { percent } from '@/lib/format/percent';
import { count, fixed } from '@/lib/format/number';

// /ai — Coremetry-native AI observability dashboard. The
// Langfuse-alike: every Copilot Explain call lands as one
// ai_calls row, this page surfaces KPIs + per-surface and per-
// provider breakdowns + a paginated recent-calls table with a
// drill-in modal that shows prompt + response samples (capped
// at 4KB each at insert time so a runaway prompt can't blow up
// the row).
//
// Admin-only — prompts can carry telemetry the viewer role
// might not otherwise have access to.
//
// v0.10.1484 (operatör kararı) — "Şüpheli yönlendirmeler" raporu editörlere de
// açık (GET /api/ai/routing-report editor + admin). Editör /ai'de YALNIZ o
// paneli görür; diğer paneller (çağrılar, prompt örnekleri, bütçe, kalite)
// admin kalır ve editöre hiç çizilmez (403 hata kutuları yerine).
export default function AIObservabilityPage() {
  const role = useAuthRole();
  if (role === 'editor') return <EditorAIObservabilityPage />;
  return <AdminAIObservabilityPage />;
}

// EditorAIObservabilityPage — v0.10.1484: editörün /ai görünümü.
export function EditorAIObservabilityPage() {
  const tn = useT(); // v0.10.2486 dilim 2 — Topbar başlığı katalogdan
  return (
    <>
      <Topbar title={tn('nav.ai')} />
      <PageShell>
        <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 12 }}>
          {tn('aiObs.editorIntro')}
        </div>
        <RoutingReportPanel />
      </PageShell>
    </>
  );
}

function AdminAIObservabilityPage() {
  const tn = useT(); // v0.10.2486 dilim 2 — Topbar başlığı katalogdan
  const lang = useLang();
  const [range, setRange] = useUrlRange('24h');
  const [stats, setStats] = useState<AIStats | null | undefined>(undefined);
  const [budget, setBudget] = useState<AIBudgetStatus | null>(null); // v0.10.411
  const [series, setSeries] = useState<AICallsTimePoint[] | undefined>(undefined);
  const [calls, setCalls] = useState<AICall[] | null | undefined>(undefined);
  const [surface, setSurface] = useState('');
  const [provider, setProvider] = useState('');
  const [status, setStatus] = useState('');
  const surfaceRef = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState<AICall | null>(null);
  // v0.10.1470 — tablo satırı çekmeceyi HEMEN açar, sonra ayrıntı yanıtı
  // (traceQueryable bayrağı yalnız GET /api/ai/calls/{id}'de) aynı satırın
  // üstüne biner; arada başka satır açıldıysa ezmez.
  const openCallRow = (c: AICall) => {
    setOpen(c);
    api.aiCall(c.id)
      .then(d => { if (d) setOpen(prev => (prev?.id === d.id ? d : prev)); })
      .catch(() => { /* satır TTL'le düşmüş olabilir — liste satırı kalır */ });
  };
  const [rates, setRates] = useState<AIRateTable>(() => mergeRates(null));
  // v0.10.940 (operatör onayı K2) — Kaynak: Üretim (varsayılan, parametre
  // YOK) / Değerlendirme (?source=evalset). Settings › CoSRE › Değerlendirme
  // koşularının çağrıları ai_calls'a `evalset-*` yüzeyiyle düşer; sunucu
  // üretim görünümünde onları HARİÇ tutar, yani KPI/grafik/tablo üretimi
  // şişirmez. Kaynak URL'de (paylaşılan link aynı görünümü açar; Settings
  // paneli buraya ?source=evalset ile bağlanır) ve üç okumaya da gider —
  // biri unutulsa KPI bir kaynaktan, tablo ötekinden okurdu. Bütçe (24 sa)
  // parametre almaz: bütçe üretim harcamasının tavanıdır.
  const [searchParams, setSearchParams] = useSearchParams();
  const source: AICallSource | undefined = searchParams.get('source') === 'evalset' ? 'evalset' : undefined;
  const setSource = (v: string) => {
    setSearchParams(prev => {
      const p = new URLSearchParams(prev);
      if (v === 'evalset') p.set('source', 'evalset');
      else p.delete('source');
      return p;
    }, { replace: true });
    // v0.10.940 — öbür kaynağın yüzey süzgeci taşınmaz: sunucu kaynak
    // koşulunu süzgeçle AND'ler, `evalset-…` süzgeci Üretim'de (ya da
    // tersi) hep 0 satır döndürürdü. Sağlayıcı / durum iki kaynakta da
    // anlamlı — korunur.
    if (surface && surface.startsWith(EVALSET_SURFACE_PREFIX) !== (v === 'evalset')) setSurface('');
  };

  // v0.9.875 (tutarlılık denetimi BT1) — 200 satırlık AI çağrı listesi.
  // Duration ve Cost kolonları VARDI ama sıralanamıyordu; "en pahalı /
  // en yavaş çağrı hangisi" sorusu göz taramasıyla cevaplanıyordu.
  //
  // Kolonlar `rates`e memoise: maliyet oranlardan türetiliyor. `rates`
  // mount başına BİR KEZ ayarlanıyor, yani kimlik kararlı — columnLayoutSig
  // her render'da tazelenmiyor (operatörün sürüklediği genişlikler durur).
  //
  // v0.10.947 (tablo standardı) — hücre görünümü kolon bayraklarında; 11px
  // ikincil hücreler tablo boyunda, hiyerarşi renkle (S3).
  const callCols = useMemo<ColumnDef<AICall>[]>(() => [
    { id: 'time',     label: t('aiObs.col.time', lang),              sortValue: c => c.createdAt, width: 165, tone: () => 'muted' },
    { id: 'surface',  label: t('aiObs.col.surface', lang),           sortValue: c => c.surface,   naturalDir: 'asc', flex: true, mono: true },
    { id: 'model',    label: t('aiObs.col.model', lang),  sortValue: c => `${c.provider} ${c.model ?? ''}`, naturalDir: 'asc', width: 210 },
    { id: 'status',   label: t('aiObs.col.status', lang),            sortValue: c => c.status,    naturalDir: 'asc', width: 95 },
    { id: 'duration', label: t('aiObs.col.duration', lang),          sortValue: c => c.durationMs, numeric: true, width: 105 },
    { id: 'tokens',   label: t('aiObs.col.tokens', lang),   sortValue: c => c.inputTokens + c.outputTokens, numeric: true, width: 140, tone: () => 'faint' },
    // Oran tablosunda olmayan bir model için costForCall null döner;
    // sıralamada -1 ile en dibe iner (bilinmeyen maliyet "ucuz" sayılmasın).
    { id: 'cost',     label: t('aiObs.col.cost', lang),
      sortValue: c => costForCall(rates, c.model, c.inputTokens, c.outputTokens) ?? -1,
      numeric: true, width: 100,
      tone: c => (costForCall(rates, c.model, c.inputTokens, c.outputTokens) === null ? 'faint' : 'muted') },
    { id: 'user',     label: t('aiObs.col.user', lang),              sortValue: c => c.userEmail || c.userId || '', naturalDir: 'asc', width: 175, tone: () => 'faint' },
  ], [rates, lang]);
  const dt = useDataTable<AICall>({
    storageKey: 'ai-calls', columns: callCols, rows: calls ?? [],
    initialSort: { id: 'time', dir: 'desc' },
  });
  // v0.10.954 — tablo standardı T12: yükleniyor / hata / boş tablonun
  // İÇİNDE, başlık durur. Yüzey / sağlayıcı / durum süzgeci sunucu
  // isteğine gider; biri etkinken 0 satır "eşleşme yok"tur ve kurtuluş
  // yolu "Clear" düğmesiyle AYNI eylem (kaynak bir görünüm, süzgeç değil).
  const clearCallFilters = () => { setSurface(''); setProvider(''); setStatus(''); };
  const callsState: Omit<DataTableStateProps<AICall>, 'dt'> =
    calls === undefined ? { kind: 'loading' }
    : calls === null ? { kind: 'error' }
    : (surface || provider || status) ? { kind: 'no-match', onClearFilters: clearCallFilters, returnFocusRef: surfaceRef }
    : { kind: 'empty', message: tn('aiObs.calls.empty') };

  // Pull operator-set rate overrides; merge over the bundled
  // defaults. Done once per mount — rates change infrequently
  // (manual Settings edits).
  useEffect(() => {
    api.aiRates()
      .then(o => setRates(mergeRates(o)))
      .catch(() => { /* fall back to bundled */ });
  }, []);

  // v0.10.940 — kaynak değişince önceki kaynağın KPI'ları / grafiği yeni
  // etiketin altında bir an bile görünmesin: yeni okuma gelene dek yükleniyor.
  useEffect(() => { setStats(undefined); setSeries(undefined); }, [source]);

  // Poll every 60s so the page stays close to live for the
  // operator watching deployments. Cheap — the stats query is
  // server-side cached 30s.
  useEffect(() => {
    let timer: number | undefined;
    let cancelled = false; // v0.8.300 — range change mid-flight must not overwrite
    const tick = () => {
      const { from, to } = timeRangeToNs(range);
      api.aiStats({ from, to, source }).then(s => { if (!cancelled) setStats(s); }).catch(() => { if (!cancelled) setStats(null); });
      api.aiSeries({ from, to, source }).then(s => { if (!cancelled) setSeries(s ?? []); }).catch(() => { if (!cancelled) setSeries([]); });
      api.aiBudget().then(b => { if (!cancelled) setBudget(b); }).catch(() => { if (!cancelled) setBudget(null); }); // v0.10.411 — sabit 24 sa pencere
    };
    tick();
    // v0.5.248 — skip the refresh when the tab is hidden so
    // backgrounded operator sessions don't re-query CH every
    // minute. Foreground operator sees fresh stats on focus
    // (the next tick fires within 60s).
    timer = window.setInterval(() => { if (!document.hidden) tick(); }, 60_000);
    return () => { cancelled = true; if (timer) clearInterval(timer); };
  }, [range, source]);

  useEffect(() => {
    const { from, to } = timeRangeToNs(range);
    setCalls(undefined);
    let cancelled = false; // v0.8.300 — stale-overwrite guard
    api.aiCalls({
      from, to, limit: 200,
      surface: surface || undefined,
      provider: provider || undefined,
      status: status || undefined,
      source, // v0.10.940
    })
      .then(c => { if (!cancelled) setCalls(c ?? []); })
      .catch(() => { if (!cancelled) setCalls(null); });
    return () => { cancelled = true; };
  }, [range, surface, provider, status, source]);

  return (
    <>
      <Topbar title={tn('nav.ai')} range={range} onRangeChange={setRange} />
      <PageShell>
        <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 12 }}>
          {tn('aiObs.intro')}
          {/* v0.10.940 — kaynak seçici süzgeç şeridinde, KPI'ların ALTINDA;
              değerlendirme görünümündeyken karoların neyi saydığı üstte de
              yazsın (bütçe karosu hep üretim). */}
          {source === 'evalset' && (
            <div className="field-hint">
              {tn('aiObs.evalsetHint')}
            </div>
          )}
        </div>

        {/* KPI cards */}
        {stats === undefined && <Spinner />}
        {stats === null && (
          <Empty icon="✗" title={tn('aiObs.statsFailed')}>
            {tn('aiObs.statsFailedHint')}
          </Empty>
        )}
        {stats && (() => {
          // Sum estimated cost across the per-provider breakdown.
          // Skip models we have no rate for; null total means
          // every model was unknown — render "—" instead of $0.
          let totalCost = 0;
          let anyKnown = false;
          for (const r of stats.byProvider ?? []) { // v0.10.811 — eski sunucu null dönebilir
            const c = costForCall(rates, r.model, r.inputTokens, r.outputTokens);
            if (c !== null) {
              totalCost += c;
              anyKnown = true;
            }
          }
          const totalCostLabel = anyKnown ? fmtCost(totalCost) : '—';
          // v0.10.411 — bütçe: SON 24 SAAT (seçici penceresi değil); dolar
          // aynı fiyat tablosuyla, bilinmeyen model varsa null (yargılanmaz).
          let dayCost: number | null = 0;
          if (budget) {
            for (const m of budget.usage.byModel) {
              const c = costForCall(rates, m.model, m.inputTokens, m.outputTokens);
              if (c === null) { dayCost = null; break; }
              dayCost += c;
            }
          }
          const bv = budgetVerdict(budget, dayCost);
          return (
          <>
            <div style={{
              display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))',
              gap: 10, marginBottom: 14,
            }}>
              <KPI label={tn('aiObs.kpi.totalCalls')} value={fmtNum(stats.totalCalls)} />
              <KPI label={tn('aiObs.kpi.errorRate')} value={percent(stats.errorRate, lang, { from: 'ratio' })}
                cls={stats.errorRate > 0.1 ? 'err' : stats.errorRate > 0.02 ? 'warn' : 'ok'} />
              <KPI label={tn('aiObs.kpi.avgLatency')} value={duration(stats.avgDurationMs, lang)} />
              <KPI label={tn('aiObs.kpi.p99Latency')} value={duration(stats.p99DurationMs, lang)} />
              <KPI label={tn('aiObs.kpi.inputTokens')} value={fmtNum(stats.inputTokens)} />
              <KPI label={tn('aiObs.kpi.outputTokens')} value={fmtNum(stats.outputTokens)} />
              {/* v0.10.807 (L2) — önek önbelleği: giriş token'ının yüzde kaçı önbellekten geldi.
                  0 iken de gösterilir (kolon varsa): "uç bildirmiyor ya da önbellek kapalı" sinyali. */}
              {stats.cachedCol && (
                <KPI label={tn('aiObs.kpi.cache')} value={cachedPctLabel(stats.cachedTokens ?? 0, stats.inputTokens)}
                  cls={(stats.cachedTokens ?? 0) > 0 ? 'ok' : undefined} />
              )}
              <KPI label={tn('aiObs.kpi.estCost')} value={totalCostLabel} />
              <KPI label={tn('aiObs.kpi.budget')} value={bv.text} cls={budgetCls(bv)} />
              {/* v0.10.421 (E6) — uydurma oranı; v0.10.409'un ilk-token karosu da burada. */}
              {stats.extended && (
                <KPI label={tn('aiObs.kpi.shield')} value={tf('aiObs.kpi.shieldValue', lang, { calls: fmtNum(stats.shieldHitCalls ?? 0), names: fmtNum(stats.shieldHits ?? 0) })}
                  cls={(stats.shieldHitCalls ?? 0) > 0 ? 'warn' : 'ok'} />
              )}
              {stats.extended && (stats.avgTtftMs ?? 0) > 0 && (
                <KPI label={tn('aiObs.kpi.ttft')} value={duration(stats.avgTtftMs ?? 0, lang)} />
              )}
              <KPI label={tn('aiObs.kpi.users')} value={fmtNum(stats.distinctUsers)} />
            </div>

            {/* Volume + error timeseries */}
            {series && series.length > 0 && (
              <CallsChart series={series} />
            )}

            {/* Per-surface + per-provider breakdowns */}
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))', gap: 12, marginTop: 14 }}>
              {(stats.bySurface ?? []).length > 0 && (
                <BreakdownTable title={tn('aiObs.bySurface')}
                  rows={(stats.bySurface ?? []).map(r => ({
                    a: r.surface, b: fmtNum(r.calls),
                    c: percent(r.errorRate, lang, { from: 'ratio' }),
                    d: duration(r.avgMs, lang),
                    // Memnuniyet (v0.8.399) — thumbs-up rate over rated
                    // exchanges. thumbsUpRate is omitempty server-side,
                    // so an absent rate with feedback present means 0%.
                    e: r.feedbackCount
                      ? `${Math.round((r.thumbsUpRate ?? 0) * 100)}% (${r.feedbackCount})`
                      : '—',
                  }))}
                  cols={[tn('aiObs.bd.surface'), tn('aiObs.bd.calls'), tn('aiObs.bd.errRate'), tn('aiObs.bd.avgMs'), tn('aiObs.bd.satisfaction')]}
                  onPickFirst={v => setSurface(v)} />
              )}
              {(stats.byProvider ?? []).length > 0 && (
                <BreakdownTable title={tn('aiObs.byProvider')}
                  rows={(stats.byProvider ?? []).map(r => ({
                    a: `${r.provider} · ${r.model || '—'}`, b: fmtNum(r.calls),
                    // v0.10.400 — model başına hata oranı + p95 gecikme (CoSRE denetimi O5/E3).
                    c: `${percent(r.calls ? (r.errors ?? 0) / r.calls : 0, lang, { from: 'ratio' })} · p95 ${duration(r.p95Ms ?? 0, lang)}`,
                    d: fmtCost(costForCall(rates, r.model, r.inputTokens, r.outputTokens)),
                  }))}
                  cols={[tn('aiObs.bd.provider'), tn('aiObs.bd.calls'), tn('aiObs.bd.errP95'), tn('aiObs.kpi.estCost')]}
                  onPickFirst={v => setProvider(v.split(' · ')[0])} />
              )}
            </div>
          </>
          );
        })()}

        {/* v0.10.1153 — CoSRE etkileşimleri: kullanıcı turu başına bir satır
            (LLM'siz cevaplar dahil), alt çağrılar çekmecede. Çağrı tablosu
            aşağıda model çağrısı başına kalır. Değerlendirme kaynağında tur
            yok (evalset tek çağrı koşar) — panel yalnız üretimde. */}
        {source !== 'evalset' && (
          <ExchangesPanel range={range}
            onOpenCall={id => { api.aiCall(id).then(c => { if (c) setOpen(c); }).catch(() => { /* satır TTL'le düşmüş olabilir */ }); }} />
        )}

        {/* Filter strip */}
        <PageControls sticky style={{ marginTop: 18, marginBottom: 8 }}>
          {/* v0.9.1003 (etkileşim denetimi O4) — işaret GERİ kondu.
              v0.9.669 PageControls'u `data-shortcut-search` için
              geçirgen yapmıştı ama öznitelik bu dosyaya hiç geri
              yazılmamıştı: PageControls.tsx:43'ün yorumu bir sözleşme
              anlatıyordu, kablo takılı değildi. Aynı kök nedenin
              (shortcutSearchTarget.test.ts başlığı) ikinci tekrarı. */}
          {/* v0.10.940 — Kaynak ilk sırada: diğer süzgeçlerden farklı
              olarak yalnız tabloyu değil KPI + grafiği de kapsar. İki değer →
              düz select (frontend-design-system §3 (eski frontend-conventions), ≤10 sabit değer). Boyut
              `.ai-filter-select`te, satır içinde değil: satır içi 12px
              yoğunluk kurallarını ve telefondaki iOS 16px kuralını ezerdi;
              sağlayıcı / durum seçicileri de aynı sınıfa geçti. */}
          <select value={source ?? ''} onChange={e => setSource(e.target.value)}
            aria-label={tn('aiObs.source')}
            className="ai-filter-select">
            <option value="">{tn('aiObs.source.prod')}</option>
            <option value="evalset">{tn('aiObs.source.eval')}</option>
          </select>
          <input ref={surfaceRef} type="search" placeholder={tn('aiObs.filter.surfacePh')} aria-label={tn('aiObs.filter.surface')}
            data-shortcut-search
            value={surface} onChange={e => setSurface(e.target.value)}
            style={{ fontSize: 12, padding: '3px 8px', width: 200 }} />
          <select value={provider} onChange={e => setProvider(e.target.value)}
            aria-label={tn('aiObs.filter.provider')}
            className="ai-filter-select">
            <option value="">{tn('aiObs.filter.allProviders')}</option>
            {/* sağlayıcı kimlikleri sunucu değeri — çevrilmez */}
            {['openai', 'anthropic', 'github'].map(p => <option key={p} value={p}>{p}</option>)}
          </select>
          <select value={status} onChange={e => setStatus(e.target.value)}
            aria-label={tn('aiObs.filter.status')}
            className="ai-filter-select">
            <option value="">{tn('aiObs.filter.allStatuses')}</option>
            {/* ai_calls.status değerleri sunucu sabiti — çevrilmez */}
            {['ok', 'error'].map(v => <option key={v} value={v}>{v}</option>)}
          </select>
          {(surface || provider || status) && (
            <Button variant="secondary" size="sm"
              onClick={clearCallFilters}>{tn('aiObs.clear')}</Button>
          )}
          <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--text3)' }}>
            {calls ? tf('aiObs.callCount', lang, { n: calls.length }) : ''}
          </span>
          {calls && calls.length > 0 && (
            <Button variant="secondary" size="sm"
              onClick={() => exportCallsCSV(calls, rates)}
              title={tn('aiObs.csvTitle')}>
              {/* i18n-exempt: dosya biçimi adı */}
              ↓ CSV
            </Button>
          )}
        </PageControls>

        {/* Recent calls table */}
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.length === 0 ? <DataTableState dt={dt} {...callsState} /> : dt.sortedRows.map(c => {
                const cost = costForCall(rates, c.model, c.inputTokens, c.outputTokens);
                return (
                <tr key={c.id} className="cv-row" {...rowActivation(() => openCallRow(c))}>
                  {/* v0.10.947 — zaman damgası mono kalır (S2 yalnız sayıyı kapsar). */}
                  <DataTableCell dt={dt} col="time" row={c} value={tsLong(c.createdAt)} className="mono" />
                  <DataTableCell dt={dt} col="surface" row={c} value={c.surface} />
                  <DataTableCell dt={dt} col="model" row={c}>
                    <span style={{ color: 'var(--text2)' }}>{c.provider}</span>
                    {c.model && <span style={{ color: 'var(--text3)' }}> · {c.model}</span>}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="status" row={c}>
                    {c.status === 'ok'
                      ? <span className="badge b-gray">ok</span> // i18n-exempt: ai_calls.status değerleri (sunucu sabiti)
                      : <span className="badge b-err">error</span>}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="duration" row={c} value={`${c.durationMs} ms`} />
                  <DataTableCell dt={dt} col="tokens" row={c}
                    title={c.cachedTokens ? tf('aiObs.cachedTitle', lang, { count: c.cachedTokens }) : undefined}>
                    {c.inputTokens}{c.cachedTokens ? <span> ({cachedPctLabel(c.cachedTokens, c.inputTokens)})</span> : null} / {c.outputTokens}
                  </DataTableCell>
                  <DataTableCell dt={dt} col="cost" row={c} value={fmtCost(cost)} />
                  <DataTableCell dt={dt} col="user" row={c} value={c.userEmail || c.userId || null} />
                </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {/* v0.9.1018 — TAVAN BEYANI. Sabit `limit: 200` ekranda hiç
            geçmiyordu: yoğun bir pencerede tablo sessizce kırpılıyor ve
            operatör eksik veriyle "AI çağrısı azalmış" diye okuyordu.
            v0.9.1014'ün `count` sözleşmesiyle aynı dürüstlük kuralı. */}
        {calls && calls.length >= 200 && (
          <div className="pager" style={{ color: 'var(--text3)' }}>
            {tn('aiObs.capNote')}
          </div>
        )}

        {/* v0.9.423 (CoSRE fikir #6) — 👎 madenciliği: hangi soru
            şekilleri kötü cevap alıyor? Yeni guided-intent adayları
            buradan çıkar. Sunucu 60s cache'li; mount'ta bir kez. */}
        {/* v0.9.549 — 👎 paneli "cevap kötüydü"yü, bu panel
            "soru hiç tanınmadı"yı gösteriyor. İkisi farklı
            kalite sinyali: biri anlatımın, diğeri yönlendirmenin. */}
        {/* v0.9.594 — kalite ölçümü. Yukarıdaki her şey TRANSPORT
            sağlığı: kaç çağrı, kaç hata, kaç token, ne kadar gecikme.
            Bir model sürekli 200 dönüp sürekli saçmalayabilir ve o
            karolarda mükemmel görünür. Bu panel cevabın KENDİSİNE
            bakan tek yer. */}
        <RCAQualityPanel range={range} />
        <RouterGapsPanel />
        <Suspense fallback={null}><FailedTurnsPanel /></Suspense>
        <RoutingReportPanel />
        <NegativeFeedbackPanel />
        <CosreQualityPanel /> {/* CoSRE öğrenme Faz 2 — örtük sinyal gece puanı */}
        <PendingCasesPanel /> {/* v0.10.1475 — onay bekleyen vakalar: eval yalnız onaylı vaka okur */}
        <SREEvalPanel /> {/* v0.10.1301 — AI SRE Faz 5: geçmiş problemlerle doğruluk (onaylı vakalar; 👎 yalnız inceleme) */}
        <Suspense fallback={null}><SREReplayPanel /></Suspense> {/* CoSRE replay notu: etiketli Problem'ler, yerel hakem */}

        {open && <CallDrawer call={open} rates={rates} onClose={() => setOpen(null)} />}
      </PageShell>
    </>
  );
}

// RCAQualityPanel — v0.9.594. Kök-neden hakem motorunun kalitesi.
//
// Üç ayrı soru, üçü FARKLI şey ve birine tek başına bakmak yanıltır:
//
//   kararın DAĞILIMI   kaçı kök neden gösterdi, kaçı "kanıt yetersiz"
//   MOTORUN sağlığı    model kaç kez çözümlenemedi, kalkan kaç kez girdi
//   OPERATÖRÜN yargısı 👍/👎
//
// Yüksek "kanıt yetersiz" oranı modelin zayıflığı DEĞİL, kanıtın
// yetersizliği olabilir. Yüksek kalkan oranı ise modelin UYDURDUĞUNU
// söyler ve bu bambaşka bir arıza — biri veri sorunu, öteki model
// sorunu ve çareleri de farklı.
function RCAQualityPanel({ range }: { range: TimeRange }) {
  const lang = useLang();
  const tn = useT();
  const [q, setQ] = useState<RCAVerdictQuality | null | undefined>(undefined);
  useEffect(() => {
    let cancelled = false;
    setQ(undefined);
    const { from, to } = timeRangeToNs(range);
    api.aiRCAQuality({ from, to })
      .then(r => { if (!cancelled) setQ(r); })
      .catch(() => { if (!cancelled) setQ(null); });
    return () => { cancelled = true; };
  }, [range]);

  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="ov-card-h">
        <h3>{tn('aiObs.rca.title')}</h3>
        <span className="ov-sub">
          {tn('aiObs.rca.sub')}
        </span>
      </div>
      <div className="ov-card-b">
        {q === undefined && <Spinner />}
        {q === null && <Empty icon="✗" title={tn('aiObs.loadFailed')} />}
        {q && q.total === 0 && (
          <Empty icon="○" title={tn('aiObs.rca.empty')}>
            {tn('aiObs.rca.emptyHint')}
          </Empty>
        )}
        {q && q.total > 0 && (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 10 }}>
            <KPI label={tn('aiObs.rca.verdict')} value={fmtNum(q.total)} />
            <KPI label={tn('aiObs.rca.rootCause')}
              value={`${fmtNum(q.rootCauseIdentified)} · ${rcaPctText(q.rootCauseIdentified, q.total)}`} />
            <KPI label={tn('aiObs.rca.probable')}
              value={`${fmtNum(q.probableCause)} · ${rcaPctText(q.probableCause, q.total)}`} />
            {/* Kanıt yetersiz NÖTR çizilir: bir arıza değil, geçerli
                bir cevap. Kırmızı yapmak modeli tam da kaçınmasını
                istemediğimiz yöne — kendinden emin ve yanlış cevaba —
                iter (prompt ona açıkça "bunu demek ayıp değil" diyor). */}
            <KPI label={tn('aiObs.rca.insufficient')}
              value={`${fmtNum(q.insufficientEvidence)} · ${rcaPctText(q.insufficientEvidence, q.total)}`} />
            <KPI label={tn('aiObs.rca.unparsed')}
              value={`${fmtNum(q.unparsed)} · ${rcaPctText(q.unparsed, q.total)}`}
              cls={rcaEngineTone(q)} />
            <KPI label={tn('aiObs.rca.shielded')}
              value={`${fmtNum(q.shielded)} · ${rcaPctText(q.shielded, q.total)}`}
              cls={rcaEngineTone(q)} />
            <KPI label={tn('aiObs.rca.repaired')}
              value={`${fmtNum(q.repaired)} · ${rcaPctText(q.repaired, q.total)}`} />
            <KPI label={tn('aiObs.rca.avgConf')} value={fixed(q.avgConfidence, 2, lang)} />
            <KPI label={tn('aiObs.rca.satisfaction')} value={rcaSatisfactionText(q)} />
          </div>
        )}
        {/* v0.10.410 (CoSRE denetimi E4) — güven kalibrasyonu: "0.8 dediğinde
            haklı mı?" Sabit 3 satır → ham tablo meşru (tasarım sistemi §5). */}
        {q && q.total > 0 && q.calibration?.length > 0 && (
          <div style={{ marginTop: 12 }}>
            <div className="ov-sub" style={{ marginBottom: 6 }}>
              {tn('aiObs.rca.calib')}
              {rcaCalibrationNote(q.calibration) && (
                <span className="badge b-warn" style={{ marginLeft: 8 }}>{rcaCalibrationNote(q.calibration)}</span>
              )}
            </div>
            <table style={{ width: "auto", minWidth: 420 }}>
              <thead>
                <tr><th>{tn('aiObs.rca.bucket')}</th><th>{tn('aiObs.rca.verdict')}</th><th>👍</th><th>👎</th><th>{tn('aiObs.bd.satisfaction')}</th></tr>
              </thead>
              <tbody>
                {q.calibration.map(b => (
                  <tr key={b.bucket}>
                    <td>{rcaBucketLabel(b)}</td>
                    <td>{fmtNum(b.total)}</td>
                    <td>{fmtNum(b.thumbsUp)}</td>
                    <td>{fmtNum(b.thumbsDown)}</td>
                    <td>{rcaBucketSatisfaction(b)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

// RouterGapsPanel — v0.9.549. Guided router'ın YAKALAYAMADIĞI sorular.
//
// CoSRE 16 intent tanıyor; tanımayan soru serbest tool döngüsüne
// düşüyor. O döngü çalışır ama pahalıdır (N tur LLM çağrısı) ve küçük
// modelde kalitesi düşüktür. Asıl mesele: hangi soruların düştüğünü
// kimse GÖRMÜYORDU, dolayısıyla "sıradaki intent ne olmalı" sorusu
// sezgiyle cevaplanıyordu.
//
// Yeni kayıt gerekmedi: serbest döngü zaten ai_calls'a surface='chat'
// yazıyor ve prompt_sample kullanıcının sorusunun ta kendisi. Guided
// yol 'chat-guided' yazıyor — ayrım hazır duruyordu.
//
// "Kullanıcı" kolonu bilinçli: tek kişinin ısrarla denediği bir soru
// ile ekibin tamamının sorduğu soru aynı öncelikte değil, ve sayı tek
// başına bunu ayırt edemiyor.
// v0.10.947 (tablo standardı T1) — sunucu LIMIT 50: kayıt listesi,
// useDataTable. initialSort yok — satırlar sunucunun sıklık sırasında kalır.
// Soru kimlik kolonu: 11.5px düştü, ton almadı; tam soru satırın değil
// hücrenin title'ında (T7). Damga mono kalır (S2).
// v0.10.2134 (router boşlukları D1) — "Tür" kolonu: serbest döngü ile genel
// cevap (sınıflandırıcı none) ayrı satır; ön-getirmeyle tohumlanan döngü
// (chat-prefetch) listede yok. Toplamlar gerçek sayı (eskiden ilk 50 grup).
// v0.10.2448 (operatör, 2026-11-22) — satır TUR sayar (sunucu exchange kökü
// başına toplar): none işareti + ardından gelen serbest döngü artık tek tur,
// son rota "Serbest döngü"; yedek zinciri Tür hücresinin title'ında. "Kim"
// soranların e-postaları (uç yalnız admin; e-postasız eski satır "—").
const gapCols = (lang: Lang): ColumnDef<RouterGap>[] => [
  { id: 'question', label: t('aiObs.gapCol.question', lang), sortValue: g => g.question, naturalDir: 'asc', flex: true, mono: true },
  { id: 'kind',     label: t('aiObs.gapCol.kind', lang), sortValue: g => g.kind, naturalDir: 'asc', width: 120, tone: () => 'faint' },
  { id: 'count',    label: t('aiObs.gapCol.count', lang), sortValue: g => g.count, numeric: true, width: 80 },
  { id: 'users',    label: t('aiObs.gapCol.users', lang), sortValue: g => g.users, numeric: true, width: 100, tone: () => 'faint' },
  { id: 'who',      label: t('aiObs.gapCol.who', lang), sortValue: g => gapWho(g), naturalDir: 'asc', width: 200, tone: () => 'faint' },
  { id: 'last',     label: t('aiObs.gapCol.last', lang), sortValue: g => g.lastAt, width: 165, tone: () => 'faint' },
];

function RouterGapsPanel() {
  const lang = useLang();
  const tn = useT();
  const gapColumns = useMemo(() => gapCols(lang), [lang]);
  const [days, setDays] = useState<1 | 7 | 30>(7);
  const [data, setData] = useState<RouterGapsView | null | undefined>(undefined);
  const [kind, setKind] = useState<GapKindFilter>('all'); // v0.10.2465 — tür süzgeci
  useEffect(() => {
    let cancelled = false;
    setData(undefined);
    api.aiRouterGaps(days)
      .then(r => { if (!cancelled) setData(r); })
      .catch(() => { if (!cancelled) setData(null); });
    return () => { cancelled = true; };
  }, [days]);
  const gapRows = useMemo(() => filterGapsByKind(data?.gaps ?? [], kind), [data, kind]);
  const gapDt = useDataTable<RouterGap>({ storageKey: 'ai-router-gaps', columns: gapColumns, rows: gapRows });
  // v0.10.954 — tablo standardı T12: durumlar tablonun İÇİNDE, başlık durur.
  const gapState: Omit<DataTableStateProps<RouterGap>, 'dt'> =
    data === undefined ? { kind: 'loading' }
    : data === null ? { kind: 'error' }
    : { kind: 'empty', message: tn('aiObs.gaps.empty') };
  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="ov-card-h">
        <h3>{tn('aiObs.gaps.title')}</h3>
        <span className="ov-sub">
          {tn('aiObs.gaps.sub')}
        </span>
        <span style={{ marginLeft: 'auto' }}>
          <SegmentedControl size="sm" aria-label={tn('aiObs.gaps.window')} value={String(days)}
            onChange={v => setDays(v === '30' ? 30 : v === '7' ? 7 : 1)}
            options={([1, 7, 30] as const).map(d => ({ value: String(d), label: tf('aiObs.gaps.days', lang, { d }) }))} />
        </span>
      </div>
      <div className="ov-card-b">
        {data && data.gaps.length > 0 && (
          <div style={{ fontSize: 11.5, color: 'var(--text2)', marginBottom: 8 }}>
            {tfNodes(t('aiObs.gaps.summary', lang), {
              fallbacks: <b>{count(data.totalFallbacks, lang)}</b>,
              general: <b>{count(data.totalGeneral, lang)}</b>,
              clarify: <b>{count(data.totalClarify ?? 0, lang)}</b>,
            })}
          </div>
        )}
        {data && data.gaps.length > 0 && (
          <div style={{ marginBottom: 8 }}>
            <SegmentedControl size="sm" aria-label={tn('aiObs.gapCol.kind')} value={kind}
              onChange={v => setKind(v as GapKindFilter)} options={gapKindOptions(data.gaps)} />
          </div>
        )}
        <div className="table-wrap">
          <table {...gapDt.tableProps}>
            <DataTableColgroup dt={gapDt} />
            <DataTableHead dt={gapDt} />
            <tbody>
              {gapDt.sortedRows.length === 0 ? <DataTableState dt={gapDt} {...gapState} /> : gapDt.sortedRows.map(g => (
                <tr key={`${g.kind}:${g.question}`}>
                  <DataTableCell dt={gapDt} col="question" row={g} value={g.question} />
                  <DataTableCell dt={gapDt} col="kind" row={g} value={GAP_KIND_LABEL[g.kind] ?? g.kind} title={gapChainTitle(g)} />
                  <DataTableCell dt={gapDt} col="count" row={g} value={count(g.count, lang)} />
                  <DataTableCell dt={gapDt} col="users" row={g} value={g.users} />
                  <DataTableCell dt={gapDt} col="who" row={g} value={gapWho(g) || null} title={gapWho(g) || undefined} />
                  <DataTableCell dt={gapDt} col="last" row={g} value={tsLong(g.lastAt)} className="mono" />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

// v0.10.947 (tablo standardı T1) — sunucu LIMIT 100: kayıt listesi,
// useDataTable; initialSort yok (sunucu sırası). Satırın "Cevap" ipucu
// Soru hücresine taşındı (T7: veri ipucu değeri gösteren hücrede).
const negCols = (lang: Lang): ColumnDef<NegativeFeedbackCall>[] => [
  { id: 'surface', label: t('aiObs.col.surface', lang), sortValue: r => r.surface, naturalDir: 'asc', width: 190 },
  { id: 'prompt',  label: t('aiObs.gapCol.question', lang), sortValue: r => r.prompt, naturalDir: 'asc', flex: true, mono: true },
  { id: 'comment', label: t('aiObs.negCol.comment', lang), sortValue: r => r.comment ?? '', naturalDir: 'asc', width: 280 },
  { id: 'when',    label: t('aiObs.negCol.when', lang), sortValue: r => r.createdAt, width: 165, tone: () => 'faint' },
  { id: 'who',     label: t('aiObs.gapCol.who', lang), sortValue: r => r.userEmail ?? '', naturalDir: 'asc', width: 175, tone: () => 'faint' },
];

function NegativeFeedbackPanel() {
  const lang = useLang();
  const tn = useT();
  const negColumns = useMemo(() => negCols(lang), [lang]);
  const [rows, setRows] = useState<NegativeFeedbackCall[] | null | undefined>(undefined);
  useEffect(() => {
    let cancelled = false;
    api.aiNegativeFeedback()
      .then(r => { if (!cancelled) setRows(r.rows); })
      .catch(() => { if (!cancelled) setRows(null); });
    return () => { cancelled = true; };
  }, []);
  const negDt = useDataTable<NegativeFeedbackCall>({ storageKey: 'ai-negative-feedback', columns: negColumns, rows: rows ?? [] });
  // v0.10.954 — tablo standardı T12: durumlar tablonun İÇİNDE, başlık durur.
  const negState: Omit<DataTableStateProps<NegativeFeedbackCall>, 'dt'> =
    rows === undefined ? { kind: 'loading' }
    : rows === null ? { kind: 'error' }
    : { kind: 'empty', message: tn('aiObs.neg.empty') };
  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="ov-card-h">
        <h3>{tn('aiObs.neg.title')}</h3>
        <span className="ov-sub">{tn('aiObs.neg.sub')}</span>
        {/* v0.10.423 (CoSRE denetimi E5) — evalset vakası olarak indir;
            prompt gövdeleri sistemi terk eder (audit'li), repoya ELLE alınır. */}
        {rows && rows.length > 0 && (
          <Button variant="secondary" size="sm" style={{ marginLeft: 'auto' }}
            onClick={() => { api.aiEvalsetExport().catch(() => { /* hata: indirme olmaz, sayfa bozulmaz */ }); }}
            title={tn('aiObs.neg.exportTitle')}>
            {/* i18n-exempt: dosya biçimi adı */}
            ↓ Evalset (JSON)
          </Button>
        )}
      </div>
      <div className="ov-card-b">
        <div className="table-wrap">
          <table {...negDt.tableProps}>
            <DataTableColgroup dt={negDt} />
            <DataTableHead dt={negDt} />
            <tbody>
              {negDt.sortedRows.length === 0 ? <DataTableState dt={negDt} {...negState} /> : negDt.sortedRows.map((r, i) => (
                <tr key={i}>
                  <DataTableCell dt={negDt} col="surface" row={r}><span className="badge b-gray">{r.surface || '—'}</span></DataTableCell>
                  <DataTableCell dt={negDt} col="prompt" row={r} value={r.prompt || null}
                    title={r.response ? tf('aiObs.neg.answerTitle', lang, { text: r.response.slice(0, 400) }) : undefined} />
                  {/* v0.9.1193 — 👎'nin NEDENİ. Kırpma title'da tamamlanır;
                      yorum çoğu satırda boş kalacak (opsiyonel), '—' ile
                      "yorum yazılmadı" dürüstçe görünür. */}
                  <DataTableCell dt={negDt} col="comment" row={r} value={r.comment || null} />
                  <DataTableCell dt={negDt} col="when" row={r} value={tsLong(r.createdAt)} className="mono" />
                  <DataTableCell dt={negDt} col="who" row={r} value={r.userEmail || null} />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function KPI({ label, value, cls }: { label: string; value: string; cls?: 'ok' | 'warn' | 'err' }) {
  // v0.10.929 (K5) — 'ok' tonu (sağlıklı KPI) nötr metin; renk yalnız warn/err
  // sapmasında. Ton ADI görünüm modellerinde (aiBudgetView, rcaQualityView) kalır.
  const color = cls === 'err' ? 'var(--err)'
    : cls === 'warn' ? 'var(--warn)' : 'var(--text)';
  return (
    <div style={{
      padding: '10px 12px', borderRadius: 6,
      background: 'var(--bg2)', border: '1px solid var(--border)',
    }}>
      <div style={{ fontSize: 10, color: 'var(--text3)', textTransform: 'uppercase', letterSpacing: 0.4 }}>
        {label}
      </div>
      <div style={{ fontSize: 18, fontWeight: 600, color, marginTop: 4, fontFamily: 'var(--font-mono)' }}>
        {value}
      </div>
    </div>
  );
}

function BreakdownTable({ title, rows, cols, onPickFirst }: {
  title: string;
  // `e` (v0.8.399) — optional 5th column; only the by-surface table
  // uses it (Memnuniyet), the provider table stays 4 columns.
  rows: Array<{ a: string; b: string; c: string; d: string; e?: string }>;
  cols: [string, string, string, string] | [string, string, string, string, string];
  onPickFirst: (v: string) => void;
}) {
  return (
    <div style={{
      background: 'var(--bg2)', border: '1px solid var(--border)',
      borderRadius: 6, padding: 12,
    }}>
      <div style={{ fontSize: 12, fontWeight: 600, marginBottom: 8 }}>{title}</div>
      <div className="table-wrap is-scroll" style={{ maxHeight: 220 }}>
        {/* v0.10.947 — statik tablo (T1): satırlar biçimli dizge (sıralama
            anahtarı yok) ve ≥360px ızgara kartında otomatik düzen; sabit
            kolon genişlikleri bugünkü dağılıma yaklaşamaz. 11px ikincil
            sayılar tablo boyunda, renkle (S3). */}
        <table>
          <thead><tr>
            {cols.map(c => <th key={c}>{c}</th>)}
          </tr></thead>
          <tbody>
            {rows.map((r, i) => (
              <tr key={i} {...rowActivation(() => onPickFirst(r.a))}>
                <td className="mono">{r.a}</td>
                <td className="num">{r.b}</td>
                <td className="num cell-muted">{r.c}</td>
                <td className="num cell-muted">{r.d}</td>
                {cols.length === 5 && (
                  <td className="num cell-muted">{r.e ?? '—'}</td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// CallsChart — tiny inline SVG line chart of call volume + error
// volume + avg-latency. No chart lib — at 120 points the geometry
// is one path per series and there's no need to pay the bundle
// cost of recharts/visx.
function CallsChart({ series }: { series: AICallsTimePoint[] }) {
  const tn = useT();
  const W = 1000, H = 140, PAD = 24;
  const xs = useMemo(() => series.map(p => p.time), [series]);
  const minT = xs[0] ?? 0;
  const maxT = xs[xs.length - 1] ?? 1;
  const maxCalls = Math.max(1, ...series.map(p => p.calls));
  const maxLatency = Math.max(1, ...series.map(p => p.avgMs));
  const xOf = (t: number) => PAD + ((t - minT) / Math.max(1, maxT - minT)) * (W - PAD * 2);
  const yCalls = (v: number) => H - PAD - (v / maxCalls) * (H - PAD * 2);
  const yLatency = (v: number) => H - PAD - (v / maxLatency) * (H - PAD * 2);
  const callsPath = series.map((p, i) =>
    `${i === 0 ? 'M' : 'L'} ${xOf(p.time).toFixed(1)} ${yCalls(p.calls).toFixed(1)}`
  ).join(' ');
  const errorsPath = series.map((p, i) =>
    `${i === 0 ? 'M' : 'L'} ${xOf(p.time).toFixed(1)} ${yCalls(p.errors).toFixed(1)}`
  ).join(' ');
  const latencyPath = series.map((p, i) =>
    `${i === 0 ? 'M' : 'L'} ${xOf(p.time).toFixed(1)} ${yLatency(p.avgMs).toFixed(1)}`
  ).join(' ');
  return (
    <div style={{
      background: 'var(--bg2)', border: '1px solid var(--border)',
      borderRadius: 6, padding: 10, marginBottom: 14,
    }}>
      <div style={{
        display: 'flex', gap: 14, fontSize: 11, color: 'var(--text3)',
        marginBottom: 4, paddingLeft: PAD,
      }}>
        <span><span style={{ color: 'var(--accent2)' }}>━</span> {tn('aiObs.chart.calls')}</span>
        <span><span style={{ color: 'var(--err)' }}>━</span> {tn('aiObs.chart.errors')}</span>
        <span><span style={{ color: 'var(--warn)' }}>━</span> {tn('aiObs.chart.latency')}</span>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H}
        preserveAspectRatio="none"
        style={{ display: 'block' }}>
        <path d={callsPath}   stroke="var(--accent2)" strokeWidth={1.5} fill="none" />
        <path d={errorsPath}  stroke="var(--err)"     strokeWidth={1.2} fill="none" />
        <path d={latencyPath} stroke="var(--warn)"    strokeWidth={1.2} fill="none"
          strokeDasharray="3,2" opacity={0.85} />
      </svg>
    </div>
  );
}

// v0.8.495 (sadeleştirme #2) — kabuk ui/Drawer'a taşındı: Esc/overlay/✕
// davranışı tek evden; içerik (Kv grid + örnek pre blokları) birebir.
function CallDrawer({ call, rates, onClose }: { call: AICall; rates: AIRateTable; onClose: () => void }) {
  const tn = useT();
  const cost = costForCall(rates, call.model, call.inputTokens, call.outputTokens);
  return (
    <Drawer onClose={onClose} width={680} header={
      <>
        <span className={`badge ${call.status === 'ok' ? 'b-gray' : 'b-err'}`}>
          {call.status}
        </span>
        <span style={{ fontWeight: 700, fontSize: 13 }}>{call.surface}</span>
        <span style={{ fontSize: 11, color: 'var(--text3)' }}>
          {call.provider} · {call.model || '—'} · {call.durationMs} ms
        </span>
      </>
    }>
        <div style={{ paddingTop: 10, display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 8 }}>
            <Kv k={tn('aiObs.col.time')} v={tsLong(call.createdAt)} />
            <Kv k={tn('aiObs.drawer.inputTok')} v={String(call.inputTokens)} />
            <Kv k={tn('aiObs.drawer.outputTok')} v={String(call.outputTokens)} />
            <Kv k={tn('aiObs.kpi.estCost')} v={fmtCost(cost)} />
            <Kv k={tn('aiObs.drawer.promptChars')} v={String(call.promptChars)} />
            <Kv k={tn('aiObs.drawer.respChars')} v={String(call.responseChars)} />
            <Kv k={tn('aiObs.col.user')} v={call.userEmail || call.userId || '—'} />
            {call.baseUrl && <Kv k={tn('aiObs.drawer.baseUrl')} v={call.baseUrl} />}
          </div>
          {/* v0.10.1463 (CoSRE D1) — çağrının self-telemetri trace'i: yalnız
              örneklenmiş (sorgulanabilir) trace'te dolu; arka plan anlatımında
              sre.investigation ağacını, sohbette ai.chat ağacını açar. */}
          {/* v0.10.1470 — link yalnız trace BU Coremetry'de sorgulanabilirken
              (traceQueryable, sunucu süreç bayrağı); dış collector'da kimlik
              düz metin + ipucu, boş /trace sayfasına link yok. */}
          {call.traceId && (
            <div style={{ fontSize: 12 }}>
              {call.traceQueryable
                ? <Link to={traceHref(call.traceId)} title={tn('aiObs.drawer.traceTitle')}>{tn('aiObs.drawer.trace')}</Link>
                : <span title={tn('aiObs.drawer.traceExternalTitle')} style={{ color: 'var(--text3)' }}>{tn('aiObs.drawer.traceExternal')}</span>}
              <span className="mono" style={{ marginLeft: 8, color: 'var(--text3)' }}>{call.traceId}</span>
            </div>
          )}
          {call.errorMsg && (
            <Section title={tn('aiObs.drawer.error')}>
              <pre style={{
                margin: 0, fontSize: 12, color: 'var(--err)',
                whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              }}>{call.errorMsg}</pre>
            </Section>
          )}
          <Section title={tn('aiObs.drawer.prompt')}>
            <pre style={{
              margin: 0, fontSize: 12,
              whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              maxHeight: 280, overflowY: 'auto',
              background: 'var(--bg2)', padding: 10, borderRadius: 4,
              border: '1px solid var(--border)',
            }}>{call.promptSample || tn('aiObs.drawer.empty')}</pre>
          </Section>
          <Section title={tn('aiObs.drawer.response')}>
            <pre style={{
              margin: 0, fontSize: 12,
              whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              maxHeight: 280, overflowY: 'auto',
              background: 'var(--bg2)', padding: 10, borderRadius: 4,
              border: '1px solid var(--border)',
            }}>{call.responseSample || tn('aiObs.drawer.empty')}</pre>
          </Section>
        </div>
    </Drawer>
  );
}

// tfNodes — tf'nin JSX hâli: {ad} yer tutucularına düğüm (kalın sayı) koyar.
function tfNodes(tpl: string, vars: Record<string, React.ReactNode>): React.ReactNode[] {
  return tpl.split(/(\{\w+\})/).map((part, i) => {
    const m = /^\{(\w+)\}$/.exec(part);
    return m && m[1] in vars ? <Fragment key={i}>{vars[m[1]]}</Fragment> : part;
  });
}

// Section — DrawerSection'ın ince sarmalayıcısı; yerel kopya v0.8.495'te
// paylaşılan primitife bağlandı.
function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return <DrawerSection title={title}>{children}</DrawerSection>;
}
function Kv({ k, v }: { k: string; v: string }) {
  return (
    <div>
      <div style={{ fontSize: 10, color: 'var(--text3)', textTransform: 'uppercase', letterSpacing: 0.4 }}>{k}</div>
      <div className="mono" style={{ marginTop: 2 }}>{v}</div>
    </div>
  );
}

// CSV escape — RFC 4180 minimum: wrap fields that contain
// commas/quotes/newlines in double quotes, and double-up any
// embedded quotes. We omit BOM since modern Excel handles UTF-8
// fine and operators piping into pandas/duckdb prefer it
// without.
function csvField(v: string | number): string {
  const s = String(v);
  if (/[",\n\r]/.test(s)) {
    return '"' + s.replace(/"/g, '""') + '"';
  }
  return s;
}

// exportCallsCSV — v0.5.174. Bundles the operator's currently-
// filtered ai_calls rows into a CSV file with a computed cost
// column (uses the same rate table the page renders with).
// Prompt + response samples are deliberately omitted — they can
// be 4KB each and the operator can drill in via the table row
// for individual inspection.
function exportCallsCSV(calls: AICall[], rates: AIRateTable) {
  const header = [
    'createdAt', 'surface', 'provider', 'model', 'status',
    'durationMs', 'inputTokens', 'outputTokens', 'estCostUsd',
    'userEmail', 'userId', 'errorMsg',
  ];
  const lines: string[] = [header.join(',')];
  for (const c of calls) {
    const cost = costForCall(rates, c.model, c.inputTokens, c.outputTokens);
    lines.push([
      new Date(c.createdAt / 1e6).toISOString(),
      c.surface, c.provider, c.model, c.status,
      c.durationMs, c.inputTokens, c.outputTokens,
      cost === null ? '' : cost.toFixed(6),
      c.userEmail ?? '', c.userId ?? '',
      c.errorMsg ?? '',
    ].map(csvField).join(','));
  }
  const blob = new Blob([lines.join('\n')], { type: 'text/csv;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `coremetry-ai-calls-${new Date().toISOString().slice(0, 10)}.csv`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}
