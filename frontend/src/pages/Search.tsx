// /search — v0.10.1335: birleşik arama sayfası (spec R3+R4, operatör onaylı
// varsayılanlar). Backend v0.10.1333 GET /api/search: tek istekte altı kaynak
// (service → endpoint → problem → exception → trace → log), kaynak başına ≤20.
//
// URL TEK doğruluk kaynağı (lib/searchUrl): q/types/service/sev/status/sort/
// group/sel; env global EnvPicker paramı (useUrlEnv), range useUrlRange.
// Arama kutusu yerel taslak → 180 ms debounce → setSearchParams(replace).
// URL→taslak içe aktarımı İMZA bekçili (Logs.tsx urlSig emsali, v0.8.253):
// range/env/ai/sel yazımları yazılmakta olan taslağı silmez; kendi
// yazımlarımız imzayı önceden saklar, içe aktarma no-op olur.
//
// Durumlar: < 2 karakter · yükleniyor · hata · "“q” için sonuç yok" (süzgeç
// yok) ≠ "Mevcut filtrelerle sonuç yok · N gizli" (N yalnız önbellekteki
// süzgeçsiz cevaptan; ek istek yok) · kaynak şeridi ("Logs okunamadı
// (timeout)" ≠ "sonuç yok"). Detay çekmecesi ?sel= ile; "Ask CoSRE" ?ai=
// yazınca sel silinir ve çekmece çizilmez (iki odak tuzağı üst üste binmez).
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { Empty, Spinner } from '@/components/Spinner';
import { IconSearch } from '@/components/icons';
import { Chip, LinkButton, SearchField, SegmentedControl } from '@/components/ui';
import { PageControls } from '@/components/coremetry/PageControls';
import { PageShell } from '@/components/coremetry/PageShell';
import { useT } from '@/lib/i18n';
import { keys } from '@/lib/queries/keys';
import { useSearch, SEARCH_MIN_Q } from '@/lib/queries/search';
import {
  SEARCH_TYPES, decodeSearchUrl, hiddenByFilters, searchApiParams, searchUrlSig, writeSearchUrl,
  type SearchGroup, type SearchUrlState,
} from '@/lib/searchUrl';
import { removeRaw, STORAGE_KEYS } from '@/lib/storage';
import type { SearchQueryParams, SearchResponse, SearchResult, SearchSort } from '@/lib/types';
import { useTableNav } from '@/lib/useTableNav';
import { useUrlEnv } from '@/lib/useUrlEnv';
import { DEFAULT_RANGE_PRESET, useUrlRange } from '@/lib/useUrlRange';
import { timeRangeToNs } from '@/lib/utils';
import { FacetRail } from './search/FacetRail';
import { ResultList, SourceStrip } from './search/ResultList';
import { SearchDetailDrawer } from './search/SearchDetailDrawer';
import { resultOpenTarget } from './search/resultSubject';
import { fmt, focusRow, highlightTerms, orderResults } from './search/searchText';

export const SEARCH_DEBOUNCE_MS = 180;

export default function SearchPage() {
  const t = useT();
  const qc = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const [range, setRange] = useUrlRange(DEFAULT_RANGE_PRESET); // backend kaynak başına kırpar (trace metni ≤1 sa, şerit söyler)
  const [env, setEnv] = useUrlEnv();
  const state = useMemo(() => decodeSearchUrl(searchParams), [searchParams]);
  const aiRaw = searchParams.get('ai');

  // ── Taslak + imza bekçili içe aktarma ─────────────────────────────────
  const [draft, setDraft] = useState(state.q);
  const lastSigRef = useRef<string>(searchUrlSig(searchParams));
  useEffect(() => {
    const sig = searchUrlSig(searchParams);
    if (sig === lastSigRef.current) return;
    lastSigRef.current = sig;
    setDraft(decodeSearchUrl(searchParams).q);
  }, [searchParams]);

  const patch = useCallback((p: Partial<SearchUrlState>, also?: (next: URLSearchParams) => void) => {
    setSearchParams(prev => {
      const next = writeSearchUrl(prev, p);
      also?.(next);
      lastSigRef.current = searchUrlSig(next);
      return next;
    }, { replace: true });
  }, [setSearchParams]);

  const flushDraft = useCallback((v: string) => {
    if (v.trim() === state.q.trim()) return;
    patch({ q: v, sel: null });
  }, [patch, state.q]);

  useEffect(() => {
    if (draft.trim() === state.q.trim()) return;
    const h = setTimeout(() => flushDraft(draft), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(h);
  }, [draft, state.q, flushDraft]);

  // ── Sorgu ──────────────────────────────────────────────────────────────
  const win = useMemo(() => timeRangeToNs(range), [range]);
  const params = useMemo<SearchQueryParams>(() => searchApiParams(state, env, win), [state, env, win]);
  const query = useSearch(params);
  const tooShort = params.q.length < SEARCH_MIN_Q;
  const data = tooShort ? undefined : query.data;
  const rows = useMemo(() => orderResults(data?.results ?? [], state.group), [data, state.group]);
  const terms = useMemo(() => highlightTerms(params.q), [params.q]);

  // ── Seçim / çekmece ────────────────────────────────────────────────────
  const drawerOpen = !!state.sel && !aiRaw;
  const openRow = useCallback((r: SearchResult) => {
    patch({ sel: { type: r.type, id: r.id } });
  }, [patch]);
  const nav = useTableNav<SearchResult>(rows, {
    onOpen: r => openRow(r),
    enabled: !drawerOpen,
  });
  // "Ask CoSRE" ?ai= yazdı → sel'i sil (çekmece zaten çizilmiyor).
  useEffect(() => {
    if (!aiRaw || !state.sel) return;
    patch({ sel: null });
  }, [aiRaw, state.sel, patch]);
  const selRow = useMemo(() => {
    const s = state.sel;
    if (!s) return null;
    return (data?.results ?? []).find(r => r.type === s.type && r.id === s.id) ?? null;
  }, [state.sel, data]);

  // ── Süzgeç çipleri ─────────────────────────────────────────────────────
  const allTypes = state.types.length === SEARCH_TYPES.length;
  const chips: { key: string; label: string; remove: () => void }[] = [];
  if (!allTypes) chips.push({
    key: 'types', label: `${t('search.facet.type')}: ${state.types.map(ty => t(`search.type.${ty}`)).join(', ')}`,
    remove: () => patch({ types: [...SEARCH_TYPES], sel: null }),
  });
  if (state.service) chips.push({ key: 'service', label: `${t('search.facet.service')}: ${state.service}`, remove: () => patch({ service: '', sel: null }) });
  if (env) chips.push({ key: 'env', label: `${t('search.facet.env')}: ${env}`, remove: () => setEnv('') });
  if (state.sev) chips.push({ key: 'sev', label: `${t('search.facet.sev')} ${state.sev}`, remove: () => patch({ sev: '', sel: null }) });
  if (state.status === 'all') chips.push({ key: 'status', label: `${t('search.facet.status')}: ${t('search.status.all')}`, remove: () => patch({ status: 'open', sel: null }) });
  // Tek yazım: env de aynı güncellemede silinir (iki ardışık setSearchParams
  // bayat `prev` yüzünden birbirini ezerdi) + yapışkan env seçimi temizlenir.
  const clearAll = () => {
    if (env) removeRaw(STORAGE_KEYS.env);
    patch({ types: [...SEARCH_TYPES], service: '', sev: '', status: 'open', sel: null }, next => next.delete('env'));
  };

  const hidden = useMemo(() => {
    if (!data || data.results.length > 0) return null;
    const cached = qc.getQueriesData<SearchResponse>({ queryKey: [...keys.search.all, 'results'] })
      .map(([k, d]) => [k[2] as SearchQueryParams | undefined, d] as const);
    return hiddenByFilters(cached, params);
  }, [data, qc, params]);

  const listRef = useRef<HTMLDivElement>(null);
  const openTarget = useCallback((r: SearchResult) => {
    const tg = resultOpenTarget(r, { range, env, q: params.q });
    return tg ? { href: tg.href, label: t(tg.labelKey) } : null;
  }, [range, env, params.q, t]);

  let body: React.ReactNode;
  if (tooShort) {
    body = <Empty icon={<IconSearch size={28} />} title={t('search.minChars')}>{t('search.minCharsBody')}</Empty>;
  } else if (!data && query.isError) {
    body = <Empty icon={<IconSearch size={28} />} title={t('search.error')}>{(query.error as Error)?.message ?? ''}</Empty>;
  } else if (!data) {
    body = <Spinner label={t('search.loading')} />;
  } else if (data.results.length === 0 && chips.length > 0) {
    const title = t('search.hidden') + (hidden != null ? ` · ${fmt(t('search.hiddenN'), { n: hidden })}` : '');
    body = (
      <Empty icon={<IconSearch size={28} />} title={title}
        action={<LinkButton onClick={clearAll}>{t('search.clearAll')}</LinkButton>} />
    );
  } else if (data.results.length === 0) {
    body = <Empty icon={<IconSearch size={28} />} title={fmt(t('search.noMatch'), { q: params.q })}>{t('search.noMatchBody')}</Empty>;
  } else {
    body = (
      <ResultList rows={rows} group={state.group} facets={data.facets} terms={terms}
        selected={nav.selected} t={t} openTarget={openTarget} listRef={listRef}
        onSelect={i => nav.setSelected(i)}
        onOpen={(r, i) => { nav.setSelected(i); openRow(r); }} />
    );
  }

  return (
    <>
      <Topbar title={t('nav.search')} range={range} onRangeChange={setRange} envApplies />
      <PageShell>
        <PageControls sticky>
          <SearchField data-pc-lead data-shortcut-search hint="/" width="min(520px, 100%)"
            aria-label={t('nav.search')} placeholder={t('search.placeholder')}
            value={draft} onChange={setDraft}
            onKeyDown={e => {
              if (e.key === 'Enter') { e.preventDefault(); flushDraft(draft); }
              else if (e.key === 'ArrowDown' && rows.length > 0) {
                e.preventDefault();
                nav.setSelected(0);
                focusRow(listRef.current, 0);
              }
            }} />
          <SegmentedControl<SearchSort> size="sm" aria-label={t('search.sort')} value={state.sort}
            onChange={v => patch({ sort: v })}
            options={(['relevance', 'newest', 'oldest', 'severity'] as const).map(v => ({ value: v, label: t(`search.sort.${v}`) }))} />
          <SegmentedControl<SearchGroup> size="sm" aria-label={t('search.group')} value={state.group}
            onChange={v => patch({ group: v })}
            options={[
              { value: 'list', label: t('search.group.list') },
              { value: 'type', label: t('search.group.type') },
            ]} />
          {data && (
            <span className="srch-count" aria-live="polite">
              {fmt(t('search.results'), { n: data.total })}
              {query.isPlaceholderData && <span className="spinner sm" aria-hidden="true" />}
            </span>
          )}
        </PageControls>
        <div className="srch-layout">
          <FacetRail facets={data?.facets} state={state} env={env} t={t} onPatch={patch} onEnv={setEnv} />
          <section className="srch-col" aria-busy={query.isFetching || undefined}>
            {chips.length > 0 && (
              <div className="srch-chips">
                {chips.map(c => (
                  <Chip key={c.key} onRemove={c.remove} removeLabel={`Remove ${c.label}`}>{c.label}</Chip>
                ))}
                <LinkButton onClick={clearAll}>{t('search.clearAll')}</LinkButton>
              </div>
            )}
            {data && <SourceStrip sources={data.sources} t={t} />}
            {body}
          </section>
        </div>
        {drawerOpen && state.sel && (
          <SearchDetailDrawer sel={state.sel} row={selRow} range={range} env={env} q={params.q} win={win} t={t}
            onClose={() => patch({ sel: null })} />
        )}
      </PageShell>
    </>
  );
}
