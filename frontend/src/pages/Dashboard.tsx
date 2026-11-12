import { Suspense, useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import { useEscLayer } from '@/lib/escLayer';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { Button, IconButton, MenuItem, useConfirm } from '@/components/ui';
import { LazyMount } from '@/components/LazyMount';
import { PanelFrameMenu } from '@/components/coremetry/dashboard/PanelFrameMenu';
import { DASH_GRID_CLASS, panelSpan, panelSpanClass } from '@/components/coremetry/dashboard/grid';
import { PanelMenuProvider, usePanelMenuFrame } from '@/components/dashboard/panelMenuHost';
import { panelChartHeight } from '@/components/dashboard/panelChrome';
import type { PanelMenuAction } from '@/lib/chart/panelMenu';
import { copyToClipboard } from '@/lib/clipboard';
import { absoluteShareHref } from '@/lib/shareUrl';
import { ShareButton } from '@/components/ShareButton';
import { encodeRange } from '@/lib/urlState';
import { metricsExplorerCodeHref } from '@/lib/metricsExplorer/panels';
import { useAiSubject } from '@/components/ai/useAiSubject';
import { useAuth } from '@/components/AuthProvider';
import { PanelRenderer, applyVarsToMetric, applyVarsToSpan, expandPromqlVars, type PanelDataOverride } from '@/components/dashboard/PanelRenderer';
import { PanelEditor, defaultConfig } from '@/components/dashboard/PanelEditor';
import { VariableEditor } from '@/components/dashboard/VariableEditor';
import { VariablesBar } from '@/components/dashboard/VariablesBar';
import type { DashboardVariable } from '@/lib/types';
import { api } from '@/lib/api';
import { raceGuard } from '@/lib/raceGuard';
import { usePageZoomRange } from '@/lib/chart/usePageZoomRange';
import { useContentWidth } from '@/lib/useContentWidth';
import { quantizeWidth } from '@/lib/chartStep';
import { effectivePanelStep, estimatePanelPx } from '@/components/dashboard/panelStep';
import type {
  Dashboard, Panel, PanelType, TimeRange,
  MetricPanelConfig, SpanMetricPanelConfig, PromqlPanelConfig,
} from '@/lib/types';
import { timeRangeToNs } from '@/lib/utils';
import { DASHBOARD_RESERVED_PARAMS, isDashboardVariableParam, panelShareUrl } from '@/lib/dashboardUrl';
import { useRefreshHold } from '@/lib/useAutoRefresh';
import { downloadDashboard } from '@/lib/dashboardIO';
import { panelToExploreHref } from '@/pages/explore/panelToExplore';
import { PageShell } from '@/components/coremetry/PageShell';
import { metricPanelSubject } from '@/components/ai/metricPanelAsk';
import { useCopilotEnabled } from '@/components/ai/useCopilotEnabled';
import { ASKABLE_PANEL_TYPES, dashboardPanelAsk } from '@/components/dashboard/panelAsk';
import { dashboardPanelLoc, formatAiParam } from '@/lib/aiSubject';
import { stageMetricPanelAsk, usePublishPageOverlay } from '@/lib/pageContextOverlay';

// Wrapper handles the Suspense requirement of useSearchParams() in App
// Router with static export.
export default function DashboardPage() {
  return <Suspense fallback={<Spinner />}><Inner /></Suspense>;
}

function Inner() {
  const [sp, setSp] = useSearchParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { user } = useAuth();
  const id = sp.get('id') ?? '';
  const startInEdit = sp.get('edit') === '1';

  // v0.9.429 — zoom-yığını deseni paylaşılan usePageZoomRange hook'unda
  // (Dynatrace-style drag-to-zoom: her panel global range'i yazar, tüm
  // paneller yeni pencereyle refetch; çift-tık bir adım geri).
  // v0.10.1435 — zoomBase: zoom'luyken operatörün PENCERESİ; URL'deki aralık
  // brush'ı. Panelin ✨'ı ikisini ayrı gönderir.
  const { range, setRange, handleZoom, handleZoomReset, zoomDepth, zoomBase } = usePageZoomRange(DEFAULT_RANGE_PRESET);
  const [doc, setDoc] = useState<Dashboard | null | undefined>(undefined);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<Dashboard | null>(null);
  const [editingPanel, setEditingPanel] = useState<string | null>(null); // panel id
  // v0.9.780 — etiket girdisinin HAM metni. null = dokunulmadı (değer
  // draft.tags'ten türetilir). Ayrı tutulmasının nedeni edit alanının
  // yanında açıklanıyor: türetilmiş değer virgülü yutuyor.
  const [tagsInput, setTagsInput] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const confirm = useConfirm();
  // Resolved values for the dashboard's Grafana-style variables.
  // URL-persisted so reloads + share-links keep the choice.
  // Empty value for a variable means "all" — the renderer drops any
  // predicate line that references the empty variable so the panel
  // shows aggregates across the relevant universe.
  const [varValues, setVarValues] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {};
    sp.forEach((v, k) => {
      // Reserve route params like id/edit/range for their own slots; everything
      // else becomes a candidate variable value. Cheaper than parsing the
      // dashboard's variable list synchronously here.
      if (!isDashboardVariableParam(k)) return;
      init[k] = v;
    });
    return init;
  });

  // ── Kiosk (v0.9.779) ──────────────────────────────────────────────
  // ADRESTE yaşıyor: paylaşılan link aynı TV görünümünü açar. Yazım
  // react-router üzerinden (aşağıdaki değişken aynası ham replaceState
  // kullandığı için router'ın kendi location'ı bayat kalabiliyor; kiosk'u
  // AppShell okuyacağından yazımın router'a GÖRÜNMESİ şart).
  //
  // v0.10.1430 — AUTO-REFRESH BU SAYFADAN ÇIKTI. v0.9.779'un ?refresh=<sn>
  // + refreshTick sayacı (her panel effect'ine inen bağımlılık) yerini
  // Topbar'daki global RefreshPicker'a bıraktı (lib/autoRefresh.ts): tik
  // useUrlRange'in range kimliğini yenilediği için bundle effect'i ve her
  // panelin kendi fetch effect'i [range] üzerinden zaten yeniden koşuyor —
  // sayaç gereksizleşti. `?refresh=` paramı aynı adla global, eski 30/60/300
  // tam sayıları hâlâ okunuyor. Düzenleme modu saati DURDURUR (aşağıdaki
  // hold): kaydedilmemiş taslağın üstüne veri çekmek gürültü.
  const kiosk = sp.get('kiosk') === '1';
  // v0.10.1998 (dilim 3.4) — tek panel görünümü (?panel=<id>), router üzerinden.
  const soloId = sp.get('panel');
  const setSolo = useCallback((pid: string | null) => {
    setSp(prev => {
      const next = new URLSearchParams(prev);
      if (pid) next.set('panel', pid); else next.delete('panel');
      return next;
    }, { replace: true });
  }, [setSp]);
  const setKiosk = useCallback((on: boolean) => {
    setSp(prev => {
      const next = new URLSearchParams(prev);
      if (on) next.set('kiosk', '1'); else next.delete('kiosk');
      return next;
    }, { replace: true });
  }, [setSp]);
  useRefreshHold(editing, 'edit');

  // Kiosk'tan ESC ile çıkış (PanelMenu / CorePanel klavye sözleşmesinin
  // aynısı) — sidebar gizliyken görünür ✕ tek çıkış olmasın.
  // v0.9.950 (E2/Ö28) — KATMAN. Kiosk EN ALTTAKİ katman olmalı: kiosk
  // içinde açılan bir panel menüsü/modal ilk Esc'i alır, yoksa operatör
  // menüyü kapatmak isterken tam ekrandan da düşerdi.
  useEscLayer(kiosk, () => setKiosk(false));

  // v0.9.857 (UX denetimi K7) — Trace.tsx ile aynı yarış deseni: cleanup'sız
  // manuel fetch adası. Düşük frekanslı ama aynı sınıf; iki pano arasında
  // hızlı geçişte eski pano yenisinin üstüne yazabilirdi.
  useEffect(() => {
    if (!id) return;
    setDoc(undefined);
    const g = raceGuard();
    api.getDashboard(id, g.signal).then(d => {
      if (!g.ok()) return;
      setDoc(d);
      // panels arrives as JSON-encoded string on the wire (json.RawMessage),
      // normalize it to an array for our local state.
      const panels = normalizePanels(d.panels);
      setDraft({ ...d, panels });
      if (startInEdit && user?.role === 'admin') setEditing(true);
      // v0.10.1996 (Faz 3 dilim 3.2) — kişisel "Son görüntülemeniz";
      // en iyi çaba (hata listeyi bozmaz, kullanıcıya gösterilmez).
      void api.recordDashboardView(d.id).then(
        () => qc.invalidateQueries({ queryKey: ['dashboards', 'recent'] }),
        () => undefined,
      );
    }).catch(() => { if (g.ok()) setDoc(null); });
    return g.cancel;
  }, [id]);

  // Mirror the variable values into the URL so the selection survives
  // reloads + is shareable. One param per variable name. Empty values
  // get removed so we don't accumulate dead params on toggle.
  //
  // NOTE: defined BEFORE the early returns below — Rules of Hooks
  // require every hook to be called on every render in the same
  // order. Putting this after the early returns crashed the page on
  // second render (when doc finished loading) because the hook count
  // changed mid-mount.
  //
  // v0.9.779 — rezerve set artık DASHBOARD_RESERVED_PARAMS (testli, tek
  // yer). 'refresh' / 'kiosk' listeye girmeseydi bu effect onları her
  // yazımda silerdi: auto-refresh açılır, bir sonraki render'da sessizce
  // kapanırdı.
  //
  // `sp` de bağımlılıkta: bu ayna ham replaceState yazıyor, yani
  // react-router'ın location'ı onu GÖRMÜYOR. Router üzerinden bir yazım
  // olduğunda (Topbar'ın range seçimi, aşağıdaki refresh/kiosk yazımları)
  // router'ın bayat snapshot'ı değişken paramlarını adresten düşürüyordu;
  // sp değişince ayna tekrar koşup onları geri koyar.
  useEffect(() => {
    const url = new URL(window.location.href);
    for (const key of Array.from(url.searchParams.keys())) {
      if (!DASHBOARD_RESERVED_PARAMS.has(key)) url.searchParams.delete(key);
    }
    for (const [k, v] of Object.entries(varValues)) {
      if (v) url.searchParams.set(k, v);
    }
    window.history.replaceState({}, '', url.toString());
  }, [varValues, sp]);

  // Parsed variable definitions (live from the dashboard doc) drive
  // both the picker bar and what gets substituted into panels. Same
  // Rules-of-Hooks reasoning as the URL mirror — declared before any
  // conditional returns.
  // v0.9.759 — düzenleme modunda değişken tanımları düzenlenebilir;
  // null = dokunulmadı (doc'unki geçerli). Save effVariables'ı yazar.
  const [varDefs, setVarDefs] = useState<DashboardVariable[] | null>(null);
  const variables: DashboardVariable[] = useMemo(() => {
    const raw = doc?.variables;
    if (!raw) return [];
    if (Array.isArray(raw)) return raw as DashboardVariable[];
    try {
      const parsed = JSON.parse(raw as unknown as string);
      return Array.isArray(parsed) ? parsed : [];
    } catch { return []; }
  }, [doc?.variables]);
  const effVariables = varDefs ?? variables;

  // Bundled panel data — v0.5.81 perf nudge. Instead of every
  // metric / spanmetric panel firing its own /api/{metrics,
  // spans}/metric round trip on mount, the dashboard page
  // fires a single POST /api/dashboards/data carrying ALL the
  // panel queries; the server fans them out to CH in parallel
  // goroutines and returns the results keyed by panel id. Each
  // PanelRenderer reads its slot via the dataOverride prop and
  // skips its own fetch. Browser concurrency cap stops mattering;
  // server-side wall-clock = max(panel queries) instead of sum.
  //
  // Falls back gracefully — when bundlePanelData has no entry
  // for a panel (e.g. a panel added mid-edit) the renderer
  // does its own fetch.
  const [bundlePanelData, setBundlePanelData] = useState<Record<string, PanelDataOverride>>({});
  const bundleablePanels = useMemo(() => {
    const list = doc?.panels;
    if (!list) return [];
    const panels = normalizePanels(list);
    return panels.filter(p => p.type === 'metric' || p.type === 'spanmetric');
  }, [doc?.panels]);
  // GRAN-C (v0.8.248) — quantized #content width for the bundle's
  // width-aware auto step. The bundle builds every panel's query before the
  // panel divs are measurable, so auto-step panels estimate their pixels as
  // content-width × grid-span/4 (estimatePanelPx); the per-panel fallback
  // fetch (PanelRenderer) measures the real div instead. Already bucketed,
  // so it only re-fires the bundle on a 200px bucket crossing. The watch key
  // matters: #content doesn't exist during the early-return spinner, so the
  // hook re-measures once the doc lands and the real layout renders.
  const contentW = useContentWidth(doc != null);
  // Re-fire the bundle whenever the panel set, the time range,
  // or any variable value changes. Each of those re-keys the
  // server-side cache anyway, so we want the bundle aligned.
  // v0.10.1430 — `range` bağımlılığı auto-refresh'i de taşır: global tik
  // useUrlRange'in range kimliğini yeniler, timeRangeToNs aşağıda yeniden
  // hesaplanır, pencere ilerler (eski refreshTick sayacı gereksizleşti).
  useEffect(() => {
    if (bundleablePanels.length === 0) {
      setBundlePanelData({});
      return;
    }
    // Skip when contentW is provably stale: useContentWidth's effect runs
    // just above this one in the same flush (hook declaration order), so a
    // live-DOM mismatch means the corrected bucket is already scheduled and
    // this effect re-fires immediately — skipping avoids a throwaway POST
    // with wrong-width steps on the doc-load commit (where the bundle would
    // otherwise fire against the pre-#content fallback width).
    const live = document.getElementById('content');
    if (live && quantizeWidth(live.clientWidth || 1200) !== contentW) return;
    let cancelled = false;
    const { from, to } = timeRangeToNs(range);
    // Per-panel effective step: operator-pinned cfg.step passes through;
    // auto (step absent — every pre-GRAN-C dashboard) resolves against the
    // panel's estimated width. The backend min-step clamp (v0.8.243) floors
    // fine requests at the metric's export interval.
    const rangeSec = (to - from) / 1e9;
    const stepFor = (cfgStep: number | undefined, p: Panel) =>
      effectivePanelStep(cfgStep, rangeSec, estimatePanelPx(contentW, panelSpan(p) / 3)) ?? undefined;
    const requests = bundleablePanels.map(p => {
      if (p.type === 'metric') {
        const cfg = applyVarsToMetric(p.config as MetricPanelConfig, varValues);
        return {
          id: p.id, type: 'metric' as const,
          name: cfg.metricName, service: cfg.service,
          agg: cfg.agg,
          groupBy: cfg.groupBy ? cfg.groupBy.split(',').map(s => s.trim()).filter(Boolean) : undefined,
          step: stepFor(cfg.step, p),
          filters: cfg.filters,
        };
      }
      const cfg = applyVarsToSpan(p.config as SpanMetricPanelConfig, varValues);
      return {
        id: p.id, type: 'spanMetric' as const,
        agg: cfg.agg, field: cfg.field,
        groupBy: cfg.groupBy ? cfg.groupBy.split(',').map(s => s.trim()).filter(Boolean) : undefined,
        filters: cfg.filters, dsl: cfg.dsl,
        step: stepFor(cfg.step, p),
      };
    });
    api.dashboardData({ from, to, requests })
      .then(out => { if (!cancelled) setBundlePanelData(out as Record<string, PanelDataOverride>); })
      .catch(() => { if (!cancelled) setBundlePanelData({}); });
    return () => { cancelled = true; };
  }, [bundleablePanels, range, varValues, contentW]);

  if (!id) return <Empty icon="◫" title="No dashboard selected" />;
  if (doc === undefined) return <Spinner />;
  if (doc === null) return <Empty icon="⚠" title="Dashboard not found" />;
  if (!draft) return <Spinner />;

  const isAdmin = user?.role === 'admin' || user?.role === 'editor';
  const panels: Panel[] = draft.panels ?? [];

  const updatePanel = (panel: Panel) => {
    setDraft({ ...draft, panels: panels.map(p => p.id === panel.id ? panel : p) });
  };
  const addPanel = (type: PanelType) => {
    const p: Panel = {
      id: rid(), type,
      title: type === 'row' ? 'New row' : `New ${type}`,
      // Row markers always span the full grid; everything else defaults
      // to half-width and the user can resize via the editor.
      width: type === 'row' ? 4 : 2,
      config: defaultConfig(type),
    };
    setDraft({ ...draft, panels: [...panels, p] });
    setEditingPanel(p.id);
  };
  const deletePanel = (id: string) => {
    setDraft({ ...draft, panels: panels.filter(p => p.id !== id) });
    setEditingPanel(null);
  };
  // v0.9.773 — duplicate a panel in place (Grafana's "Copy" on the panel
  // menu). The copy lands immediately AFTER its source so the operator sees
  // it without hunting the bottom of the board.
  //
  // Two details that would be bugs if missed:
  //   • structuredClone, not a spread. Panel.config carries nested objects
  //     (stat/gauge thresholds, the metric/span sub-config); a shallow copy
  //     would leave the two panels sharing one thresholds array, so editing
  //     the copy would silently rewrite the original.
  //   • enter edit mode. `draft` is only persisted by Save, which only
  //     exists in edit mode — duplicating from the view mode would look
  //     like it worked and vanish on reload. Flipping to editing makes the
  //     unsaved state visible and saveable.
  const duplicatePanel = (pid: string) => {
    const idx = panels.findIndex(p => p.id === pid);
    if (idx < 0) return;
    const copy: Panel = {
      ...structuredClone(panels[idx]),
      id: rid(),
      title: `${panels[idx].title} (kopya)`,
    };
    const next = [...panels];
    next.splice(idx + 1, 0, copy);
    if (!editing) setEditing(true);
    setDraft({ ...draft, panels: next });
  };
  // Edit requested from the panel menu — reachable in view mode, so it has
  // to open edit mode first (the editor writes into `draft`).
  const requestEditPanel = (pid: string) => {
    if (!editing) setEditing(true);
    setEditingPanel(pid);
  };
  // Reorder by drop: move srcId to immediately before targetId.
  // No-op when src === target. Used by the drag-and-drop
  // handlers wired below the panel render block.
  const movePanel = (srcId: string, targetId: string) => {
    if (srcId === targetId) return;
    const srcIdx = panels.findIndex(p => p.id === srcId);
    const tgtIdx = panels.findIndex(p => p.id === targetId);
    if (srcIdx < 0 || tgtIdx < 0) return;
    const next = [...panels];
    const [moved] = next.splice(srcIdx, 1);
    // After splicing out src, target index might shift left by 1
    // if src came before it.
    const insertAt = srcIdx < tgtIdx ? tgtIdx - 1 : tgtIdx;
    next.splice(insertAt, 0, moved);
    setDraft({ ...draft, panels: next });
  };
  const save = async () => {
    setBusy(true);
    try {
      const updated = await api.updateDashboard(id, {
        name: draft.name, description: draft.description, panels: draft.panels,
        // v0.9.759 — değişken tanımları da kaydedilir (backend merge
        // semantiği: alan yoksa korunur, açık boş liste boşaltır).
        variables: effVariables,
        // v0.9.780 — etiketler. Aynı merge semantiği: burada AÇIKÇA
        // gönderiliyor, yani operatör hepsini silerse ([]) boş kalır.
        tags: draft.tags ?? [],
      });
      setDoc({ ...updated, panels: normalizePanels(updated.panels) });
      setVarDefs(null);
      setTagsInput(null);
      // v0.9.780 — /dashboards listesi ad/açıklama/etiket gösteriyor ve
      // 60s staleTime ile duruyor; geçersiz kılmazsak operatör bir
      // etiketi kaydedip listeye dönünce eski hâlini görürdü. ['dashboards']
      // ön-eki hem 'list' hem PinToDashboardModal'ın anahtarını kapsıyor.
      void qc.invalidateQueries({ queryKey: ['dashboards'] });
      setEditing(false);
    } finally {
      setBusy(false);
    }
  };
  const cancel = () => {
    setDraft({ ...doc, panels: normalizePanels(doc.panels) });
    setVarDefs(null); // v0.9.759 — düzenlenen değişkenler de geri alınır
    setTagsInput(null); // v0.9.780 — etiket girdisi de taslakla birlikte
    setEditing(false);
    setEditingPanel(null);
  };
  const removeDashboard = async () => {
    if (!await confirm({
      title: 'Pano silinsin mi?',
      body: <><b>{doc?.name || id}</b> panosu, panelleri ve değişkenleriyle birlikte
        kalıcı olarak silinecek. Kaybetmek istemiyorsan önce <b>↓ Export JSON</b> al.</>,
      confirmLabel: 'Panoyu sil',
      danger: true,
    })) return;
    await api.deleteDashboard(id);
    navigate('/dashboards');
  };

  // v0.6.50 — export the dashboard to a JSON file. Read-only, so
  // every role can use it (a viewer exporting a board to share is
  // fine). Builds the portable subset from the already-loaded
  // panels + variables via serializeDashboard; triggers a client-
  // side download with no backend round-trip.
  const exportDashboard = () => {
    if (!draft) return;
    downloadDashboard({ ...draft, panels, variables: effVariables });
  };

  const editingPanelObj = editingPanel ? panels.find(p => p.id === editingPanel) : null;

  return (
    <>
      <Topbar title={draft.name} range={range} onRangeChange={setRange} />
      {/* Kiosk çıkışı — sidebar gizliyken tek görünür çıkış. ESC de
          çalışır (yukarıdaki dinleyici), ama görünmeyen bir kısayol tek
          çıkış olamaz: bir TV'de kimse ESC'i bilmiyor.
          v0.10.928 — secondary dolgusuz; paneller üstünde yüzdüğü için
          is-overlay opak zemin verir (satır-içi background hover'ı öldürürdü). */}
      {kiosk && (
        <Button variant="secondary" size="sm" onClick={() => setKiosk(false)}
          className="is-overlay"
          title="Kiosk modundan çık (ESC)"
          style={{ position: 'fixed', top: 10, right: 12, zIndex: 'var(--z-fab)' }}>
          Kiosk'tan çık ✕
        </Button>
      )}
      <PageShell>
        <div className="controls" style={{ marginBottom: 14 }}>
          {editing ? (
            <>
              <input value={draft.name} placeholder="Dashboard name" aria-label="Dashboard name"
                onChange={e => setDraft({ ...draft, name: e.target.value })}
                style={{ width: 220 }} />
              <input value={draft.description} placeholder="Description" aria-label="Dashboard description"
                onChange={e => setDraft({ ...draft, description: e.target.value })}
                style={{ width: 320 }} />
              {/* v0.9.780 — etiketler. Virgülle ayrılmış düz metin:
                  bir rozet-editörü kurmak yerine yazmayı serbest
                  bırakıyoruz; etiket kümesi kapalı değil ve operatör
                  zaten kendi sözlüğünü kuruyor. Ayrıştırma kaydederken
                  değil YAZARKEN yapılıyor ki draft tek doğru olsun. */}
              <input value={tagsInput ?? (draft.tags ?? []).join(', ')}
                placeholder="Etiketler (virgülle)" aria-label="Dashboard tags"
                title="Virgülle ayrılmış etiketler — panolar listesinde görünür ve aranabilir"
                onChange={e => {
                  // HAM metin ayrı tutuluyor: gösterilen değeri
                  // ayrıştırılmış diziden türetseydik, yazılan virgül
                  // (ve ardındaki boşluk) filtreden düşer ve operatör
                  // ikinci etiketi hiç yazamazdı.
                  setTagsInput(e.target.value);
                  setDraft({
                    ...draft,
                    tags: e.target.value.split(',').map(t => t.trim()).filter(Boolean),
                  });
                }}
                style={{ width: 220 }} />
              <AddPanelMenu onAdd={addPanel} />
              <span style={{ marginLeft: 'auto' }} />
              <Button variant="secondary" onClick={cancel}>Cancel</Button>
              <Button variant="primary" onClick={save} loading={busy}>Save</Button>
            </>
          ) : (
            <>
              {draft.description && (
                <span style={{ color: 'var(--text2)', fontSize: 12 }}>{draft.description}</span>
              )}
              <span style={{ marginLeft: 'auto' }} />
              {/* v0.9.779 — TV modu bu sayfanın kendi şeridinde; auto-refresh
                  v0.10.1430'da Topbar'ın global RefreshPicker'ına taşındı. */}
              <Button variant="secondary" size="sm" onClick={() => setKiosk(true)}
                title="TV/kiosk modu — sol menüyü gizler, panoyu tam genişliğe açar. ESC ile çıkılır.">
                ⛶ TV
              </Button>
              {/* v0.10.1998 (dilim 3.4) — pano bağlantısı: değişkenler + aralık
                  (mutlak zamana sabitlenir) + kiosk/tek panel aynı URL'de. */}
              <ShareButton size="sm" label="Paylaş" copiedLabel="Bağlantı kopyalandı"
                title="Pano bağlantısını kopyala — zaman penceresi mutlak zamana sabitlenir" />
              {/* Export is read-only → available to every role so a
                  viewer can grab a board to share / version. */}
              <Button variant="secondary" onClick={exportDashboard}
                title="Download this dashboard as a portable JSON file">↓ Export JSON</Button>
              {isAdmin && (
                <>
                  {/* v0.9.1006 (M4/O6 + K5) — iki değişiklik: dolu
                      kırmızı Delete, dolu mavi Edit'in yanında ikinci bir
                      ağırlıktı (`ghost-danger`a indi) ve YIKICI OLAN
                      SOLDAYDI, yani kas hafızasının birincil eylemi
                      beklediği yerde. Sıra da düzeltildi. */}
                  <Button variant="primary" onClick={() => setEditing(true)}>Edit</Button>
                  <Button variant="ghost-danger" onClick={removeDashboard}>Delete</Button>
                </>
              )}
            </>
          )}
        </div>

        {editing && (
          <VariableEditor value={effVariables} onChange={setVarDefs} />
        )}
        {/* Grafana-style variables bar — only renders when the
            dashboard declares variables. Each variable's selection
            persists in the URL as ?<name>=<value> and the renderer
            substitutes ${name} into panel DSLs / service / groupBy. */}
        {!editing && effVariables.length > 0 && (
          <VariablesBar
            variables={effVariables}
            values={varValues}
            onChange={(k, v) => setVarValues(prev => ({ ...prev, [k]: v }))}
          />
        )}

        {soloId && !editing ? (
          <SoloPanel panel={panels.find(p => p.id === soloId && p.type !== 'row')} onBack={() => setSolo(null)}>
            <DashboardGrid
              panels={panels.filter(p => p.id === soloId).map(p => ({ ...p, width: 4, span: 12, height: 'l' }))}
              range={range}
              vars={varValues}
              editing={false}
              canEdit={isAdmin}
              onEditPanel={setEditingPanel}
              onEditRequest={requestEditPanel}
              onDuplicatePanel={duplicatePanel}
              onDeletePanel={deletePanel}
              onMovePanel={movePanel}
              onSolo={setSolo}
              onZoom={handleZoom}
              onZoomReset={handleZoomReset}
              dashboardId={id}
              zoomBase={zoomDepth > 0 ? zoomBase : null}
              bundlePanelData={{}} />
          </SoloPanel>
        ) : panels.length === 0 ? (
          <Empty icon="◫" title="No panels yet">
            {editing ? 'Use "+ Add panel" above to start building.'
                     : 'Click Edit to add panels.'}
          </Empty>
        ) : (
          <DashboardGrid
            panels={panels}
            range={range}
            vars={varValues}
            editing={editing}
            canEdit={isAdmin}
            onEditPanel={setEditingPanel}
            onEditRequest={requestEditPanel}
            onDuplicatePanel={duplicatePanel}
            onDeletePanel={deletePanel}
            onMovePanel={movePanel}
            onSolo={setSolo}
            onZoom={handleZoom}
            onZoomReset={handleZoomReset}
            dashboardId={id}
            zoomBase={zoomDepth > 0 ? zoomBase : null}
            bundlePanelData={bundlePanelData} />
        )}

        {editingPanelObj && (
          <PanelEditor panel={editingPanelObj}
            onChange={updatePanel}
            onClose={() => setEditingPanel(null)}
            onDelete={() => deletePanel(editingPanelObj.id)} />
        )}
      </PageShell>
    </>
  );
}

// RefreshControl (v0.9.779) — v0.10.1430'da SİLİNDİ: auto-refresh global
// Topbar RefreshPicker'ında (components/RefreshPicker.tsx), şim yok.

// usePanelActions — v0.10.1998 (Faz 3 dilim 3.4; eski PanelMenu v0.9.773).
//
// Panel başına TEK eylem listesi; nerede çizileceğine panelMenuHost karar
// verir (grafik varsa CorePanel'in ⋯'ü, yoksa çerçevenin PanelFrameMenu'sü).
// Sıra (mockup görünüm 5): sor · Düzenle · Tek başına aç · Explorer'da aç ·
// Çoğalt · Bağlantıyı kopyala. Silme menüde YOK (v0.9.773 kararı: düzenleme
// kipindeki × zaten bilinçli giriş ister).
function usePanelActions({ panel, vars, range, canEdit, onDuplicate, onEdit, onSolo, dashboardId, zoomBase, selected, editing }: {
  panel: Panel;
  vars?: Record<string, string>;
  range: TimeRange;
  canEdit: boolean;
  onDuplicate: (id: string) => void;
  onEdit: (id: string) => void;
  onSolo: (id: string) => void;
  dashboardId: string;
  zoomBase: TimeRange | null;
  selected: readonly string[];
  editing: boolean;
}): PanelMenuAction[] {
  const navigate = useNavigate();
  const [, setAi] = useAiSubject();
  const [copied, setCopied] = useState(false);
  // A panel's own range override wins — Explore should open the window the
  // operator is actually looking at, not the dashboard's.
  const win = panel.rangeOverride ?? range;
  const exploreHref = panelToExploreHref(panel, vars, win)
    ?? (panel.type === 'promql'
      ? metricsExplorerCodeHref(expandPromqlVars((panel.config as PromqlPanelConfig).query ?? '', vars), encodeRange(win), (panel.config as PromqlPanelConfig).source)
      : null);

  // v0.10.1435 — sor yalnız zaman serisi panelinde, düzenleme dışında, copilot
  // açıkken ve kimlikler konum kodeğine sığıyorsa.
  const copilotOn = useCopilotEnabled() === true;
  const loc = dashboardPanelLoc(dashboardId, panel.id);
  const askable = copilotOn && !editing && !!loc && ASKABLE_PANEL_TYPES.has(panel.type);
  // Pencere: panelin kendi aralığı (override → brush yok) ya da zoom tabanı;
  // zoom'luyken URL aralığı = brush. timeRangeToNs memo içinde (v0.5.184 kuralı).
  const askSubject = useMemo(() => {
    if (!askable || !loc) return null;
    if (panel.rangeOverride) return metricPanelSubject(loc, nsWin(panel.rangeOverride), null, selected);
    if (zoomBase) return metricPanelSubject(loc, nsWin(zoomBase), nsWin(range), selected);
    return metricPanelSubject(loc, nsWin(range), null, selected);
  }, [askable, loc, panel.rangeOverride, zoomBase, range, selected]);

  const out: PanelMenuAction[] = [];
  if (askSubject) out.push({ key: 'ask', label: "CoSRE'ye sor", onClick: () => {
    const ask = dashboardPanelAsk(panel, vars ?? {});
    if (ask) stageMetricPanelAsk(formatAiParam(askSubject), ask);
    setAi(askSubject);
  } });
  if (canEdit) out.push({ key: 'edit', label: 'Düzenle', onClick: () => onEdit(panel.id) });
  out.push({ key: 'solo', label: 'Tek başına aç', onClick: () => onSolo(panel.id) });
  if (exploreHref) out.push({ key: 'explore', label: "Explorer'da aç", onClick: () => navigate(exploreHref) });
  if (canEdit) out.push({ key: 'dup', label: 'Çoğalt', onClick: () => onDuplicate(panel.id) });
  out.push({
    key: 'share', label: copied ? 'Kopyalandı' : 'Bağlantıyı kopyala', keepOpen: true,
    onClick: () => {
      void copyToClipboard(absoluteShareHref(panelShareUrl(window.location.href, panel.id), Date.now())).then(ok => {
        if (!ok) return;
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      });
    },
  });
  return out;
}

function AddPanelMenu({ onAdd }: { onAdd: (t: PanelType) => void }) {
  const [open, setOpen] = useState(false);
  const labels: Record<PanelType, string> = {
    row: 'Row (section header)',
    metric: 'Metric (line)',
    spanmetric: 'Span aggregation (line)',
    stat: 'Stat (single value)',
    gauge: 'Gauge',
    heatmap: 'Heatmap (latency density)',
    promql: 'PromQL / MetricsQL query',
    topn: 'Top-N bar',
    markdown: 'Markdown / notes',
  };
  return (
    <div style={{ position: 'relative' }}>
      <Button variant="secondary" onClick={() => setOpen(o => !o)}>+ Add panel</Button>
      {open && (
        <div style={{
          position: 'absolute', top: '100%', left: 0, marginTop: 4,
          background: 'var(--bg1)', border: '1px solid var(--border)',
          borderRadius: 6, padding: 4, zIndex: 'var(--z-dropdown)', minWidth: 180,
          boxShadow: 'var(--shadow-pop)',
        }}>
          {/* v0.9.781 — this list is a HAND-MAINTAINED array behind an
              `as PanelType[]` cast, so TypeScript does NOT check it against
              the union: gauge and heatmap both shipped renderable but
              unreachable because only the `labels` record above was updated.
              Any new type goes in BOTH places. */}
          {(['row', 'metric', 'spanmetric', 'promql', 'topn', 'stat', 'gauge', 'heatmap', 'markdown'] as PanelType[]).map(t => (
            <MenuItem key={t} onClick={() => { onAdd(t); setOpen(false); }}>
              {labels[t]}
            </MenuItem>
          ))}
        </div>
      )}
    </div>
  );
}

function rid(): string {
  return Math.random().toString(36).slice(2, 10);
}

// Backend returns panels as a JSON-encoded string (json.RawMessage). Some
// endpoints (PUT) round-trip it as an array. Normalize both to Panel[].
function normalizePanels(raw: unknown): Panel[] {
  if (Array.isArray(raw)) return raw as Panel[];
  if (typeof raw === 'string') {
    try { const parsed = JSON.parse(raw); return Array.isArray(parsed) ? parsed : []; }
    catch { return []; }
  }
  return [];
}

// Grafana-style row layout: panels of type 'row' act as collapsible
// section headers. All non-row panels following a row marker (until
// the next row) belong to that row's grid. Panels before any row
// marker form an implicit "default" row at the top.
//
// Per-row collapse state is local component state, keyed by panel id —
// not persisted across reloads (matches Grafana's default behaviour;
// add a localStorage layer if users start asking for it).
function DashboardGrid({
  panels, range, vars, editing, canEdit, onEditPanel, onEditRequest, onDuplicatePanel,
  onDeletePanel, onMovePanel, onSolo, onZoom, onZoomReset, dashboardId, zoomBase,
  bundlePanelData,
}: {
  panels: Panel[];
  range: TimeRange;
  vars?: Record<string, string>;
  editing: boolean;
  // Whether this operator may mutate the board (admin/editor). A viewer
  // still gets the menu — "open in Explore" is a read — but not the
  // mutating half of it.
  canEdit: boolean;
  onEditPanel: (id: string) => void;
  // v0.9.773 — edit asked for from the panel menu, which is reachable in
  // VIEW mode; the parent flips into editing first. onEditPanel stays the
  // already-editing path (the inline Edit button).
  onEditRequest: (id: string) => void;
  onDuplicatePanel: (id: string) => void;
  onDeletePanel: (id: string) => void;
  onMovePanel: (srcId: string, targetId: string) => void;
  // v0.10.1998 — "Tek başına aç" (?panel=<id>).
  onSolo: (id: string) => void;
  // onZoom propagates a chart's drag-to-zoom selection up to
  // the dashboard so the rest of the panels re-fetch for the
  // new range. Receives unix-seconds bounds, parent owns the
  // TimeRange state.
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  // Grafana-parite M1 — çift-tık: dashboard zoom geri-yığınını pop eder.
  onZoomReset?: () => void;
  // Cursor-sync key passed to every chart panel — every chart on
  // the dashboard hovers in lockstep so the operator reads 8
  // panels as one view.
  dashboardId: string;
  // v0.10.1435 — zoom'luyken ilk zoom'dan önceki görünüm (✨ penceresi); yoksa null.
  zoomBase: TimeRange | null;
  // Pre-fetched panel data keyed by panel id (v0.5.81 bundle).
  // Each metric / spanmetric PanelRenderer reads its slot and
  // skips its own fetch. Missing entries fall through to the
  // panel's own fetch path.
  bundlePanelData: Record<string, PanelDataOverride>;
}) {
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  // ID of the panel currently being dragged-over so we can render a
  // visual drop indicator. Drag-source id rides on dataTransfer, no
  // need to mirror it into state.
  const [dropTarget, setDropTarget] = useState<string | null>(null);
  // v0.10.1435 — panel başına lejantta seçili seriler (CorePanel onFocusChange)
  // + son değişen panelin seçimi sohbet bağlamına (pageContextOverlay); zoom'luyken
  // URL aralığı brush olarak yayınlanır.
  const [selectedByPanel, setSelectedByPanel] = useState<Record<string, string[]>>({});
  const [lastSelected, setLastSelected] = useState<string[]>([]);
  const onPanelFocus = useCallback((panelId: string, names: string[]) => {
    setSelectedByPanel(m => ((m[panelId] ?? []).join('\u001f') === names.join('\u001f') ? m : { ...m, [panelId]: names }));
    if (names.length) setLastSelected(names);
  }, []);
  const brushOverlay = zoomBase && range.preset === 'custom' && range.fromMs && range.toMs
    ? { fromMs: range.fromMs, toMs: range.toMs } : undefined;
  usePublishPageOverlay({ brush: brushOverlay, selectedSeries: lastSelected });

  // Bucket panels into row groups.
  type RowGroup = { rowPanel: Panel | null; key: string; panels: Panel[] };
  const groups: RowGroup[] = [];
  let cur: RowGroup = { rowPanel: null, key: '__head', panels: [] };
  groups.push(cur);
  for (const p of panels) {
    if (p.type === 'row') {
      cur = { rowPanel: p, key: p.id, panels: [] };
      groups.push(cur);
    } else {
      cur.panels.push(p);
    }
  }
  // Drop the implicit head if it ended up empty (i.e. the dashboard
  // starts with an explicit row).
  const visible = groups.filter(g => g.rowPanel || g.panels.length > 0);

  return (
    <div>
      {visible.map(g => {
        const isCollapsed = g.rowPanel ? collapsed.has(g.rowPanel.id) : false;
        return (
          <div key={g.key} style={{ marginBottom: 14 }}>
            {g.rowPanel && (
              <div className="dash-row-header"
                   onClick={() => {
                     if (!g.rowPanel) return;
                     const next = new Set(collapsed);
                     if (next.has(g.rowPanel.id)) next.delete(g.rowPanel.id); else next.add(g.rowPanel.id);
                     setCollapsed(next);
                   }}
                   onKeyDown={e => {
                     // Keyboard parity with the click toggle — Enter/Space
                     // collapses/expands the row the same way a click does.
                     if (!g.rowPanel) return;
                     if (e.key === 'Enter' || e.key === ' ') {
                       e.preventDefault();
                       const next = new Set(collapsed);
                       if (next.has(g.rowPanel.id)) next.delete(g.rowPanel.id); else next.add(g.rowPanel.id);
                       setCollapsed(next);
                     }
                   }}
                   // eslint-disable-next-line ui/no-raw-element -- satır başlığı düzenleme kipinde Edit/Delete düğmelerini sarıyor; düğme içinde düğme olamaz
                   role="button"
                   tabIndex={0}
                   aria-expanded={!isCollapsed}>
                <span className="dash-row-toggle">{isCollapsed ? '▶' : '▼'}</span>
                <span className="dash-row-title">{g.rowPanel.title || 'Row'}</span>
                <span className="dash-row-count">
                  {g.panels.length} panel{g.panels.length === 1 ? '' : 's'}
                </span>
                {editing && (
                  <span className="row gap-1" style={{ marginLeft: 8 }} onClick={e => e.stopPropagation()}>
                    <Button variant="secondary" size="sm"
                      onClick={() => g.rowPanel && onEditPanel(g.rowPanel.id)}>Edit</Button>
                    <IconButton variant="danger" size="sm" tooltip="Delete row"
                      aria-label="Delete row"
                      onClick={() => g.rowPanel && onDeletePanel(g.rowPanel.id)} icon="×" />
                  </span>
                )}
              </div>
            )}
            {!isCollapsed && g.panels.length > 0 && (
              // v0.10.1999 (dilim 3.5) — 12 kolon akış ızgarası (grid.ts).
              <div className={DASH_GRID_CLASS} style={{ marginTop: g.rowPanel ? 8 : 0 }}>
                {g.panels.map(p => (
                  <PanelCard key={p.id} p={p} range={range} vars={vars} editing={editing} canEdit={canEdit}
                    dropTarget={dropTarget === p.id}
                    onDragState={setDropTarget}
                    onMovePanel={onMovePanel}
                    onEditPanel={onEditPanel} onEditRequest={onEditRequest}
                    onDuplicatePanel={onDuplicatePanel} onDeletePanel={onDeletePanel}
                    onSolo={onSolo}
                    onZoom={onZoom} onZoomReset={onZoomReset}
                    dashboardId={dashboardId} zoomBase={zoomBase}
                    selected={selectedByPanel[p.id] ?? NO_SELECTION}
                    onPanelFocus={onPanelFocus}
                    dataOverride={editing ? undefined : bundlePanelData[p.id]} />
                ))}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

// PanelCard — v0.10.1998 (Faz 3 dilim 3.4). Eski DashboardGrid satır içi
// kartı + üç değişiklik:
//   • TEK ⋯: eylemler panelMenuHost bağlamıyla aşağı iner; grafik çizilince
//     CorePanel'in menüsünde, çizilmezken çerçevenin PanelFrameMenu'sünde.
//   • LazyMount: görünmeyen panel MOUNT EDİLMEZ, yani kendi isteğini atmaz
//     (bundle'daki paneller zaten tek istekle gelir; promql / stat / gauge /
//     top-N / heatmap ekrana yaklaşınca çeker). Kiosk ve düzenleme kipinde de
//     aynı: TV'de görünmeyen panel istek atmamalı.
//   • "Tek başına aç": ?panel=<id> (aşağıdaki SoloPanel).
function PanelCard({
  p, range, vars, editing, canEdit, dropTarget, onDragState, onMovePanel, onEditPanel, onEditRequest,
  onDuplicatePanel, onDeletePanel, onSolo, onZoom, onZoomReset, dashboardId, zoomBase, selected, onPanelFocus, dataOverride,
}: {
  p: Panel;
  range: TimeRange;
  vars?: Record<string, string>;
  editing: boolean;
  canEdit: boolean;
  dropTarget: boolean;
  onDragState: (id: string | null) => void;
  onMovePanel: (srcId: string, targetId: string) => void;
  onEditPanel: (id: string) => void;
  onEditRequest: (id: string) => void;
  onDuplicatePanel: (id: string) => void;
  onDeletePanel: (id: string) => void;
  onSolo: (id: string) => void;
  onZoom?: (fromUnixSec: number, toUnixSec: number) => void;
  onZoomReset?: () => void;
  dashboardId: string;
  zoomBase: TimeRange | null;
  selected: readonly string[];
  onPanelFocus: (panelId: string, names: string[]) => void;
  dataOverride: PanelDataOverride;
}) {
  const actions = usePanelActions({
    panel: p, vars, range, canEdit, onDuplicate: onDuplicatePanel, onEdit: onEditRequest, onSolo,
    dashboardId, zoomBase, selected, editing,
  });
  const frame = usePanelMenuFrame();
  return (
    <div
      className={`dash-panel ${panelSpanClass(p)}`}
      data-panel-id={p.id}
      draggable={editing}
      onDragStart={e => {
        if (!editing) return;
        e.dataTransfer.setData('text/panel-id', p.id);
        e.dataTransfer.effectAllowed = 'move';
      }}
      onDragOver={e => {
        if (!editing) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        if (!dropTarget) onDragState(p.id);
      }}
      onDragLeave={() => {
        if (dropTarget) onDragState(null);
      }}
      onDrop={e => {
        if (!editing) return;
        e.preventDefault();
        const srcId = e.dataTransfer.getData('text/panel-id');
        onDragState(null);
        if (srcId && srcId !== p.id) onMovePanel(srcId, p.id);
      }}
      onDragEnd={() => onDragState(null)}
      style={{
        background: 'var(--bg2)',
        border: dropTarget
          ? '1px dashed var(--accent)'
          : '1px solid var(--border)',
        borderRadius: 6, padding: 10,
        position: 'relative',
        cursor: editing ? 'grab' : 'default',
        opacity: 1,
        transition: 'border-color 0.1s',
      }}>
      <div className="row-between" style={{
        marginBottom: 6, fontSize: 12, color: 'var(--text2)',
      }}>
        <span className="row gap-2" style={{ minWidth: 0 }}>
          {editing && (
            <span title="Drag to reorder"
              style={{
                color: 'var(--text3)', fontSize: 14,
                cursor: 'grab', userSelect: 'none',
              }}>⋮⋮</span>
          )}
          <span style={{
            fontWeight: 600, color: 'var(--text)',
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
          }}>{p.title}</span>
          {/* v0.9.773 — operator note. A dashboard is scanned,
              so the text lives in a tooltip, not in the card
              body where it would push the chart down. */}
          {p.description && (
            <i className="dash-panel-info" title={p.description}
              aria-label={`Panel description: ${p.description}`}>i</i>
          )}
          {/* v0.6.20 — range-override indicator. */}
          {p.rangeOverride?.preset && (
            <span className="badge b-info mono"
              title="This panel uses a fixed time range — overrides the dashboard Topbar">
              ↻ {p.rangeOverride.preset}
            </span>
          )}
        </span>
        <span className="row gap-1">
          {editing && (
            <>
              <Button variant="secondary" size="sm"
                onClick={() => onEditPanel(p.id)}>Edit</Button>
              <IconButton variant="danger" size="sm" tooltip="Delete panel"
                aria-label="Delete panel"
                onClick={() => onDeletePanel(p.id)} icon="×" />
            </>
          )}
          {!frame.hosted && <PanelFrameMenu actions={actions} label={`${p.title || 'Panel'} menüsü`} />}
        </span>
      </div>
      <PanelMenuProvider actions={actions} claim={frame.claim}>
        <LazyMount minHeight={p.type === 'markdown' ? 40 : panelChartHeight(p.height)}>
          <PanelRenderer panel={p} range={range} vars={vars}
            syncKey={`dashboard:${dashboardId}`}
            onFocusChange={names => onPanelFocus(p.id, names)}
            onZoom={onZoom}
            onZoomReset={onZoomReset}
            // v0.10.146 — düzenlerken override YOK (çağıran undefined geçer):
            // taslak konfig her panelde kendi fetch'iyle önizlenir.
            dataOverride={dataOverride} />
        </LazyMount>
      </PanelMenuProvider>
    </div>
  );
}

// SoloPanel — v0.10.1998 (Faz 3 dilim 3.4; audit §5.3 "Tam ekran: tek panel
// URL'i ?panel=<id>"). Panoyu tek panele indirir: tam genişlik, uzun gövde.
// Değişkenler, aralık ve kiosk aynı URL'de kalır; "Panoya dön" ?panel'i siler.
function SoloPanel({ panel, children, onBack }: { panel: Panel | undefined; children: ReactNode; onBack: () => void }) {
  return (
    <div>
      <div className="controls" style={{ marginBottom: 10 }}>
        <Button variant="secondary" size="sm" onClick={onBack}>← Panoya dön</Button>
        <b>{panel?.title ?? 'Panel bulunamadı'}</b>
      </div>
      {panel ? children : <Empty icon="◫" title="Panel bulunamadı">Bağlantıdaki panel bu panoda yok; panoya dönün.</Empty>}
    </div>
  );
}

// v0.10.1435 — boş seçim (kararlı kimlik: PanelMenu memo'su her render'da kırılmasın).
const NO_SELECTION: readonly string[] = [];

// nsWin — v0.10.1435: TimeRange → ns penceresi (yalnız memo / tık anında çağrılır).
function nsWin(r: TimeRange): { fromNs: number; toNs: number } {
  const { from, to } = timeRangeToNs(r);
  return { fromNs: from, toNs: to };
}
