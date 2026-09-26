import { useEffect, useMemo, useState } from 'react';
import { rowActivation } from '@/lib/a11y'; // v0.10.451 (dış denetim D3 kalan)
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { Button, Drawer, DrawerSection, SegmentedControl } from '@/components/ui';
import { api } from '@/lib/api';
import { cachedPctLabel } from './ai/cachedTokens';
import { useUrlRange } from '@/lib/useUrlRange';
import { rcaPctText, rcaEngineTone, rcaSatisfactionText, rcaBucketLabel, rcaBucketSatisfaction, rcaCalibrationNote } from './ai/rcaQualityView';
import type { RCAVerdictQuality, AIBudgetStatus, NegativeFeedbackCall } from '@/lib/types';
import { budgetVerdict, budgetCls } from './ai/aiBudgetView';
import { timeRangeToNs, tsLong, fmtNum } from '@/lib/utils';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, type ColumnDef } from '@/components/ui/DataTable';
import {
  type AIRateTable, mergeRates, costForCall, fmtCost,
} from '@/lib/ai-rates';
import {
  type AICall, type AIStats, type AICallsTimePoint, type TimeRange, type AICallSource,
  EVALSET_SURFACE_PREFIX,
} from '@/lib/types';
import { useSearchParams } from 'react-router-dom';
import { PageControls } from '@/components/ui/PageControls';
import { PageShell } from '@/components/ui/PageShell';

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
export default function AIObservabilityPage() {
  const [range, setRange] = useUrlRange('24h');
  const [stats, setStats] = useState<AIStats | null | undefined>(undefined);
  const [budget, setBudget] = useState<AIBudgetStatus | null>(null); // v0.10.411
  const [series, setSeries] = useState<AICallsTimePoint[] | undefined>(undefined);
  const [calls, setCalls] = useState<AICall[] | null | undefined>(undefined);
  const [surface, setSurface] = useState('');
  const [provider, setProvider] = useState('');
  const [status, setStatus] = useState('');
  const [open, setOpen] = useState<AICall | null>(null);
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
    { id: 'time',     label: 'Time',              sortValue: c => c.createdAt, width: 165, tone: () => 'muted' },
    { id: 'surface',  label: 'Surface',           sortValue: c => c.surface,   naturalDir: 'asc', flex: true, mono: true },
    { id: 'model',    label: 'Provider · Model',  sortValue: c => `${c.provider} ${c.model ?? ''}`, naturalDir: 'asc', width: 210 },
    { id: 'status',   label: 'Status',            sortValue: c => c.status,    naturalDir: 'asc', width: 95 },
    { id: 'duration', label: 'Duration',          sortValue: c => c.durationMs, numeric: true, width: 105 },
    { id: 'tokens',   label: 'In / Out tokens',   sortValue: c => c.inputTokens + c.outputTokens, numeric: true, width: 140, tone: () => 'faint' },
    // Oran tablosunda olmayan bir model için costForCall null döner;
    // sıralamada -1 ile en dibe iner (bilinmeyen maliyet "ucuz" sayılmasın).
    { id: 'cost',     label: 'Cost',
      sortValue: c => costForCall(rates, c.model, c.inputTokens, c.outputTokens) ?? -1,
      numeric: true, width: 100,
      tone: c => (costForCall(rates, c.model, c.inputTokens, c.outputTokens) === null ? 'faint' : 'muted') },
    { id: 'user',     label: 'User',              sortValue: c => c.userEmail || c.userId || '', naturalDir: 'asc', width: 175, tone: () => 'faint' },
  ], [rates]);
  const dt = useDataTable<AICall>({
    storageKey: 'ai-calls', columns: callCols, rows: calls ?? [],
    initialSort: { id: 'time', dir: 'desc' },
  });

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
      <Topbar title="AI observability" range={range} onRangeChange={setRange} />
      <PageShell>
        <div style={{ color: 'var(--text2)', fontSize: 12, marginBottom: 12 }}>
          Every CoSRE call lands here — latency, tokens, status,
          per-surface breakdown. Prompt + response samples (≤4KB) are kept
          for inspection. Admin-only.
          {/* v0.10.940 — kaynak seçici süzgeç şeridinde, KPI'ların ALTINDA;
              değerlendirme görünümündeyken karoların neyi saydığı üstte de
              yazsın (bütçe karosu hep üretim). */}
          {source === 'evalset' && (
            <div className="field-hint">
              Kaynak: Değerlendirme — yalnız Settings › CoSRE › Değerlendirme koşularının
              çağrıları (yüzey evalset-…). Bütçe (24 sa) karosu her zaman üretimi gösterir.
            </div>
          )}
        </div>

        {/* KPI cards */}
        {stats === undefined && <Spinner />}
        {stats === null && (
          <Empty icon="✗" title="Failed to load AI stats">
            Check that CoSRE is configured and that ai_calls table exists.
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
              <KPI label="Total calls" value={fmtNum(stats.totalCalls)} />
              <KPI label="Error rate" value={`${(stats.errorRate * 100).toFixed(2)}%`}
                cls={stats.errorRate > 0.1 ? 'err' : stats.errorRate > 0.02 ? 'warn' : 'ok'} />
              <KPI label="Avg latency" value={`${stats.avgDurationMs.toFixed(0)} ms`} />
              <KPI label="P99 latency" value={`${stats.p99DurationMs.toFixed(0)} ms`} />
              <KPI label="Input tokens" value={fmtNum(stats.inputTokens)} />
              <KPI label="Output tokens" value={fmtNum(stats.outputTokens)} />
              {/* v0.10.807 (L2) — önek önbelleği: giriş token'ının yüzde kaçı önbellekten geldi.
                  0 iken de gösterilir (kolon varsa): "uç bildirmiyor ya da önbellek kapalı" sinyali. */}
              {stats.cachedCol && (
                <KPI label="Önbellek (giriş)" value={cachedPctLabel(stats.cachedTokens ?? 0, stats.inputTokens)}
                  cls={(stats.cachedTokens ?? 0) > 0 ? 'ok' : undefined} />
              )}
              <KPI label="Est cost" value={totalCostLabel} />
              <KPI label="Bütçe (24 sa)" value={bv.text} cls={budgetCls(bv)} />
              {/* v0.10.421 (E6) — uydurma oranı; v0.10.409'un ilk-token karosu da burada. */}
              {stats.extended && (
                <KPI label="Kalkan isabeti" value={`${fmtNum(stats.shieldHitCalls ?? 0)} çağrı · ${fmtNum(stats.shieldHits ?? 0)} ad`}
                  cls={(stats.shieldHitCalls ?? 0) > 0 ? 'warn' : 'ok'} />
              )}
              {stats.extended && (stats.avgTtftMs ?? 0) > 0 && (
                <KPI label="Ort. ilk token" value={`${(stats.avgTtftMs ?? 0).toFixed(0)} ms`} />
              )}
              <KPI label="Users" value={fmtNum(stats.distinctUsers)} />
            </div>

            {/* Volume + error timeseries */}
            {series && series.length > 0 && (
              <CallsChart series={series} />
            )}

            {/* Per-surface + per-provider breakdowns */}
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))', gap: 12, marginTop: 14 }}>
              {(stats.bySurface ?? []).length > 0 && (
                <BreakdownTable title="By surface (which Explain button)"
                  rows={(stats.bySurface ?? []).map(r => ({
                    a: r.surface, b: fmtNum(r.calls),
                    c: `${(r.errorRate * 100).toFixed(1)}%`,
                    d: `${r.avgMs.toFixed(0)} ms`,
                    // Memnuniyet (v0.8.399) — thumbs-up rate over rated
                    // exchanges. thumbsUpRate is omitempty server-side,
                    // so an absent rate with feedback present means 0%.
                    e: r.feedbackCount
                      ? `${Math.round((r.thumbsUpRate ?? 0) * 100)}% (${r.feedbackCount})`
                      : '—',
                  }))}
                  cols={['Surface', 'Calls', 'Err rate', 'Avg ms', 'Memnuniyet']}
                  onPickFirst={v => setSurface(v)} />
              )}
              {(stats.byProvider ?? []).length > 0 && (
                <BreakdownTable title="By provider · model"
                  rows={(stats.byProvider ?? []).map(r => ({
                    a: `${r.provider} · ${r.model || '—'}`, b: fmtNum(r.calls),
                    // v0.10.400 — model başına hata oranı + p95 gecikme (CoSRE denetimi O5/E3).
                    c: `${r.calls ? ((r.errors ?? 0) / r.calls * 100).toFixed(1) : '0.0'}% · p95 ${fmtNum(Math.round(r.p95Ms ?? 0))} ms`,
                    d: fmtCost(costForCall(rates, r.model, r.inputTokens, r.outputTokens)),
                  }))}
                  cols={['Provider', 'Calls', 'Hata · p95', 'Est cost']}
                  onPickFirst={v => setProvider(v.split(' · ')[0])} />
              )}
            </div>
          </>
          );
        })()}

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
              düz select (frontend-conventions §3, ≤10 sabit değer). Boyut
              `.ai-filter-select`te, satır içinde değil: satır içi 12px
              yoğunluk kurallarını ve telefondaki iOS 16px kuralını ezerdi;
              sağlayıcı / durum seçicileri de aynı sınıfa geçti. */}
          <select value={source ?? ''} onChange={e => setSource(e.target.value)}
            aria-label="Kaynak"
            className="ai-filter-select">
            <option value="">Üretim</option>
            <option value="evalset">Değerlendirme</option>
          </select>
          <input type="search" placeholder="Filter by surface…" aria-label="Filter by surface"
            data-shortcut-search
            value={surface} onChange={e => setSurface(e.target.value)}
            style={{ fontSize: 12, padding: '3px 8px', width: 200 }} />
          <select value={provider} onChange={e => setProvider(e.target.value)}
            aria-label="Filter by provider"
            className="ai-filter-select">
            <option value="">All providers</option>
            <option value="openai">openai</option>
            <option value="anthropic">anthropic</option>
            <option value="github">github</option>
          </select>
          <select value={status} onChange={e => setStatus(e.target.value)}
            aria-label="Filter by status"
            className="ai-filter-select">
            <option value="">All statuses</option>
            <option value="ok">ok</option>
            <option value="error">error</option>
          </select>
          {(surface || provider || status) && (
            <Button variant="secondary" size="sm"
              onClick={() => { setSurface(''); setProvider(''); setStatus(''); }}>Clear</Button>
          )}
          <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--text3)' }}>
            {calls ? `${calls.length} call${calls.length === 1 ? '' : 's'}` : ''}
          </span>
          {calls && calls.length > 0 && (
            <Button variant="secondary" size="sm"
              onClick={() => exportCallsCSV(calls, rates)}
              title="Download the currently-filtered calls as a CSV (with computed cost)">
              ↓ CSV
            </Button>
          )}
        </PageControls>

        {/* Recent calls table */}
        {calls === undefined && <Spinner />}
        {calls === null && (
          <Empty icon="✗" title="Failed to load calls" />
        )}
        {calls && calls.length === 0 && (
          <Empty icon="◇" title="No AI calls in this window">
            Click an "✨ Explain" button anywhere in Coremetry — it lands here.
          </Empty>
        )}
        {calls && calls.length > 0 && (
          <>
          <div className="table-wrap">
            <table {...dt.tableProps}>
              <DataTableColgroup dt={dt} />
              <DataTableHead dt={dt} />
              <tbody>
                {dt.sortedRows.map(c => {
                  const cost = costForCall(rates, c.model, c.inputTokens, c.outputTokens);
                  return (
                  <tr key={c.id} className="cv-row" {...rowActivation(() => setOpen(c))}>
                    {/* v0.10.947 — zaman damgası mono kalır (S2 yalnız sayıyı kapsar). */}
                    <DataTableCell dt={dt} col="time" row={c} value={tsLong(c.createdAt)} className="mono" />
                    <DataTableCell dt={dt} col="surface" row={c} value={c.surface} />
                    <DataTableCell dt={dt} col="model" row={c}>
                      <span style={{ color: 'var(--text2)' }}>{c.provider}</span>
                      {c.model && <span style={{ color: 'var(--text3)' }}> · {c.model}</span>}
                    </DataTableCell>
                    <DataTableCell dt={dt} col="status" row={c}>
                      {c.status === 'ok'
                        ? <span className="badge b-gray">ok</span>
                        : <span className="badge b-err">error</span>}
                    </DataTableCell>
                    <DataTableCell dt={dt} col="duration" row={c} value={`${c.durationMs} ms`} />
                    <DataTableCell dt={dt} col="tokens" row={c}
                      title={c.cachedTokens ? `${c.cachedTokens} giriş token'ı önek önbelleğinden` : undefined}>
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
            {calls.length >= 200 && (
              <div className="pager" style={{ color: 'var(--text3)' }}>
                son 200 çağrı gösteriliyor — tavana dayandı; daha fazlası için
                pencereyi daraltın
              </div>
            )}
          </>
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
        <NegativeFeedbackPanel />

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
        <h3>Kök-neden hakem kalitesi</h3>
        <span className="ov-sub">
          transport değil, CEVABIN kendisi — karar dağılımı, motor sağlığı, operatör yargısı
        </span>
      </div>
      <div className="ov-card-b">
        {q === undefined && <Spinner />}
        {q === null && <Empty icon="✗" title="Okunamadı" />}
        {q && q.total === 0 && (
          <Empty icon="○" title="Bu pencerede hiç verdict üretilmemiş">
            Bir problem/anomali kartında ✨ Explain'e basıldığında burada görünür.
          </Empty>
        )}
        {q && q.total > 0 && (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 10 }}>
            <KPI label="Verdict" value={fmtNum(q.total)} />
            <KPI label="Kök neden belirlendi"
              value={`${fmtNum(q.rootCauseIdentified)} · ${rcaPctText(q.rootCauseIdentified, q.total)}`} />
            <KPI label="Olası neden"
              value={`${fmtNum(q.probableCause)} · ${rcaPctText(q.probableCause, q.total)}`} />
            {/* Kanıt yetersiz NÖTR çizilir: bir arıza değil, geçerli
                bir cevap. Kırmızı yapmak modeli tam da kaçınmasını
                istemediğimiz yöne — kendinden emin ve yanlış cevaba —
                iter (prompt ona açıkça "bunu demek ayıp değil" diyor). */}
            <KPI label="Kanıt yetersiz"
              value={`${fmtNum(q.insufficientEvidence)} · ${rcaPctText(q.insufficientEvidence, q.total)}`} />
            <KPI label="Model çözümlenemedi"
              value={`${fmtNum(q.unparsed)} · ${rcaPctText(q.unparsed, q.total)}`}
              cls={rcaEngineTone(q)} />
            <KPI label="Kalkan devrede"
              value={`${fmtNum(q.shielded)} · ${rcaPctText(q.shielded, q.total)}`}
              cls={rcaEngineTone(q)} />
            <KPI label="Onarım gerekti"
              value={`${fmtNum(q.repaired)} · ${rcaPctText(q.repaired, q.total)}`} />
            <KPI label="Ortalama güven" value={q.avgConfidence.toFixed(2)} />
            <KPI label="Operatör memnuniyeti" value={rcaSatisfactionText(q)} />
          </div>
        )}
        {/* v0.10.410 (CoSRE denetimi E4) — güven kalibrasyonu: "0.8 dediğinde
            haklı mı?" Sabit 3 satır → ham tablo meşru (tasarım sistemi §5). */}
        {q && q.total > 0 && q.calibration?.length > 0 && (
          <div style={{ marginTop: 12 }}>
            <div className="ov-sub" style={{ marginBottom: 6 }}>
              Güven kalibrasyonu — kova başına operatör yargısı (yalnız çözümlenmiş verdict'ler)
              {rcaCalibrationNote(q.calibration) && (
                <span className="badge b-warn" style={{ marginLeft: 8 }}>{rcaCalibrationNote(q.calibration)}</span>
              )}
            </div>
            <table style={{ width: "auto", minWidth: 420 }}>
              <thead>
                <tr><th>Güven kovası</th><th>Verdict</th><th>👍</th><th>👎</th><th>Memnuniyet</th></tr>
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
type RouterGap = Awaited<ReturnType<typeof api.aiRouterGaps>>['gaps'][number];
const GAP_COLS: ColumnDef<RouterGap>[] = [
  { id: 'question', label: 'Soru',      sortValue: g => g.question, naturalDir: 'asc', flex: true, mono: true },
  { id: 'count',    label: 'Kez',       sortValue: g => g.count, numeric: true, width: 80 },
  { id: 'users',    label: 'Kullanıcı', sortValue: g => g.users, numeric: true, width: 100, tone: () => 'faint' },
  { id: 'last',     label: 'Son',       sortValue: g => g.lastAt, width: 165, tone: () => 'faint' },
];

function RouterGapsPanel() {
  const [days, setDays] = useState<1 | 7 | 30>(7);
  const [data, setData] = useState<Awaited<ReturnType<typeof api.aiRouterGaps>> | null | undefined>(undefined);
  useEffect(() => {
    let cancelled = false;
    setData(undefined);
    api.aiRouterGaps(days)
      .then(r => { if (!cancelled) setData(r); })
      .catch(() => { if (!cancelled) setData(null); });
    return () => { cancelled = true; };
  }, [days]);
  const gapDt = useDataTable<RouterGap>({ storageKey: 'ai-router-gaps', columns: GAP_COLS, rows: data?.gaps ?? [] });
  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="ov-card-h">
        <h3>Router boşlukları</h3>
        <span className="ov-sub">
          guided intent'e OTURMAYAN sorular — serbest tool döngüsüne düştüler ya da sınıflandırıcı «none» dedi (v0.10.172)
        </span>
        <span style={{ marginLeft: 'auto' }}>
          <SegmentedControl size="sm" aria-label="Gün penceresi" value={String(days)}
            onChange={v => setDays(v === '30' ? 30 : v === '7' ? 7 : 1)}
            options={([1, 7, 30] as const).map(d => ({ value: String(d), label: `${d}g` }))} />
        </span>
      </div>
      <div className="ov-card-b">
        {data === undefined && <Spinner />}
        {data === null && <Empty icon="✗" title="Okunamadı" />}
        {data && data.gaps.length === 0 && (
          <div style={{ color: 'var(--text3)', fontSize: 12 }}>
            Bu pencerede serbest döngüye düşen soru yok — router her soruyu yakalamış.
          </div>
        )}
        {data && data.gaps.length > 0 && (
          <>
            <div style={{ fontSize: 11.5, color: 'var(--text2)', marginBottom: 8 }}>
              Toplam <b>{data.totalFallbacks.toLocaleString()}</b> soru serbest döngüye
              düştü. Sık tekrarlayanlar yeni bir guided intent adayıdır: deterministik
              prefetch + tek anlatım çağrısı, N turluk tool döngüsünden hem ucuz hem
              tutarlı olur.
            </div>
            <div className="table-wrap">
              <table {...gapDt.tableProps}>
                <DataTableColgroup dt={gapDt} />
                <DataTableHead dt={gapDt} />
                <tbody>
                  {gapDt.sortedRows.map(g => (
                    <tr key={g.question}>
                      <DataTableCell dt={gapDt} col="question" row={g} value={g.question} />
                      <DataTableCell dt={gapDt} col="count" row={g} value={g.count.toLocaleString()} />
                      <DataTableCell dt={gapDt} col="users" row={g} value={g.users} />
                      <DataTableCell dt={gapDt} col="last" row={g} value={tsLong(g.lastAt)} className="mono" />
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

// v0.10.947 (tablo standardı T1) — sunucu LIMIT 100: kayıt listesi,
// useDataTable; initialSort yok (sunucu sırası). Satırın "Cevap" ipucu
// Soru hücresine taşındı (T7: veri ipucu değeri gösteren hücrede).
const NEG_COLS: ColumnDef<NegativeFeedbackCall>[] = [
  { id: 'surface', label: 'Yüzey',    sortValue: r => r.surface, naturalDir: 'asc', width: 190 },
  { id: 'prompt',  label: 'Soru',     sortValue: r => r.prompt, naturalDir: 'asc', flex: true, mono: true },
  { id: 'comment', label: 'Yorum',    sortValue: r => r.comment ?? '', naturalDir: 'asc', width: 280 },
  { id: 'when',    label: 'Ne zaman', sortValue: r => r.createdAt, width: 165, tone: () => 'faint' },
  { id: 'who',     label: 'Kim',      sortValue: r => r.userEmail ?? '', naturalDir: 'asc', width: 175, tone: () => 'faint' },
];

function NegativeFeedbackPanel() {
  const [rows, setRows] = useState<NegativeFeedbackCall[] | null | undefined>(undefined);
  useEffect(() => {
    let cancelled = false;
    api.aiNegativeFeedback()
      .then(r => { if (!cancelled) setRows(r.rows); })
      .catch(() => { if (!cancelled) setRows(null); });
    return () => { cancelled = true; };
  }, []);
  const negDt = useDataTable<NegativeFeedbackCall>({ storageKey: 'ai-negative-feedback', columns: NEG_COLS, rows: rows ?? [] });
  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="ov-card-h">
        <h3>👎 alan cevaplar</h3>
        <span className="ov-sub">son 7 gün — yeni guided-intent adayları buradan çıkar</span>
        {/* v0.10.423 (CoSRE denetimi E5) — evalset vakası olarak indir;
            prompt gövdeleri sistemi terk eder (audit'li), repoya ELLE alınır. */}
        {rows && rows.length > 0 && (
          <Button variant="secondary" size="sm" style={{ marginLeft: 'auto' }}
            onClick={() => { api.aiEvalsetExport().catch(() => { /* hata: indirme olmaz, sayfa bozulmaz */ }); }}
            title="Bu 👎 satırlarını coremetry.evalset/1 JSON'u olarak indir (internal/copilot/evalset/ için ham malzeme)">
            ↓ Evalset (JSON)
          </Button>
        )}
      </div>
      <div className="ov-card-b">
        {rows === undefined && <Spinner />}
        {rows === null && <Empty icon="✗" title="Feedback okunamadı" />}
        {rows && rows.length === 0 && (
          <div style={{ color: 'var(--text3)', fontSize: 12 }}>Son 7 günde 👎 yok. 🎉</div>
        )}
        {rows && rows.length > 0 && (
          <div className="table-wrap">
            <table {...negDt.tableProps}>
              <DataTableColgroup dt={negDt} />
              <DataTableHead dt={negDt} />
              <tbody>
                {negDt.sortedRows.map((r, i) => (
                  <tr key={i}>
                    <DataTableCell dt={negDt} col="surface" row={r}><span className="badge b-gray">{r.surface || '—'}</span></DataTableCell>
                    <DataTableCell dt={negDt} col="prompt" row={r} value={r.prompt || null}
                      title={r.response ? `Cevap: ${r.response.slice(0, 400)}` : undefined} />
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
        )}
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
        <span><span style={{ color: 'var(--accent2)' }}>━</span> calls</span>
        <span><span style={{ color: 'var(--err)' }}>━</span> errors</span>
        <span><span style={{ color: 'var(--warn)' }}>━</span> avg latency (ms)</span>
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
            <Kv k="Time" v={tsLong(call.createdAt)} />
            <Kv k="Input tok" v={String(call.inputTokens)} />
            <Kv k="Output tok" v={String(call.outputTokens)} />
            <Kv k="Est cost" v={fmtCost(cost)} />
            <Kv k="Prompt chars" v={String(call.promptChars)} />
            <Kv k="Resp chars" v={String(call.responseChars)} />
            <Kv k="User" v={call.userEmail || call.userId || '—'} />
            {call.baseUrl && <Kv k="Base URL" v={call.baseUrl} />}
          </div>
          {call.errorMsg && (
            <Section title="Error">
              <pre style={{
                margin: 0, fontSize: 12, color: 'var(--err)',
                whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              }}>{call.errorMsg}</pre>
            </Section>
          )}
          <Section title="Prompt (sample)">
            <pre style={{
              margin: 0, fontSize: 12,
              whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              maxHeight: 280, overflowY: 'auto',
              background: 'var(--bg2)', padding: 10, borderRadius: 4,
              border: '1px solid var(--border)',
            }}>{call.promptSample || '(empty)'}</pre>
          </Section>
          <Section title="Response (sample)">
            <pre style={{
              margin: 0, fontSize: 12,
              whiteSpace: 'pre-wrap', wordBreak: 'break-word',
              maxHeight: 280, overflowY: 'auto',
              background: 'var(--bg2)', padding: 10, borderRadius: 4,
              border: '1px solid var(--border)',
            }}>{call.responseSample || '(empty)'}</pre>
          </Section>
        </div>
    </Drawer>
  );
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
