import { Suspense, useEffect, useMemo, useRef, useState } from 'react';
import { LOG_SPIKE_QUERY_MAX, type AISubject } from '@/lib/aiSubject';
import { DEFAULT_RANGE_PRESET } from '@/lib/useUrlRange';
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { SavedViewsBar } from '@/components/SavedViewsBar';
import { Spinner } from '@/components/Spinner';
import { Combobox } from '@/components/Combobox';
import { LogTable, DEFAULT_LOG_COLUMNS } from '@/components/LogTable';
import type { DataTableStateProps } from '@/components/ui/DataTable';
import { CorrelationContextDrawer } from '@/components/CorrelationContextDrawer';
import { LogContextModal } from '@/components/LogContextModal';
import {
  LogsHistogram, parseBreakdown, histogramFeedsChips, type LogsBreakdown,
} from '@/components/LogsHistogram';
import { LogFieldsPanel } from '@/components/LogFieldsPanel';
import { LogPatternsPanel } from '@/components/LogPatternsPanel';
import { Button } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { SegmentedControl } from '@/components/ui/SegmentedControl';
import { IconSparkles } from '@/components/icons';
import { RenderedMarkdown } from '@/components/Markdown';
import { Pager } from '@/components/Pager';
import { buildKibanaURL, buildKQLFromFilter } from '@/lib/kibanaLink';
import { logsUseTimeRange } from '@/lib/logsTraceWindow'; // v0.10.690
import type { KibanaSettings } from '@/lib/types';
import { useLogs, useLogsMyTeam } from '@/lib/queries';
import { usePageZoomRange } from '@/lib/chart/usePageZoomRange';
import { useUrlEnv } from '@/lib/useUrlEnv';
import { useTablePrefs } from '@/lib/queries/prefs';
import { parseColsParam } from '@/lib/columnModel';
import { getRaw, setRaw } from '@/lib/storage';
import { useTableNav } from '@/lib/useTableNav';
import { api } from '@/lib/api';
import { logsBucketSec } from '@/lib/chartStep';
import { tsShort, timeRangeToNs, sevName, sevClass, rangeToSince } from '@/lib/utils';
import { severityBandOf } from '@/lib/severityBand';
import { accumulatePage, narrowLoaded } from '@/lib/logAccumulate';
import { logsToCSV, logsToNDJSON, downloadText, exportFilename } from '@/lib/logsExport';
import {
  compileSearch, toggleFilter, encodeFiltersParam, parseFiltersParam, mergePatternQuery,
  extractHighlightTerms, toggleExistsFilter, replaceFilterAt,
} from '@/lib/logFilters';
import type { LogFilter } from '@/lib/logFilters';
import { logsUrlSig, writeLogsParams, readLogsUrlState, buildDocPermalink, parseDocParam, logsUrlHasFilterState } from '@/lib/logsUrl';
import { decideTeamAutoApply, isSameTeam, readTeamOptOut, writeTeamOptOut } from '@/lib/logsTeam';
import { useAuth } from '@/components/AuthProvider';
import { useT } from '@/lib/i18n';
import type { LogsResponse, LogRow, TimeRange } from '@/lib/types';
import { PageShell } from '@/components/coremetry/PageShell';
import { LogsQueryBar } from './logs/LogsQueryBar';
import { LogsFilterRow } from './logs/LogsFilterRow';
import { LogsNotices } from './logs/LogsNotices';
import { LogsAddFilterMenu, LogsShareMenu, LogsMoreMenu } from './logs/LogsActionsMenus';
import { isDraftDirty, bareTraceIdOf } from './logs/logsQuery';
import { LogsTableToolbar } from './logs/LogsTableToolbar';
import { parseLogsView, effectiveLogsView, writeLogsView, logsListEnabled, type LogsView } from './logs/logsView';

// v0.10.1498 — Share affordance (Copy link) moved into pages/logs/LogsActionsMenus.tsx (Share ▾).

// Level facet chips (prototype LogsView .facet/.lvl) — each chip
// drives the EXISTING min-severity filter (filter.severity). The
// `min` is the OTel severity-number floor that the severity <select>
// used: All=0, DEBUG=5, INFO=9, WARN=13, ERROR=17. Clicking a chip
// sets that floor; clicking the active chip again returns to All.
// `bucket` is the canonical severity-band name (matches the names
// the /api/logs/timeseries?groupBy=severity backend returns, see
// LogsHistogram) that we sum counts into for this chip's badge.
// ACC_CAP — ceiling on rows held by the static list's "Load more"
// accumulation (v0.9.292). The live tail has had LIVE_CAP since it
// shipped; the static list accumulated without limit, so twenty clicks
// meant two thousand live rows whose React tree and per-row highlight
// transform both scale linearly. 2000 is generous — past that the
// answer is a narrower filter, not more scrolling.
const ACC_CAP = 2000;
// v0.10.967 — tablo standardı T12: veri yokken / okunamadığında tabloya
// verilen SABİT boş dizi (her render'da yeni [] j/k gezinmesini tazelerdi).
const NO_LOGS: LogRow[] = [];

const LVL_FACETS: Array<{ key: string; label: string; min: number }> = [
  { key: 'error', label: 'ERROR', min: 17 },
  { key: 'warn',  label: 'WARN',  min: 13 },
  { key: 'info',  label: 'INFO',  min: 9  },
  { key: 'debug', label: 'DEBUG', min: 5  },
];

// Map a backend severity-band name (ERROR / FATAL / WARN / INFO /
// DEBUG / TRACE / OTHER, any casing) to one of the four chip
// buckets. FATAL folds into ERROR; TRACE + OTHER fold into DEBUG —
// so the four chips always sum to the grand total.
// v0.8.377 — routes through severityBandOf so NUMERIC series names
// ('17', '9', …) from pre-fix cached payloads / exotic backends band
// by their OTel range instead of all falling into debug (the
// operator-reported bug: severity_number-only SDKs showed ERRORS as
// DEBUG). The fixed backends emit canonical names, which the prefix
// logic recognises trivially.
function bandToFacet(name: string): 'error' | 'warn' | 'info' | 'debug' {
  switch (severityBandOf(name)) {
    case 'ERROR': return 'error';
    case 'WARN':  return 'warn';
    case 'INFO':  return 'info';
    default:      return 'debug'; // DEBUG / TRACE / OTHER
  }
}

type SevSeries = { name: string; points: { t: number; v: number }[] };

// pickVolumeBucket — same window→bucket heuristic LogsHistogram
// uses, so the Logs-local stacked-bar volume row and the shared
// histogram below it agree on resolution. Returns seconds.
function pickVolumeBucket(from?: number, to?: number): number {
  if (!from || !to) return 30;
  // v0.9.707 — iki kopya merdiven (burada + LogsHistogram) tek kaynağa
  // indi: logsBucketSec piksel bütçeli, ES-disiplinli (≤240 kova, ≥5 sn).
  return logsBucketSec((to - from) / 1_000_000_000);
}

// Compose a KQL clause from the current filter state so the
// Kibana deep-link lands on the same slice. service / trace_id
// become per-field clauses; the free-text search string passes
// through verbatim (it's already KQL on this page). Returned
// string may be empty — Kibana handles "no query" cleanly.

function LogsInner() {
  const [searchParams, setSearchParams] = useSearchParams();

  // v0.9.1220 (Kibana dilim 3) — histogram kırılımı. URL tek kaynak
  // (yerel state YOK — sig-guard gerektirmez, Share/SavedViews bedava);
  // varsayılan seviye hiç yazılmaz ki eski linkler bayt-bayt kalsın.
  // v0.9.1250 — değer kümesi cluster + namespace ile genişledi; daraltma
  // parseBreakdown'da tek kaynakta (bilinmeyen → seviye).
  const breakdown: LogsBreakdown = parseBreakdown(searchParams.get('breakdown'));
  const setBreakdown = (b: LogsBreakdown) => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev);
      if (b === 'severity') next.delete('breakdown');
      else next.set('breakdown', b);
      return next;
    }, { replace: true });
  };

  // v0.9.431 — v0.9.390'ın sayfa-yerel yığını paylaşılan
  // usePageZoomRange hook'una taşındı. İki boşluk da kapandı:
  // (1) out-of-band Topbar seçimi artık yığını geçersizleştiriyor
  // (bayat pre-zoom girdisi geri yazılamaz), (2) boş yığın + custom
  // pencerede çift-tık default preset'e dönüyor (diğer sayfalarla
  // aynı sözleşme). resetPaging her iki yönde (v0.7.81 kuralı) —
  // closure çağrı anında değerlendirilir, TDZ yok.
  const { range, setRange, handleZoom, handleZoomReset, zoomDepth } = usePageZoomRange(DEFAULT_RANGE_PRESET, () => resetPaging());
  // v0.9.291 — the window handed to the KQL autocomplete. Memoised on
  // the range, never computed bare in JSX (v0.5.184: a bare
  // range→duration call in the tree is a new object each render and
  // refetches forever). The server clamps this to 7d and snaps it to
  // the hour, so it is a hint about what the operator is looking at,
  // not a contract.
  const autocompleteSince = useMemo(() => rangeToSince(range).since, [range]);
  // v0.8.400 (env-separation Phase 4) — /logs consumes the GLOBAL
  // Topbar ?env= picker. Read-only here (the picker writes); every
  // backend round-trip below (list, histogram, facet counts, fields
  // panel, live tail, context modal) narrows to the selected env. The
  // ES backend answers envUnapplied:true when no environment field
  // resolves — surfaced as the honest chip next to the toolbar.
  const [env] = useUrlEnv();
  // Cursor accumulation (v0.8.260 — "Load more" replaced the
  // Back/Next pager; v0.7.22 keyset cursor mechanics unchanged).
  // `cursor` is the opaque `after` token of the page most recently
  // FETCHED ('' = first). Fetched pages append into `accRows`; a
  // filter / range / query change resets both (a cursor from the
  // old result set is meaningless against a new one — v0.7.81).
  const [cursor, setCursor] = useState('');
  const [accRows, setAccRows] = useState<LogRow[]>([]);
  // v0.9.292 — how many rows the accumulation cap has dropped off the
  // top in this browsing session. Surfaced, never silent.
  const [accDropped, setAccDropped] = useState(0);
  const resetPaging = () => { setCursor(''); setAccRows([]); setAccDropped(0); };
  // v0.10.440 (log arama denetimi B3) — sayfa-yerel ↻. Göreli preset'in
  // penceresi timeRangeToNs ile SON range değişiminde donuyor ("son 30 dk"
  // bir saat sonra bir saat önceki 30 dakikayı sorguluyordu, ekranda hiçbir
  // şey bunu söylemiyordu). Sayaç memo bağımlılığında: bir tık pencereyi
  // şimdiye taşır, her from/to tüketicisi (liste, çipler, histogram —
  // histogram React Query değil, o yüzden invalidate yetmezdi) yeniden
  // sorgular. NEDEN setRange değil: preset range'in kimliği değişmez
  // (Dashboard.tsx gerekçesi) ve setRange zoom yığınını siler. Otomatik
  // aralık YOK (ES maliyet disiplini, lib/queries/logs.ts) — yalnız tık.
  const [nowTick, setNowTick] = useState(0);
  // v0.10.1076 — süzgeç durumu İLK render'da URL'den kurulur (aşağıdaki
  // filters / logCols / lastUrlSigRef de aynı okumadan). Eskiden boş
  // varsayılanla başlıyordu ve içe aktarma efekti ilk commit'ten SONRA
  // koştuğu için her derin link (anomali "Logları aç", kayıtlı görünüm,
  // paylaşılan link) önce süzgeçsiz bir liste + histogram isteği atıyordu.
  // Alanlar (LogsUrlFilter): service, cluster, search, severity, traceId,
  // spanId, hasTrace (v0.8.406 — yalnız trace'li satırlar), pattern
  // (v0.10.1071 — küratörlü desen adı; kutuya YAZILMAZ, çip).
  const [initialUrl] = useState(() => readLogsUrlState(searchParams));
  const [filter, setFilter] = useState(initialUrl.filter);
  const [draft, setDraft] = useState(filter);
  // v0.10.1477 (operatör 2026-10-23: "giren kişinin takımına göre logs sayfası
  // filtrelensin otomatik") — "Takımım" varsayılanı. Karar SAF çekirdekte
  // (lib/logsTeam.ts): URL'de süzgeç varsa hiç dokunulmaz (açık link kazanır);
  // takım çözüldüyse VE takım süzgeci açıksa (ES + takım alanı) team= BİR KEZ
  // yazılır (replace). Çip kaldırılırsa vazgeçme kullanıcı başına hatırlanır.
  // Karar beklenirken liste/histogram/desen okuması ATILMAZ — süzgeçsiz bir ES
  // isteği atıp hemen takımlısıyla değiştirmek ES-maliyet disiplinini bozardı.
  const tr = useT();
  const userId = useAuth().user?.id;
  const myTeamQ = useLogsMyTeam();
  const myTeam = myTeamQ.isError ? null : myTeamQ.data;
  const [urlHadFilters] = useState(() => logsUrlHasFilterState(searchParams));
  const teamAutoDone = useRef(false);
  const [teamOptOut, setTeamOptOutState] = useState(() => readTeamOptOut(userId));
  useEffect(() => { setTeamOptOutState(readTeamOptOut(userId)); }, [userId]);
  const teamDecision = decideTeamAutoApply({ urlHadFilters, optedOut: teamOptOut, done: teamAutoDone.current, myTeam });
  // Süzgeç kararı bekleniyor (my-team çözülüyor ya da uygulanmak üzere):
  // statik okumalar o arada koşmaz.
  const teamGate = teamDecision.kind !== 'none';
  const myTeamName = myTeam?.available ? (myTeam.team ?? '').trim() : '';
  const [teamPickerOpen, setTeamPickerOpen] = useState(false);
  const [teamDraft, setTeamDraft] = useState('');
  // teamAutoDone bir ref; kilitlendiğinde kapıyı açmak için bir render gerekir.
  const [, setTeamGateTick] = useState(0);
  // v0.10.298 — "Desenler" paneli (log-search Dilim 2b); v0.10.448 (C8) —
  // `?panel=patterns|templates` URL'den adreslenir (görünüm ipucu; süzgeç
  // kimliğine GİRMEZ — logsUrlSig'e girse her tık sayfalamayı sıfırlardı).
  // v0.10.1499 (Discover dilim 2) — panel bir GÖRÜNÜM oldu: Logs | Desenler |
  // Şablonlar (pages/logs/logsView.ts). URL tek kaynak; yok = Logs. Eski
  // desen paneli açık/kapalı localStorage yedeği KALDIRILDI: param yokken sayfa
  // her zaman satırlarla açılır (paylaşılan link ne gösteriyorsa o).
  const urlView = parseLogsView(searchParams.get('panel'));
  // v0.10.417 (log arama denetimi B5) — satır sarma; okuma yardımı, URL'e
  // girmez (narrow gibi), kalıcı (localStorage). Varsayılan KAPALI: tek
  // satır + … bugünkü davranış.
  const [wrapLines, setWrapLines] = useState<boolean>(() => getRaw('logs.wrap') === '1');
  const toggleWrap = () => setWrapLines(v => { setRaw('logs.wrap', v ? '0' : '1'); return !v; });
  // v0.10.502 (B6) — mode: 'replace' (Ara / satır tıkı, eski davranış) |
  // 'and' (⊕ mevcut metne ekle) | 'not' (⊖ hariç tut, NOT). Ekleme
  // mevcut serbest metni EZMEZ (mergePatternQuery).
  const searchFromPattern = (qStr: string, mode: 'replace' | 'and' | 'not' = 'replace') => {
    const search = mode === 'replace' ? qStr : mergePatternQuery(filter.search, qStr, mode === 'not');
    const next = { ...filter, search };
    // v0.10.1499 — satır / "Ara" (replace) sorguyu yazar VE Logs görünümüne
    // geçer (tek URL yazımı); ⊕ / ⊖ desen görünümünde kalır (süzmeye devam).
    setFilter(next); setDraft(next); resetPaging(); writeUrl(next, filters, undefined, mode === 'replace' ? 'logs' : undefined);
  };
  // v0.9.1100 (F3.5) — ✨ desen anlatımı durumu (Shift emsali).
  const [aiPat, setAiPat] = useState<{ busy: boolean; text: string | null; err: string | null }>({ busy: false, text: null, err: null });
  const explainPatterns = async () => {
    setAiPat({ busy: true, text: null, err: null });
    try {
      const winSec = from && to ? Math.max(60, Math.round((to - from) / 1e9)) : 1800;
      // v0.10.507 (C7) — ekrandaki süzgeç ve pencere de gider; cevap "bakılan
      // kapsam"ı anlatır, filo geneli bölümler ayrı etiketlenir.
      const r = await api.explainLogPatterns(winSec, {
        service: filter.service, cluster: filter.cluster, env, search: compiledSearch,
        severity: filter.severity, fromNs: from ?? undefined, toNs: to ?? undefined,
      });
      setAiPat({ busy: false, text: r.explanation, err: null });
    } catch (e) {
      setAiPat({ busy: false, text: null, err: e instanceof Error ? e.message : 'Anlatım alınamadı' });
    }
  };

  // Structured field filters (Kibana Discover pill model) — separate
  // from the free-text `search`. Compiled together right before any
  // query goes out (compiledSearch below), so the backend contract
  // is unchanged. Pills live in the ?filters= URL param so Copy link
  // and SavedViewsBar reproduce them.
  const [filters, setFilters] = useState<LogFilter[]>(() => parseFiltersParam(initialUrl.filtersRaw)); // v0.10.1076 — ilk render URL'den
  // Dynamic table columns (Discover revamp step 3). localStorage is
  // the operator's standing preference; the ?cols= URL param (only
  // written when non-default) wins on deep links so a shared view
  // reproduces its columns WITHOUT clobbering the recipient's
  // stored preference.
  const COLS_STORE_KEY = 'dt.logs.columns';
  const [logCols, setLogCols] = useState<string[]>(() => {
    // v0.10.1076 — derin linkin cols'u ilk render'dan (içe aktarma efektiyle aynı kural).
    if (initialUrl.colsRaw) return initialUrl.colsRaw.split(',').filter(Boolean);
    const raw = getRaw(COLS_STORE_KEY);
    if (raw) {
      try {
        const a: unknown = JSON.parse(raw);
        if (Array.isArray(a)) return a.filter((x): x is string => typeof x === 'string');
      } catch { /* fall through to defaults */ }
    }
    return DEFAULT_LOG_COLUMNS;
  });
  // '' when the current set matches the defaults → param omitted.
  const colsParam = (cols: string[]) =>
    cols.join(',') === DEFAULT_LOG_COLUMNS.join(',') ? '' : cols.join(',');
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  // v0.5.471 — cluster list for the inline selector. /api/clusters
  // returns the distinct k8s/openshift cluster names seen in the
  // last 24h; small list, fetched once on mount.
  const [clusters, setClusters] = useState<string[]>([]);
  useEffect(() => {
    const toNs = Date.now() * 1_000_000;
    const fromNs = toNs - 24 * 3600 * 1_000_000_000;
    api.clusters(fromNs, toNs)
      .then(r => setClusters(r?.clusters ?? []))
      .catch(() => setClusters([]));
  }, []);
  // v0.5.399 — trace peek state. Clicking the "👁" button next to a
  // trace_id in the log row sets this. Task #6: the peek now opens the
  // CorrelationContextDrawer anchored on that trace_id — the SAME trace + sibling
  // logs the old TracePeekDrawer showed, plus the service's RED metrics lens, all
  // joined on the exact trace_id. No page change, page filter/search untouched.
  const [peekTraceId, setPeekTraceId] = useState<string | null>(null);
  // v0.5.402 — surrounding-context modal state. Clicking "≡ View
  // ±50 context" on an expanded log row stores the pivot row here;
  // LogContextModal fetches the before/after halves and renders
  // the chronological strip.
  const [contextPivot, setContextPivot] = useState<import('@/lib/types').LogRow | null>(null);
  // v0.9.1248 — kalıcı doküman linki (?doc=<ts>.<id>): mevcut context
  // ucundan tek sınırlı sorguyla çözülür (yeni uç yok); bulunan satır
  // context modalını pivot'lar. Ref sig-guard: aynı değer bir kez
  // çözülür — range/filtre yazımları efekti yeniden tetiklemez.
  const docRaw = searchParams.get('doc');
  const docResolvedRef = useRef('');
  // v0.10.420 — 'degraded': backend yavaş (200 {degraded}); 'miss': kayıt yok.
  const [docMiss, setDocMiss] = useState<false | 'miss' | 'degraded'>(false);
  useEffect(() => {
    if (!docRaw || docResolvedRef.current === docRaw) return;
    docResolvedRef.current = docRaw;
    const parsed = parseDocParam(docRaw);
    if (!parsed) { setDocMiss('miss'); return; }
    const svc = searchParams.get('docsvc') || undefined;
    api.logsContext({ ts: parsed.ts, service: svc, env: env || undefined, n: 5 })
      .then(r => {
        if (r?.degraded) { setDocMiss('degraded'); return; } // v0.10.420 — "kayıt yok" değil "backend yavaş"
        const rows = [...(r?.before ?? []), ...(r?.after ?? [])];
        const hit = rows.find(x => x.id === parsed.id);
        if (hit) { setDocMiss(false); setContextPivot(hit); } else setDocMiss('miss');
      })
      .catch(() => setDocMiss('miss'));
  }, [docRaw, searchParams, env]);
  // Live tail (HyperDX-style): poll, prepend new rows. Cadence is 10s — the
  // ≥10s polling budget, and at the operator's ES scale the ingest pipeline
  // (collector batch + ES exporter flush + index refresh) lags ~10s, so a
  // faster poll just burns ES queries without surfacing data any sooner.
  // (v0.7.17 — the v0.7.15 SSE push tailer was reverted: its LIMIT-per-tick
  // fetch dropped logs on a busy service at 10B logs/day.)
  const [live, setLive] = useState(false);
  // v0.10.1499 — görünüm (Logs | Desenler | Şablonlar). Canlı kuyruk Logs'u
  // zorlar; desen görünümü seçmek kuyruğu durdurur, kuyruğu açmak Logs'a döner.
  const view = effectiveLogsView(urlView, live);
  const setView = (v: LogsView) => {
    if (v !== 'logs') setLive(false);
    setSearchParams(prev => writeLogsView(prev, v), { replace: true });
  };
  const toggleLive = () => {
    if (!live && urlView !== 'logs') setSearchParams(prev => writeLogsView(prev, 'logs'), { replace: true });
    setLive(v => !v);
  };
  // v0.9.294 — "narrow within results". Ephemeral on purpose: it is a
  // reading aid over the current buffer, not part of the query, so it
  // does NOT go in the URL (a shared link must reproduce the QUERY, and
  // the recipient's buffer is a different set of rows).
  const [narrow, setNarrow] = useState('');
  // v0.9.295 — sort direction. Both backends have honoured oldest-first
  // since v0.7.83, but only the Context modal ever asked; the list had
  // no control. In the URL so a shared link reproduces the order.
  const [asc, setAsc] = useState(() => searchParams.get('asc') === '1');
  // v0.10.1220 — dış URL değişimi (geri/ileri, kayıtlı görünüm) yönü de
  // içe alır; toggleAsc state'i ve URL'i birlikte yazdığı için yankı no-op.
  const urlAsc = searchParams.get('asc') === '1';
  useEffect(() => {
    if (urlAsc !== asc) { setAsc(urlAsc); resetPaging(); }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- yalnız URL değişiminde
  }, [urlAsc]);
  // Flipping direction MUST reset paging: the keyset cursor is a strict
  // inequality tied to its direction, so a token minted newest-first is
  // meaningless oldest-first. The backend drops a mismatched token
  // rather than obeying it (v0.9.295), so without this the operator
  // would silently bounce back to page one — resetting here makes that
  // the intended behaviour instead of a surprise.
  const toggleAsc = () => {
    setAsc(v => {
      const next = !v;
      setSearchParams(prev => {
        const p = new URLSearchParams(prev);
        if (next) p.set('asc', '1'); else p.delete('asc');
        return p;
      }, { replace: true });
      return next;
    });
    resetPaging();
  };

  // Sync filter state from URL params. Covers (a) static-prerender →
  // CSR hydration, where useState initializes against empty
  // searchParams; (b) in-app navigations that update the URL without
  // remounting the page. Anomaly + service drill-down links rely on
  // this — they pass ?service=<svc>&q=<token> and expect the page to
  // land already scoped.
  //
  // Sig-guarded (Discover revamp step 1): the page now WRITES filter
  // params back to the URL (apply / pill actions / clearTraceLock),
  // and useUrlRange writes ?range=. Without the guard, every URL
  // write re-ran this import and wiped locally-applied state (a
  // range change used to clear an applied-but-not-in-URL filter).
  // The sig hashes only the filter-bearing params, so range-only
  // changes no-op and self-writes (which pre-store their own sig)
  // don't double-apply.
  // v0.8.546 — sig/write/read all come from lib/logsUrl so they cannot
  // drift apart. `severity` used to be missing from all three: the chip
  // changed the filter, the URL never learned, and Share handed out a link
  // that opened on All levels.
  // v0.10.318 (DataTable/ContextBar audit dilim 8, Logs) — SUNUCU sütun
  // tercihi (useTablePrefs 'logs', saved_views page='table:logs'); Traces
  // v0.10.251 sözleşmesinin aynısı. Öncelik: URL cols= (deep link, sunucuyu
  // EZMEZ) > sunucu > localStorage > varsayılan. URL'de cols yokken sunucu
  // modeli BİR KEZ benimsenir (prefs çözülünce); yalnız operatörün kendi
  // değişikliği (changeCols) sunucuya yazılır — deep-link'in kolonları
  // alıcının tercihini bozmaz (v0.9.4xx tasarımı korunur). Kendi-kendine-
  // yazım yarışı: prefs bekliyorken ve URL'de cols yokken writeUrl cols
  // yazmaz — aksi hâlde URL > sunucu önceliği sunucu tercihini gömerdi.
  const prefs = useTablePrefs('logs');
  const urlHadCols = useRef(!!parseColsParam(searchParams.get('cols')));
  const prefsAdopted = useRef(false);
  useEffect(() => {
    if (prefs.model === undefined || prefsAdopted.current) return;
    prefsAdopted.current = true;
    if (urlHadCols.current || !prefs.model) return;
    const hidden = new Set(prefs.model.hidden);
    const visible = prefs.model.order.filter(id => !hidden.has(id));
    if (visible.length) setLogCols(visible);
  }, [prefs.model]);
  const urlSig = logsUrlSig;
  // v0.10.1076 — ilk sig önceden yazılı: durum zaten URL'den kuruldu, bağlama
  // anındaki içe aktarma no-op'tur (ikinci, aynı dilimli set yok). Sonraki
  // URL değişimleri (SPA gezinmesi, kayıtlı görünüm) sig farkıyla içe aktarılır.
  const lastUrlSigRef = useRef<string | null>(initialUrl.sig);
  useEffect(() => {
    const { filter: next, filtersRaw, colsRaw, sig } = readLogsUrlState(searchParams);
    if (sig === lastUrlSigRef.current) return;
    lastUrlSigRef.current = sig;
    setFilter(next);
    setDraft(next);
    setFilters(parseFiltersParam(filtersRaw));
    // Deep-linked columns override the view; absence means "keep
    // whatever the operator prefers" (localStorage init), so a plain
    // /logs link never resets their column setup.
    if (colsRaw) setLogCols(colsRaw.split(',').filter(Boolean));
    resetPaging();
  }, [searchParams]);

  // State → URL. Writes every filter-bearing param (replace:true — a
  // filter tweak refines the view, no history entry per click) and
  // pre-stores the sig so the import effect above treats the
  // resulting searchParams change as a no-op.
  // v0.10.1499 — `view` verilirse görünüm (?panel=) AYNI yazımda değişir:
  // aynı tikte iki setSearchParams birbirinin `prev`ini görmez.
  const writeUrl = (f: typeof filter, pills: LogFilter[], cols?: string[], view?: LogsView) => {
    const filtersRaw = encodeFiltersParam(pills);
    const colsRaw = (prefs.model === undefined && !urlHadCols.current) ? '' : colsParam(cols ?? logCols);
    lastUrlSigRef.current = urlSig(f, filtersRaw, colsRaw);
    setSearchParams(prev => {
      const next = writeLogsParams(prev, f, filtersRaw, colsRaw);
      return view ? writeLogsView(next, view) : next;
    }, { replace: true });
  };

  // Column mutations: state + standing preference + URL in one step.
  const changeCols = (next: string[]) => {
    setLogCols(next);
    setRaw(COLS_STORE_KEY, JSON.stringify(next));
    // v0.10.318 — operatörün kendi değişikliği sunucuya (debounce'lu PUT,
    // BroadcastChannel ile diğer sekmeler). Genişlikler tarayıcı-yerel kalır.
    prefs.save({ v: 1, order: next, hidden: [], sig: 'logs' });
    writeUrl(filter, filters, next);
  };
  const removeColumn = (id: string) => changeCols(logCols.filter(c => c !== id));
  const toggleColumn = (id: string) =>
    changeCols(logCols.includes(id) ? logCols.filter(c => c !== id) : [...logCols, id]);

  // Build the params for the static-window query. When live
  // tail is on, we don't run this query (the live useQuery
  // below takes over instead) — `enabled: !live` gates it.
  //
  // CRITICAL: `from` / `to` are computed via timeRangeToNs which
  // reads Date.now() for non-custom presets. Without memoising,
  // every render produces a NEW from/to (Date.now() advanced by
  // a few ms), the React Query key hashes differently, RQ
  // starts a fresh query and discards the previous — isLoading
  // stays true forever and the page is stuck on the skeleton.
  // Memoise on the range / traceId-filter so the values only
  // refresh when the operator actually changes the inputs.
  // v0.10.690 — traceId + mutlak aralık → pencere gönderilir (derin bağlantı);
  // göreli aralıkta sunucu trace'in penceresini çözer (lib/logsTraceWindow.ts).
  const useTimeRange = logsUseTimeRange(filter.traceId, range);
  const { from, to } = useMemo(
    () => useTimeRange ? timeRangeToNs(range) : { from: undefined, to: undefined },
    // nowTick: ↻ tıkı göreli pencereyi şimdiye taşır (v0.10.440, B3).
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [useTimeRange, range, nowTick],
  );
  // Pills + free text → the single query string every consumer
  // (table, facets, histogram, live tail, Kibana link) sends to the
  // backend. Disabled pills drop out here — no backend round-trip
  // knows pills exist.
  const compiledSearch = useMemo(
    () => compileSearch(filters, filter.search),
    [filters, filter.search],
  );
  // v0.10.1284 (AI SRE Faz 1a) — histogram başlığının ✨ "Artışı açıkla"
  // öznesi. Zoom'lanmışsa (brush) seçilen aralık VERİLEN artıştır (sel),
  // değilse pencerede tepe otomatik bulunur (auto). Trace pinliyken yok (bir
  // trace'in logları artış sorusu değil). Pencere useMemo'daki from/to'dan —
  // JSX'te timeRangeToNs yok (v0.5.184). Sayfanın cluster/pattern süzgeci
  // özneye girmez (araç bu boyutları KIRILIM olarak okur); env + arama girer.
  const spikeSubject = useMemo<AISubject | undefined>(() => {
    if (!from || !to || filter.traceId) return undefined;
    return {
      kind: 'log-spike', id: filter.service || '', fromNs: from, toNs: to,
      mode: zoomDepth > 0 ? 'sel' : 'auto', env: env || '',
      q: compiledSearch.slice(0, LOG_SPIKE_QUERY_MAX),
      level: filter.severity >= 17 ? 'error' : 'all',
    };
  }, [from, to, filter.traceId, filter.service, filter.severity, zoomDepth, env, compiledSearch]);
  // <mark> terms for the message cell — the APPLIED free-text
  // query's bare terms + quoted phrases only (field clauses and
  // pills excluded; a level:error clause must not light unrelated
  // "error" text). Client-side matching only.
  const highlightTerms = useMemo(
    () => extractHighlightTerms(filter.search),
    [filter.search],
  );
  // Slice for the fields-panel accordion fetches — stable identity
  // so the panel's useQuery keys only change when the slice does.
  const fieldStatsScope = useMemo(() => ({
    from, to,
    service: filter.service || undefined,
    cluster: filter.cluster || undefined,
    env: env || undefined, // v0.8.400 — global env filter
    search: compiledSearch || undefined,
    pattern: filter.pattern || undefined, // v0.10.1071
    team: filter.team || undefined, // v0.10.1477
    severity: filter.severity > 0 ? filter.severity : undefined,
    traceId: filter.traceId || undefined,
    spanId: filter.spanId || undefined,
  }), [from, to, filter.service, filter.cluster, env, compiledSearch, filter.pattern, filter.team, filter.severity, filter.traceId, filter.spanId]);
  const staticQ = useLogs({
    limit: 100, after: cursor || undefined, from, to,
    service: filter.service || undefined,
    cluster: filter.cluster || undefined,
    env: env || undefined, // v0.8.400 — global env filter
    search: compiledSearch || undefined,
    pattern: filter.pattern || undefined, // v0.10.1071 — desen süzgeci (sunucuda dedektörün yüklemi)
    team: filter.team || undefined, // v0.10.1477 — takım süzgeci (sunucu ham yazımlara açar)
    severity: filter.severity > 0 ? filter.severity : undefined,
    traceId: filter.traceId || undefined,
    spanId:  filter.spanId  || undefined,
    hasTrace: filter.hasTrace || undefined, // v0.8.406 — trace-only filter
    asc: asc || undefined, // v0.9.295 — oldest-first
  }, { enabled: logsListEnabled(view, live, teamGate) }); // v0.10.420 — canlıyken statik sorgu koşmaz; v0.10.1477 — takım kararı beklenirken de; v0.10.1499 — desen görünümünde de (tablo çizilmez)

  // Level-facet counts. A per-severity timeseries query feeds the
  // toolbar facet chip badges (the duplicate Logs-local stacked-bar
  // volume row was removed in v0.8.115 — the Elastic-style
  // LogsHistogram below is the one volume viz). Scoped
  // to the current applied filter (service / search / trace) and
  // the active window, but deliberately WITHOUT severity — the
  // chips must show counts for every level, otherwise selecting
  // ERROR would zero out the other chips (Kibana/Datadog facet
  // behaviour). `from`/`to` come from the already-memoised window
  // (no bare timeRangeToNs — v0.5.184). Needs a bounded window OR a
  // trace pin; the query key carries from/to so a preset's Date.now
  // drift doesn't thrash it (the parent memo already froze them).
  const volumeEnabled = (from !== undefined && to !== undefined) || !!filter.traceId;
  const volumeBucket = useMemo(() => pickVolumeBucket(from, to), [from, to]);
  // v0.9.1220 — çift-fetch katlaması: severity=0 + seviye-kırılımında bu
  // sorgu LogsHistogram'ın iç fetch'iyle BAYT-BAYT aynıydı (aynı uç, aynı
  // parametreler) — her /logs açılışında iki özdeş ES _search. O durumda
  // çipler histogramın onSeries'inden beslenir; bu sorgu yalnız seviye
  // tabanı aktifken (histogram alt-küme çeker, çipler TAM sayım ister —
  // yukarıdaki "chips must show counts for every level" sözleşmesi) veya
  // seviye dışı kırılımdayken (seriler artık bant değil) koşar.
  // v0.9.1250 — kural histogramın kendi onSeries koşuluyla tek kaynakta
  // (histogramFeedsChips): cluster/namespace eksenlerinde grafik bant
  // yaymaz, çipler otomatik olarak bu sorguya döner.
  const chipsFromHistogram = histogramFeedsChips(breakdown, filter.severity);
  // Tri-state: undefined = yükleniyor · null = histogram fetch'i HATA verdi
  // (v0.9.1220 review bulgusu — sonsuz '·' yerine hata rozeti) · dizi = veri.
  const [histTotals, setHistTotals] =
    useState<{ name: string; total: number }[] | null | undefined>(undefined);
  useEffect(() => {
    // Pencere/filtre değişti → histogram yeniden çekiyor; bayat toplamları
    // taze diye göstermemek için çipleri yükleniyor'a döndür. severity da
    // dep: ERROR tabanındayken onSeries ALT-KÜME toplamları vermişti;
    // All'a dönüşte o alt-küme "tüm seviyelerin sayımı" gibi görünmesin.
    setHistTotals(undefined);
  }, [from, to, filter.service, filter.cluster, env, compiledSearch, filter.pattern, filter.team, filter.severity, filter.traceId, filter.hasTrace, breakdown]);
  const volumeQ = useQuery({
    // v0.9.216 — cluster joins the key AND the request: without it the chips
    // counted every cluster while the table below showed one.
    queryKey: ['logs', 'sev-volume', from, to, filter.service, filter.cluster, env, compiledSearch, filter.pattern, filter.team, filter.traceId, filter.hasTrace, volumeBucket],
    queryFn: () => api.logsTimeseries({
      from, to,
      service: filter.service || undefined,
      cluster: filter.cluster || undefined,
      env:     env || undefined, // v0.8.400 — global env filter
      search:  compiledSearch || undefined,
      pattern: filter.pattern || undefined, // v0.10.1071
      team:    filter.team || undefined, // v0.10.1477
      traceId: filter.traceId || undefined,
      hasTrace: filter.hasTrace || undefined, // v0.8.406 — trace-only filter
      groupBy: 'severity',
      bucketSec: volumeBucket,
    }),
    enabled: volumeEnabled && !chipsFromHistogram && !teamGate,
    staleTime: 30_000,
  });
  const sevSeries: SevSeries[] = volumeQ.data ?? [];
  // Per-chip counts (summed across all buckets), keyed by facet.
  // v0.9.1220 — kaynak ikili: katlama modunda histogramın zaten çektiği
  // seviye toplamları, aksi hâlde bu sayfanın kendi sorgusu.
  const facetCounts = useMemo(() => {
    const c: Record<string, number> = { all: 0, error: 0, warn: 0, info: 0, debug: 0 };
    const totals = chipsFromHistogram
      ? (histTotals ?? []).map(t => ({ name: t.name, sum: t.total }))
      : sevSeries.map(s => ({ name: s.name, sum: s.points.reduce((a, p) => a + p.v, 0) }));
    for (const t of totals) {
      c[bandToFacet(t.name)] += t.sum;
      c.all += t.sum;
    }
    return c;
  }, [sevSeries, histTotals, chipsFromHistogram]);
  const facetLoading = chipsFromHistogram
    ? (volumeEnabled && histTotals === undefined)
    : volumeQ.isLoading;

  // Live-tail (v0.8.x) — server-pushed SSE replaces the old 10s poll. The
  // table renders a bounded, newest-first client buffer fed by
  // /api/logs/stream: each `event: log` prepends a row (dedup by id, capped
  // at LIVE_CAP); an `event: gap` flags that a busy service outran the
  // per-tick read. The stream closes on document.hidden and reopens on show,
  // catching up via `since` = the newest row we hold (mirrors the SSE
  // reconnect catch-up shipped in eventStream.ts). A filter change starts a
  // fresh stream.
  const LIVE_CAP = 1000;
  const [liveBuffer, setLiveBuffer] = useState<LogRow[]>([]);
  const [liveGap, setLiveGap] = useState(false);
  const newestNsRef = useRef(0);

  useEffect(() => {
    if (!live || typeof EventSource === 'undefined') return;
    setLiveBuffer([]); setLiveGap(false); newestNsRef.current = 0;
    let es: EventSource | null = null;
    const open = () => {
      const p = new URLSearchParams();
      if (filter.service) p.set('service', filter.service);
      if (filter.cluster) p.set('cluster', filter.cluster);
      if (env) p.set('env', env); // v0.8.400 — global env filter
      if (compiledSearch) p.set('search', compiledSearch);
      if (filter.severity > 0) p.set('severity', String(filter.severity));
      if (filter.hasTrace) p.set('hasTrace', '1'); // v0.8.406 — trace-only filter
      if (filter.pattern) p.set('pattern', filter.pattern); // v0.10.1071 — desen süzgeci canlı kuyrukta da
      if (filter.team) p.set('team', filter.team); // v0.10.1477 — takım süzgeci canlı kuyrukta da
      // v0.10.416 (log arama denetimi B2) — trace kilidi canlı kuyruğa da
      // taşınır; eskiden "Filtered to trace" şeridi dururken kuyruk tüm
      // servisi akıtıyordu. Sunucu zaten okuyor (streamLogs traceId/spanId).
      if (filter.traceId) p.set('traceId', filter.traceId);
      if (filter.spanId) p.set('spanId', filter.spanId);
      if (newestNsRef.current) p.set('since', String(newestNsRef.current)); // reconnect catch-up
      es = new EventSource('/api/logs/stream?' + p.toString(), { withCredentials: true });
      es.addEventListener('log', (e) => {
        let row: LogRow;
        try { row = JSON.parse((e as MessageEvent).data) as LogRow; } catch { return; }
        if (row.timestamp > newestNsRef.current) newestNsRef.current = row.timestamp;
        setLiveBuffer(buf => {
          if (buf.some(r => r.id === row.id)) return buf; // dedup (boundary / reconnect overlap)
          const next = [row, ...buf];
          if (next.length > LIVE_CAP) next.length = LIVE_CAP;
          return next;
        });
      });
      es.addEventListener('gap', () => setLiveGap(true));
      // EventSource auto-reconnects on transport 'error'; the next 'open'
      // catches up forward via newestNsRef — nothing to do here.
    };
    open();
    const onVis = () => {
      if (document.hidden) { es?.close(); es = null; }
      else if (!es) { open(); }
    };
    document.addEventListener('visibilitychange', onVis);
    return () => {
      document.removeEventListener('visibilitychange', onVis);
      es?.close();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [live, filter.service, filter.cluster, env, compiledSearch, filter.pattern, filter.team, filter.severity, filter.hasTrace, filter.traceId, filter.spanId]); // v0.10.416 — kilit değişince akış yeniden açılır

  // When live, the table renders the SSE buffer; otherwise the static
  // windowed query. Live has no loading/error gate — rows fill in as they
  // arrive (an empty buffer shows the "waiting" empty state).
  const data: LogsResponse | undefined | null = live
    ? { total: liveBuffer.length, logs: liveBuffer, nextCursor: '' }
    : staticQ.isLoading ? undefined
    : staticQ.isError ? null
    : staticQ.data;

  // Accumulate fetched pages into accRows (v0.8.260 Load more).
  // First page replaces (covers fresh views AND a staleTime
  // refetch of page 1); later cursors append with id-dedup so the
  // keyset boundary can't duplicate a row. isPlaceholderData
  // guards against ingesting the keep-previous stand-in while the
  // next page is still in flight.
  useEffect(() => {
    if (live || !staticQ.data || staticQ.isPlaceholderData) return;
    const page = staticQ.data.logs ?? [];
    setAccRows(prev => {
      if (!cursor) { setAccDropped(0); return page; }
      const { rows, dropped } = accumulatePage(prev, page, ACC_CAP);
      // v0.9.292 — accumulation was UNCAPPED: 20 "Load more" clicks =
      // 2000 live rows, and while content-visibility keeps painting
      // cheap (LogTable), the React tree and the per-row highlight
      // transform (highlightSegments) grow linearly. The live tail has
      // had LIVE_CAP since it shipped; the static list never got one.
      //
      // The window slides FORWARD, dropping from the front. The
      // operator clicking "Load more" is reading downward, so the rows
      // they just asked for are the ones to keep — but rows leaving the
      // top is exactly the kind of silent disappearance this page keeps
      // getting wrong, so the count is surfaced below the table.
      if (dropped > 0) setAccDropped(d => d + dropped);
      return rows;
    });
  }, [staticQ.data, staticQ.isPlaceholderData, cursor, live]);

  // Reset expansion state when the filter / range changes —
  // opening row #5 in one window doesn't translate to the next.
  // NOT on cursor: Load more appends below, existing expansions
  // must survive the append.
  useEffect(() => { setExpanded(new Set()); }, [range, filter, filters]);

  // v0.8.400 — an env change is a NEW result set: a keyset cursor from
  // the old set is meaningless (the v0.7.81 cursor rule) and row
  // expansion doesn't carry over.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { resetPaging(); setExpanded(new Set()); }, [env]);

  // Backend mapping fields (v0.5.136 → fields panel v0.8.255).
  // Feeds the left LogFieldsPanel's "Available fields" group. CH
  // backend returns an empty list (its shape is fixed).
  const [fields, setFields] = useState<string[]>([]);
  const [fieldsTotal, setFieldsTotal] = useState<number | undefined>(undefined);
  // v0.10.280 — alan → mapping tipi (panel tip rozeti); CH'de sabit şema.
  const [fieldTypes, setFieldTypes] = useState<Record<string, string>>({});

  // Kibana deep-link config (v0.5.236). Loaded once on mount;
  // when disabled or unconfigured, buildKibanaURL returns null
  // and the button doesn't render.
  const [kibana, setKibana] = useState<KibanaSettings | null>(null);
  useEffect(() => {
    api.getKibanaSettings()
      .then(s => setKibana(s ?? null))
      .catch(() => setKibana(null));
  }, []);
  useEffect(() => {
    api.logsFields()
      .then(d => {
        setFields(d.fields ?? []);
        // v0.9.292 — the mapping's REAL path count. The backend caps
        // the list; the rail says "first N of M" rather than implying
        // the cap is the whole mapping.
        setFieldsTotal(d.total);
        setFieldTypes(d.types ?? {});
      })
      .catch(() => { setFields([]); setFieldsTotal(undefined); setFieldTypes({}); });
  }, []);
  // v0.10.447 (B3 ikinci yarı, operatör kararı 2026-09-06) — Search her
  // zaman ARAR: eskiden aynı taslakla tık React'te bail-out ediyor,
  // aynı parametreler aynı RQ anahtarına düşüp 15 sn staleTime içinde
  // hiç istek atmıyordu ("Search" düğmesi aramıyordu). Sayaç göreli
  // pencereyi şimdiye taşır (özel aralıkta refetch); maliyet tık başına
  // bir sorgu — otomatik aralık yok.
  const apply = () => {
    resetPaging(); setFilter(draft); writeUrl(draft, filters);
    if (range.preset === 'custom') void staticQ.refetch(); else setNowTick(t => t + 1);
  };
  const reset = () => {
    const empty = { service: '', cluster: '', search: '', severity: 0, traceId: '', spanId: '', hasTrace: false, pattern: '', team: '' };
    setDraft(empty); setFilter(empty); setFilters([]); resetPaging();
    writeUrl(empty, []);
  };
  // v0.10.440 (B3) — sayfa-yerel ↻: göreli preset'in penceresi SON range
  // değişiminde donar; tık pencereyi şimdiye taşır (nowTick memo bağımlılığı),
  // özel aralıkta pencere sabittir → yalnız yeniden sorgular. Cursor sıfırlanır
  // (v0.7.81). Otomatik aralık YOK — yalnız tık. v0.10.1498: Search ile aynı
  // düğmede (LogsQueryBar), taslak = uygulanan iken; canlı kuyrukta çizilmez.
  const refresh = () => {
    resetPaging(); if (range.preset === 'custom') void staticQ.refetch(); else setNowTick(t => t + 1);
  };
  const draftDirty = isDraftDirty(draft, filter);
  // v0.10.1498 — KQL kutusunda çıplak bir trace id → "trace olarak süz"
  // önerisi (Trace ID kutusu artık + Filter ▾ içinde). Tık kutuyu boşaltır,
  // id'yi trace kilidine yazar ve uygular (Search ile aynı yol).
  const traceHint = bareTraceIdOf(draft.search);
  const applyTraceHint = () => {
    const next = { ...draft, search: '', traceId: traceHint, spanId: '' };
    resetPaging(); setDraft(next); setFilter(next); writeUrl(next, filters);
  };
  // v0.9.302 — export what is ALREADY loaded (⋯ menüsünde, v0.10.1498). Zero
  // backend calls; exports what the operator SEES, local narrow included.
  const exportLoaded = (kind: 'csv' | 'ndjson') => {
    if (kind === 'csv') downloadText(logsToCSV(logs), exportFilename('csv'), 'text/csv;charset=utf-8');
    else downloadText(logsToNDJSON(logs), exportFilename('ndjson'), 'application/x-ndjson');
  };
  // v0.9.574 — Kibana derin linki: servis / trace-id / arama → KQL, pencere →
  // zaman aralığı. v0.10.1071 / v0.10.1477 — desen ve takım süzgeci KQL'e
  // çevrilemez (yüklem/değerler sunucuda); "aynı dilim" sözü tutmayacaksa
  // bağlantı yok. Settings → Kibana boşsa buildKibanaURL null döner.
  const kibanaHref = (filter.pattern || filter.team) ? null : buildKibanaURL(kibana, {
    fromNs: from ?? undefined,
    toNs: to ?? undefined,
    kql: buildKQLFromFilter({ ...filter, search: compiledSearch }),
  });

  // Commit a new pill set: state + paging + URL in one step. Every
  // pill mutation (add/negate/disable/remove) is an auto-apply —
  // same as the old toggleSearchClause behaviour.
  const applyPills = (next: LogFilter[]) => {
    setFilters(next);
    resetPaging();
    writeUrl(filter, next);
  };
  const negatePill  = (i: number) => applyPills(filters.map((f, j) => (j === i ? { ...f, negated: !f.negated } : f)));
  // v0.9.1219 (dilim 1) — pill EDIT popover'ı: hangi pill düzenleniyor.
  const [editPill, setEditPill] = useState<number | null>(null);
  const disablePill = (i: number) => applyPills(filters.map((f, j) => (j === i ? { ...f, disabled: !f.disabled } : f)));
  const removePill  = (i: number) => applyPills(filters.filter((_, j) => j !== i));

  // Expanded-row KvRow click-to-filter handlers (v0.5.229 →
  // Discover pills). Operator clicks ⊕ on any attribute → adds a
  // pill; ⊖ → adds a negated pill. Toggle semantics (same polarity
  // removes, opposite flips) live in lib/logFilters.ts.
  const addFromRow      = (key: string, value: string) => applyPills(toggleFilter(filters, key, value, false));
  // v0.9.1217 — varlık pill'i (alan paneli ∃ düğmeleri).
  const existsFromPanel = (key: string, negated: boolean) => applyPills(toggleExistsFilter(filters, key, negated));
  const excludeFromRow  = (key: string, value: string) => applyPills(toggleFilter(filters, key, value, true));
  // v0.8.406 — trace-only toggle. Auto-applies (facet-chip semantics,
  // not draft/Search): state + paging + URL in one step so Copy link
  // reproduces the view.
  const toggleHasTrace = () => {
    const next = { ...filter, hasTrace: !filter.hasTrace };
    setFilter(next); setDraft(d => ({ ...d, hasTrace: next.hasTrace }));
    resetPaging();
    writeUrl(next, filters);
  };
  const clearTraceLock = () => {
    const next = { ...filter, traceId: '', spanId: '' };
    setFilter(next); setDraft(d => ({ ...d, traceId: '', spanId: '' }));
    writeUrl(next, filters);
  };
  // v0.10.1071 — desen çipinin ×'i: yalnız deseni kaldırır; serbest metin,
  // pill'ler ve pencere durur (trace kilidinin "✕ Clear"ı gibi oto-uygula).
  const clearPattern = () => {
    const next = { ...filter, pattern: '' };
    setFilter(next); setDraft(d => ({ ...d, pattern: '' }));
    resetPaging();
    writeUrl(next, filters);
  };
  // v0.10.1477 — takım süzgeci (oto-uygula, desen çipiyle aynı). Kendi
  // takımını seçmek vazgeçmeyi siler; çipin ×'i vazgeçmeyi yazar (otomatik
  // varsayılan bu tarayıcıda bu kullanıcı için bir daha uygulanmaz).
  const setTeam = (team: string) => {
    const t = team.trim();
    const next = { ...filter, team: t };
    setFilter(next); setDraft(d => ({ ...d, team: t }));
    resetPaging();
    writeUrl(next, filters);
    if (t && myTeamName && isSameTeam(t, myTeamName) && teamOptOut) {
      writeTeamOptOut(userId, false); setTeamOptOutState(false);
    }
  };
  const clearTeam = () => {
    writeTeamOptOut(userId, true); setTeamOptOutState(true);
    setTeamPickerOpen(false);
    setTeam('');
  };
  // Otomatik varsayılan — BİR KEZ. Karar verildiği anda (uygula ya da hiçbir
  // şey) teamAutoDone kilitlenir; sonraki URL değişimleri (sıfırlama, SPA
  // gezinmesi) yeniden uygulamaz. Operatör bu arada kendisi bir süzgeç
  // kurduysa onun seçimi kazanır.
  useEffect(() => {
    if (teamAutoDone.current || teamDecision.kind === 'pending') return;
    teamAutoDone.current = true;
    if (teamDecision.kind !== 'apply') { setTeamGateTick(t => t + 1); return; }
    const pristine = !filter.service && !filter.cluster && !filter.search && !filter.severity
      && !filter.traceId && !filter.spanId && !filter.hasTrace && !filter.pattern && !filter.team
      && filters.length === 0;
    if (pristine) setTeam(teamDecision.team);
    else setTeamGateTick(t => t + 1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [teamDecision.kind]);
  const toggle = (id: number) => {
    const next = new Set(expanded);
    if (next.has(id)) next.delete(id); else next.add(id);
    setExpanded(next);
  };

  // Static view renders the ACCUMULATED rows; the raw page in
  // `data` only feeds total/nextCursor/empty-state checks. Falls
  // back to the page rows in the one render before the
  // accumulator effect fills (or while a placeholder shows the
  // previous slice during a filter change).
  const loadedRows = live ? (data?.logs ?? []) : (accRows.length > 0 ? accRows : (data?.logs ?? []));
  // v0.9.294 — "narrow within results": a LOCAL filter over the rows
  // already in the page. Zero backend calls; today every narrowing is a
  // full round trip, and at 10B docs/day the cheapest query is the one
  // you don't send. Labelled explicitly below — a local subset read as
  // a window-wide answer would be the worst kind of wrong here.
  const logs = useMemo(() => narrowLoaded(loadedRows, narrow), [loadedRows, narrow]);
  const total = data?.total ?? 0;

  // j/k row navigation — same pattern as /services and /traces.
  // Enter / o on the selected row toggles the expansion
  // (matches the existing click behaviour), Esc clears the
  // selection. The hook scrolls the active row into view via
  // [data-row-idx], which we set on the LogRowR below.
  // v0.10.967 — tablo standardı T12 (dilim 5): tabloda GÖRÜNEN satırlar.
  // Yükleniyor / hata anında boş: başarısız bir yenilemede birikmiş eski
  // satırlar (accRows) hatanın üstünde sessizce kalmaz — eskiden de tablo
  // o anda hiç çizilmiyordu. j/k aynı diziyi gezer (görünmeyen satır seçilmez).
  const shownLogs = data ? logs : NO_LOGS;
  const tableNav = useTableNav<LogRow>(shownLogs, {
    onOpen: (l) => toggle(l.id),
    pageId: 'logs',
  });
  // "Filtreleri temizle" (yerel daraltma) kendini kaldırınca odak kutuya döner.
  const narrowRef = useRef<HTMLInputElement>(null);

  // v0.10.967 — tablo standardı T12 (dilim 5, P-1): yükleniyor / hata /
  // yavaş backend / boş / yerel daraltma "eşleşme yok" tablonun İÇİNDE,
  // başlık kalır. Kapılar ve sıra eskisiyle aynı (v0.10.415/416/420
  // yorumları aşağıda yerinde). Eski kutuların eylemleri kaybolmadı:
  // "↻ Retry" → onRetry (aynı staticQ.refetch), "Clear filters" / sunucu
  // metni / KQL örneği / span filtresini kaldır bağlantısı / system stats
  // linki `detail` yuvasında. Yavaş backend bandı (rozet) tablonun üstünde
  // kalır; boş satırı artık hata türü (⚠ + Retry, "boş sonuç değil").
  const logsState: Omit<DataTableStateProps<LogRow>, 'dt' | 'leading' | 'trailing'> =
    data === undefined ? { kind: 'loading', skeletonRows: 12 }
    // v0.9.215 — the error leg of the tri-state used to render NOTHING:
    // data===null fell through every branch, so a rejected query (malformed
    // KQL, ES timeout, backend down) left a blank page under the toolbar.
    // The operator reads that as "no logs", not "your query didn't run".
    : data === null ? {
      kind: 'error',
      message: 'Log sorgusu çalışmadı — log backend sorguyu reddetti ya da zamanında cevap vermedi.',
      onRetry: () => void staticQ.refetch(),
      detail: (
        <div style={{ color: 'var(--text2)' }}>
          {staticQ.error instanceof Error && staticQ.error.message && (
            <div className="mono" style={{
              marginTop: 8, padding: '6px 9px', fontSize: 11.5,
              background: 'var(--bg2)', border: '1px solid var(--border)',
              borderRadius: 6, color: 'var(--err)', whiteSpace: 'pre-wrap',
              overflowWrap: 'anywhere',
            }}>
              {staticQ.error.message}
            </div>
          )}
          <div style={{ marginTop: 8 }}>
            Most often the search text isn’t valid KQL — check quotes and
            field names (<code>level:error AND service.name:"checkout"</code>),
            or clear the search box to confirm the backend answers at all.
          </div>
          <div style={{ marginTop: 8 }}>
            <Button variant="ghost" size="sm" onClick={reset}>Clear filters</Button>
          </div>
        </div>
      ),
    }
    // v0.10.415 (B1) — degraded boş durum: "No logs found" YALAN olurdu.
    // v0.10.420 — kapı YÜKLENEN satırlar: narrow her şeyi süzdüyse kendi
    // durumu var (aşağıdaki no-match); burada boş durum çizilmez.
    : data && loadedRows.length === 0 && !live && !!staticQ.data?.degraded ? {
      kind: 'error',
      message: `Log backend yavaş — bu liste eksik: ${staticQ.data?.reason ?? 'log backend slow/unreachable'}. Sonuç 15 sn önbellekte — pencereyi daralt, servis filtresi ekle ya da yeniden dene.`,
      onRetry: () => void staticQ.refetch(),
    }
    : data && loadedRows.length === 0 && (live || !staticQ.data?.degraded) ? (
      // v0.10.416 (B2) — canlı kuyruk ileri-yönlü: tamamlanmış bir trace
      // yeni satır üretmez; "backend'de kaydı yok" teşhisi burada yalan olurdu.
      filter.traceId && live ? {
        kind: 'empty',
        message: "Canlı kuyruk bu trace'e kilitli — yalnız YENİ satırlar akar. Trace'in geçmiş logları için canlı kuyruğu kapat; tamamlanmış bir trace yeni satır üretmez.",
      }
      // v0.10.420 — canlı kuyruk ileri-yönlü; "pencereyi genişlet" öğüdü
      // burada anlamsız (akış from/to taşımaz).
      : live ? {
        kind: 'empty',
        message: 'Canlı kuyruk açık — yeni satır bekleniyor. Yalnız akış açıldıktan sonra yazılan satırlar gelir; geçmişi görmek için canlı kuyruğu kapat.',
      }
      : filter.traceId ? {
        kind: 'empty',
        message: 'Bu trace için log kaydı yok',
        // "removing the span filter" kendini kaldıran bir eylem: tık spanId'yi
        // boşaltır, detail (açıklama) yerinde kalır ama bağlantı gider —
        // yuva yeniden bağlansın ki odak <body>'ye düşmesin.
        detailKey: filter.spanId ? 'span' : 'trace',
        detail: (
          <div style={{ color: 'var(--text2)' }}>
            The trace exists in Coremetry, but the logs backend has no
            record of it. Two common reasons:
            <ul style={{ marginTop: 8, paddingLeft: 18, lineHeight: 1.6, textAlign: 'left', display: 'inline-block' }}>
              <li>The application emitted no log lines while this trace was active.</li>
              <li>The log shipper (Filebeat / OTel Collector ES exporter / etc.) hadn't started yet when the trace ran, so the log was never indexed.</li>
            </ul>
            {filter.spanId && <div>You also filtered by span — try <a href="#" onClick={e => { e.preventDefault(); const next = { ...filter, spanId: '' }; setFilter(next); setDraft(d => ({ ...d, spanId: '' })); }}>removing the span filter</a> to see all logs for the trace.</div>}
          </div>
        ),
      }
      : {
        kind: 'empty',
        message: 'Log bulunamadı',
        detail: (
          <div style={{ color: 'var(--text2)' }}>
            Widen the time range, drop the service/cluster filter, or
            relax the severity floor. If unfiltered queries are also
            empty, the logs backend (<code>COREMETRY_LOGS_BACKEND</code>)
            may be misconfigured — check <Link to="/system/stats" style={{ color: 'var(--accent2)' }}>system stats</Link>.
          </div>
        ),
      }
    )
    // A local filter that hides everything must not read as "no logs
    // exist" — that state belongs to the query, not to this reading aid.
    // no-match yalnız YÜKLENEN satırlar varken (kaynak dolu, süzgeç eledi).
    : {
      kind: 'no-match',
      message: `Yüklenen ${loadedRows.length.toLocaleString()} satırın hiçbiri "${narrow.trim()}" içermiyor — bu süzgeç yalnız sayfadakilere bakar; tüm pencerede aramak için terimi yukarıdaki sorguya yaz.`,
      onClearFilters: () => setNarrow(''),
      returnFocusRef: narrowRef,
    };

  // Level facet chips (prototype LogsView .logbar/.facet/.lvl).
  // Each chip drives the EXISTING min-severity filter
  // (filter.severity) and carries a live count from the
  // per-severity timeseries query. Clicking a chip commits
  // its severity floor immediately + resets paging (facet =
  // a filter action, Kibana-style); clicking the active chip
  // (or All) returns to All severities. Active chip = accent
  // ring. The level label renders as the canonical severity
  // badge so the operator's colour memory carries over from
  // the table + histogram. (Replaces the old severity
  // <select> in the toolbar.)
  // v0.10.1501 (Discover dilim 3) — ayrı seviye satırı kalktı: çipler histogram
  // başlığının sol yuvasında (headerLeft), "N hits" ile yan yana.
  const levelChips = (() => {
    // The active chip = the highest-severity chip whose floor
    // is ≤ the current severity floor, so e.g. severity=17
    // lights ERROR, severity=9 lights INFO. 0 = All.
    const activeKey = filter.severity <= 0 ? 'all'
      : (LVL_FACETS.find(f => filter.severity >= f.min)?.key ?? 'all');
    const setSeverity = (min: number) => {
      const next = min === filter.severity ? 0 : min; // toggle off → All
      setFilter(f => ({ ...f, severity: next }));
      setDraft(d => ({ ...d, severity: next }));
      // v0.8.546 — the chip has to reach the URL like every other
      // filter does; without this the level lives only in memory and
      // Share copies a link that opens on All levels.
      writeUrl({ ...filter, severity: next }, filters);
      resetPaging();
    };
    // v0.10.924 — buton bütünlüğü Faz 2: elle boyanan
    // `span role=button` (chipBase/onStyle) yerine Chip atomu —
    // `active` aria-pressed'i de basıyor, Enter/Space yerel.
    // v0.10.925 — bileşen DEĞİL, render fonksiyonu: render içinde
    // tanımlanan bileşen her render'da yeni tiptir → çip yeniden
    // bağlanır ve klavyeyle seçince odak kaybolurdu.
    const levelChip = (keyName: string, count: number, title: string, children: ReactNode) => {
      const on = activeKey === keyName;
      return (
        <Chip key={keyName} pill active={on}
          title={title}
          onClick={() => setSeverity(keyName === 'all' ? 0 : LVL_FACETS.find(f => f.key === keyName)!.min)}>
          {children}
          <span style={{
            fontFamily: 'var(--font-mono)',
            fontSize: 10.5, color: on ? 'var(--accent2)' : 'var(--text3)',
          }}>
            {facetLoading ? '·' : count.toLocaleString()}
          </span>
        </Chip>
      );
    };
    return (
      <div role="group" aria-label="Filter by log level" className="ld-levels">
        {levelChip('all', facetCounts.all, 'Show all severities',
          <span style={{ color: activeKey === 'all' ? 'var(--accent2)' : 'var(--text2)' }}>All</span>)}
        {LVL_FACETS.map(f => levelChip(f.key, facetCounts[f.key] ?? 0,
          `Show ${f.label} and above (min severity ${f.min})`,
          <span className={`badge ${
            f.key === 'error' ? 'b-err'
            : f.key === 'warn' ? 'b-warn'
            : f.key === 'info' ? 'b-info'
            : 'b-gray'}`}>{f.label}</span>))}
        {!volumeEnabled && (
          <span className="ld-levels-note">
            counts need a bounded time range
          </span>
        )}
        {(chipsFromHistogram ? histTotals === null : volumeQ.isError) && (
          <span className="ld-levels-note is-err">
            level counts unavailable
          </span>
        )}
      </div>
    );
  })();

  // v0.10.1501 — "N hits": liste sorgusunun toplamı (ES track_total_hits
  // tavanında alt sınır → "10,000+"; CH'de gerçek count()). Pager'ın `count`
  // beyanıyla aynı kaynak. Desen görünümünde ve canlı kuyrukta liste koşmaz →
  // sayı yok (uydurulmaz).
  const hitsLabel = !live && staticQ.data && view === 'logs'
    ? `${total.toLocaleString()}${staticQ.data.totalIsLowerBound ? '+' : ''} hits`
    : '';

  return (
    <>
      {/* Changing the time range MUST reset the keyset cursor: a token
          from the old window encodes time < staleCursorTime, so paging
          into a new (wider/shifted) window with a stale cursor silently
          drops every row newer than it from page 1. resetPaging mirrors
          the apply/reset/search/URL-sync handlers. (v0.7.81 fix) */}
      <Topbar title="Logs" range={range} onRangeChange={(r) => { setRange(r); resetPaging(); }} envApplies />
      <PageShell>
        {/* v0.10.1498 (operatör onaylı taslak 2026-10-24, Kibana Discover) —
            sayfanın üst kısmı üç parça: (1) sorgu çubuğu (Service · Cluster ·
            KQL · Search/↻ + sağda + Filter ▾ · Live · Desenler · Saved ▾ ·
            Share ▾ · ⋯), (2) etkin süzgeçlerin TEK satırı (trace kilidi, desen,
            takım, with-trace, pill'ler, KQL ⓘ), (3) dürüstlük rozetleri.
            Durumun tamamı burada; üç bileşen saf sunum (pages/logs/). Eskiden
            Saved şeridi + Desenler/✨/Discover satırı + dört bant + pill şeridi +
            "Uygulanan arama" bandı tabloyu ekranın yarısına itiyordu. */}
        <LogsQueryBar
          draft={draft} onDraft={setDraft}
          clusters={clusters} autocompleteSince={autocompleteSince}
          fields={fields} fieldsTotal={fieldsTotal}
          onApply={apply} onRefresh={refresh} dirty={draftDirty} to={to}
          live={live} onToggleLive={toggleLive} liveGap={liveGap}
          addFilter={(
            <LogsAddFilterMenu
              traceIdDraft={draft.traceId} onTraceIdDraft={v => setDraft({ ...draft, traceId: v })}
              onApply={apply}
              hasTrace={filter.hasTrace} onToggleHasTrace={toggleHasTrace}
              myTeamName={teamGate ? '' : myTeamName} teamActive={!!filter.team}
              onApplyMyTeam={() => setTeam(myTeamName)}
              onAddPill={f => applyPills([...filters, f])}
              since={autocompleteSince} />
          )}
          menus={(
            <>
              <SavedViewsBar page="logs" variant="compact" />
              <LogsShareMenu kibanaHref={kibanaHref} />
              <LogsMoreMenu onReset={reset} />
            </>
          )} />
        <LogsFilterRow
          filter={filter}
          onClearTrace={clearTraceLock} onClearPattern={clearPattern} onToggleHasTrace={toggleHasTrace}
          myTeamName={myTeamName} teamGate={teamGate}
          teamPickerOpen={teamPickerOpen} teamDraft={teamDraft} onTeamDraft={setTeamDraft}
          onToggleTeamPicker={() => { setTeamDraft(filter.team); setTeamPickerOpen(o => !o); }}
          onSetTeam={t => { setTeam(t); setTeamPickerOpen(false); }}
          onClearTeam={clearTeam}
          filters={filters} editPill={editPill} onEditPill={setEditPill}
          onNegatePill={negatePill} onDisablePill={disablePill} onRemovePill={removePill}
          onReplacePill={(i, next) => applyPills(replaceFilterAt(filters, i, next))}
          onClearPills={() => applyPills([])}
          since={autocompleteSince}
          compiledSearch={compiledSearch}
          traceHint={traceHint} onUseTraceHint={applyTraceHint} />
        <LogsNotices live={live} env={env} team={filter.team} hasTrace={filter.hasTrace}
          data={staticQ.data} />

        {/* Fields panel (Discover revamp step 2) to the left of the
            histogram + table. Replaces the old "ƒ Fields" chip row —
            same discovery source (/api/logs/fields), but with
            Selected/Available grouping, per-field top-5 accordion
            (lazy fieldstats fetch on expand only), pill actions and
            add/remove-column. The right side keeps everything that
            was here before, unchanged. */}
        <div className="ld-body">
          <LogFieldsPanel
            fields={fields}
            fieldsTotal={fieldsTotal}
            types={fieldTypes}
            columns={logCols}
            scope={fieldStatsScope}
            onToggleColumn={toggleColumn}
            onPillAdd={addFromRow}
            onPillExclude={excludeFromRow}
            onExists={existsFromPanel}
            windowTotal={total} />
          <div className="ld-main">

        {/* v0.9.1248 — kalıcı link çözülemedi notu: sessiz düşme yok. */}
        {docMiss && (
          <div style={{ fontSize: 11, color: 'var(--warn)', marginBottom: 8, display: 'flex', alignItems: 'center', gap: 8 }}>
            <span>{docMiss === 'degraded'
              ? 'kalıcı doküman linki şimdilik çözülemedi — log backend yavaş/erişilemez, yeniden dene'
              : 'kalıcı doküman linki çözülemedi — kayıt pencere/retention dışında olabilir'}</span>
            <Button variant="secondary" size="sm" onClick={() => setDocMiss(false)}>×</Button>
          </div>
        )}
        {/* Severity-stacked histogram (v0.5.235) — spike of errors
            stands out against the background INFO traffic without
            reading the count column. Hidden when neither a time
            range nor a trace pin is set; renders nothing on
            empty data. */}
        {/* v0.9.431 — brush/çift-tık hook üzerinden; ns→sn çeviri burada
            (LogsHistogram sınır sözleşmesi ns, hook saniye alır). */}
        <LogsHistogram range={{ from, to }} filter={{ ...filter, env, search: compiledSearch }}
          onRangeSelect={(fromNs, toNs) => handleZoom(fromNs / 1e9, toNs / 1e9)}
          onZoomReset={handleZoomReset}
          breakdown={breakdown} onBreakdown={setBreakdown}
          onSeries={setHistTotals}
          onSeriesPick={addFromRow} /* v0.10.503 (B8) — lejant ⊕ → pill */
          spikeSubject={spikeSubject} /* v0.10.1284 — ✨ Artışı açıkla */
          paused={teamGate} /* v0.10.1477 — takım kararı beklenirken fetch yok */
          height={96} /* v0.10.1501 — Discover yoğunluğu; diğer çağıranlar 140 */
          headerLeft={(
            <>
              {hitsLabel && <span className="ld-hits" data-testid="logs-hits">{hitsLabel}</span>}
              {levelChips}
            </>
          )} />

        {/* v0.10.1499 (Discover dilim 2) — görünüm anahtarı: Logs | Desenler |
            Şablonlar, mevcut `?panel=` üzerinde (yeni param yok, replace:true).
            Desen görünümleri satır tablosunun YERİNE oturur ve liste sorgusu
            o arada KOŞMAZ (logsListEnabled); histogram + seviye çipleri her
            görünümde bağlamdır. Canlı kuyrukta desen görünümleri kapalı. */}
        <div className="ld-viewbar">
          <SegmentedControl<LogsView> aria-label="Logs görünümü" size="sm"
            value={view} onChange={setView}
            options={[
              { value: 'logs', label: 'Logs', title: 'Satırlar (tablo)' },
              { value: 'patterns', label: 'Desenler', disabled: live, title: live ? 'Canlı kuyrukta desen görünümü yok — kuyruğu durdur' : 'Penceredeki mesajlar imzaya göre gruplanır (örneklemeli)' },
              { value: 'templates', label: 'Şablonlar', disabled: live, title: live ? 'Canlı kuyrukta şablon görünümü yok — kuyruğu durdur' : "Drain templater'ın kalıcı şablonları (5 dk'da ≤1000 satır örneklenir)" },
            ]} />
        </div>
        {view !== 'logs' ? (
          <LogPatternsPanel embedded open={!teamGate} onSearch={searchFromPattern} tab={view}
            params={{ ...filter, env, search: compiledSearch, from: from ?? undefined, to: to ?? undefined }}
            toolbar={(
              /* v0.9.1100 (F3.5) — ✨ desen anlatımı; fetch YALNIZ tıkla (ES-maliyet disiplini). */
              <Button variant="secondary" size="sm" disabled={aiPat.busy}
                onClick={() => void explainPatterns()}
                title="Seçili süzgeç ve penceredeki log desenlerini AI anlatır; filo geneli yeni/patlayan desenler ve sürekli gürültü şablonları ayrıca etiketlenir">
                <IconSparkles /> Desenleri anlat
              </Button>
            )}
            aside={(
              <>
            {/* Faz 0.5 — ham metin yerine markdown. Bkz. Shift.tsx'teki aynı
                düzeltme; pre-wrap kalkıyor çünkü RenderedMarkdown kendi blok
                düzenini kuruyor. AŞAĞIDAKİ "Query failed" hata kutusundaki
                pre-wrap KALIYOR: o AI metni değil, ham backend hata gövdesi. */}
            {(aiPat.busy || aiPat.text || aiPat.err) && (
              <div style={{
                padding: '12px 14px', marginBottom: 10, borderRadius: 6,
                background: 'var(--bg2)', border: '1px solid var(--border)',
                fontSize: 12.5, lineHeight: 1.55,
              }}>
                <div style={{ fontSize: 10.5, fontWeight: 700, letterSpacing: 0.4, color: 'var(--text2)', marginBottom: 6 }}>
                  AI DESEN ANLATIMI
                </div>
                {aiPat.busy && <Spinner />}
                {aiPat.err && <span style={{ color: 'var(--err)' }}>{aiPat.err}</span>}
                {aiPat.text && <RenderedMarkdown text={aiPat.text} />}
              </div>
            )}
              </>
            )} />
        ) : (<>

        {/* v0.10.1502 (Discover dilim 4) — tablonun araç satırı: yerel daraltma
            (v0.9.294, yeni sorgu yok) · Columns ▾ · ⤶ sar · sıra · dışa aktarma ⋯.
            Sütun değişikliği tek yoldan (changeCols). */}
        <LogsTableToolbar
          loadedCount={data ? loadedRows.length : 0} shownCount={data ? logs.length : 0}
          narrow={narrow} onNarrow={setNarrow} narrowRef={narrowRef}
          columns={logCols} fields={fields} onColumns={changeCols}
          wrap={wrapLines} onToggleWrap={toggleWrap}
          live={live} asc={asc} onToggleAsc={toggleAsc}
          onExport={exportLoaded} />
        {/* v0.10.967 — tablo HER durumda (başlık kalır); durum satırı
            logsState'ten. Pager eskisi gibi yalnız satır varken. */}
        <LogTable logs={shownLogs} nav={tableNav} state={logsState}
          wrap={wrapLines}
          columns={logCols}
          onRemoveColumn={removeColumn}
          highlightTerms={highlightTerms}
          expandedIds={expanded}
          onToggleExpand={toggle}
          onFilterAdd={addFromRow}
          onFilterExclude={excludeFromRow}
          onToggleColumn={toggleColumn}
          onTracePeek={tid => setPeekTraceId(tid)}
          onContextOpen={l => setContextPivot(l)}
          permalink={l => buildDocPermalink(l, env)} />
        {/* Load more (v0.8.260 — replaced the Back/Next pager;
            keyset cursor mechanics unchanged underneath). Rows
            accumulate in accRows; the button advances the cursor
            to the response's nextCursor and the new page appends.
            Hidden during live tail (the live buffer owns its own
            moving window). Button-first per spec — an
            IntersectionObserver auto-load can layer on once the
            behaviour settles (and it would multiply backend
            queries, which the ES-usage constraint caps). */}
        {/* v0.9.1016 — paylaşılan sözleşme (v0.9.1014), cursor
            kipi. Kip bir görünüm tercihi DEĞİL veri modeli beyanı:
            bu sayfa keyset imleçle yürüyor, "sayfa 7'ye git"
            ifade edilemez (7'nin imleci ancak 6 çekilerek bilinir)
            ve satırlar BİRİKİYOR. v0.8.260'ın Back/Next'i
            "Load more" ile değiştirme kararı korunuyor —
            sözleşme onu geri almıyor, tarif ediyor.
            v0.9.288 — "of 10,000" ES tarafında YALANDI:
            track_total_hits 10.000'de duruyor (milyar-belge
            ölçeğinde her eşleşmeyi saymak tam da kaçındığın şey),
            ES relation "gte" dönüyor. Aynı etiket ClickHouse'ta
            gerçek bir count(). Backend hangisi olduğunu söylüyor;
            artık "+"yı `count` beyanı basıyor. */}
        {data && logs.length > 0 && !live && (
          <Pager mode="cursor"
            count={staticQ.data?.totalIsLowerBound ? 'capped' : 'exact'}
            total={total}
            loaded={logs.length}
            hasMore={!!data.nextCursor}
            onMore={() => { if (data.nextCursor) setCursor(data.nextCursor); }}
            loading={staticQ.isFetching}
            extras={
              /* v0.9.292 — the accumulation window slid forward. Rows
                 leaving the top without a word is the silent-loss
                 class this page keeps producing; say it and say the
                 remedy. */
              accDropped > 0 ? (
                <span style={{ color: 'var(--warn)' }}
                  title={`"Load more" keeps at most ${ACC_CAP.toLocaleString()} rows in the page so it stays responsive. ${accDropped.toLocaleString()} earlier (newer) rows have scrolled out of the buffer — narrow the time range or the filter to see a slice that fits.`}>
                  {accDropped.toLocaleString()} earlier rows dropped from the buffer
                </span>
              ) : undefined
            } />
        )}
        </>)}
          </div>
        </div>
      </PageShell>
      <CorrelationContextDrawer
        anchor={peekTraceId ? { kind: 'trace', traceId: peekTraceId } : null}
        onClose={() => setPeekTraceId(null)} />
      <LogContextModal pivot={contextPivot}
        highlightTerms={highlightTerms} search={compiledSearch || undefined}
        onClose={() => {
          setContextPivot(null);
          // v0.9.1248 — link-açılışlı modal kapanınca ?doc= temizlenir ki
          // sonraki gezinmelerde yeniden açılmasın; yabancı paramlar korunur.
          if (searchParams.get('doc')) {
            setSearchParams(prev => {
              const n = new URLSearchParams(prev);
              n.delete('doc'); n.delete('docsvc');
              return n;
            }, { replace: true });
          }
        }}
        onTracePeek={tid => { setContextPivot(null); setPeekTraceId(tid); }} />
    </>
  );
}

// LogRowR moved to components/LogTable.tsx (shared between
// /logs and the trace detail Logs tab).

export default function LogsPage() {
  return (
    <Suspense fallback={<Spinner />}>
      <LogsInner />
    </Suspense>
  );
}
