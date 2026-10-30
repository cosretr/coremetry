import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useT } from '@/lib/i18n';
import { navHref } from '@/lib/navHref';
import { serviceHref } from '@/lib/serviceHref';
import { getRecentServices, getPinnedServices } from '@/lib/recentServices';
import { useAuth } from '@/components/AuthProvider';
import type { Action, ParamValues, SuggestItem } from '@/lib/actions';
import { toast } from '@/lib/toast';
import { localizePages, type PaletteResult } from '@/lib/palette/pages';
import { buildPaletteResults, pivotServiceRows } from '@/lib/palette/results';
import { groupPaletteResults } from '@/lib/palette/groups';
import { usePaletteServices } from '@/lib/palette/usePaletteServices';
import { usePaletteEndpoints, usePaletteOracleOps } from '@/lib/palette/usePaletteEndpoints';
import { promptModel, type PromptPick } from '@/lib/palette/prompt';
import { CommandPaletteView } from './CommandPaletteView';
import type { CommandPaletteGroup } from './CommandPalette';

// CommandPalette gövdesi — global Cmd-K / Ctrl-K spotlight (v0.5.162).
// v0.10.1653 (P1): tembel chunk; kısayol + açıcı + açık/kapalı kapıda
// (components/CommandPalette.tsx).
// v0.10.1658 (P2, plan §1.1-§1.2): cmdk üstünde, GRUPLU (Q1). Bu dosya yalnız
// durum + veri kablosu: sonuç listesi saf `buildPaletteResults` (sıra AYNEN,
// v0.10.126 varlıklar önce), gruplar saf `groupPaletteResults` (ardışık tür →
// başlık, yeniden sıralama yok), sunucu aramaları lib/palette hook'ları
// (200 ms debounce + stale guard, eager katalog YOK), eylem parametre kipi saf
// `promptModel` (ACTIONS 1:1, Q12). Çizim dondurulmuş `CommandPaletteProps`
// üstünden `CommandPaletteView` (cmdk + coremetry/overlay/Dialog). Esc:
// sonuç listesinde paleti kapatır (tek Esc yığını, escBridge); eylem kipinde
// listeye döner, ikinci Esc kapatır (Q3).

const SEARCH_PLACEHOLDER = 'Search or paste a trace id…';

export interface CommandPaletteBodyProps {
  open: boolean;
  onOpenChange(open: boolean): void;
  /** Her açılış (⌘K / görünür kapı) artar → geçici durum sıfırlanır. */
  openSeq: number;
  /** İlk mount: kapının tuş tamponunu boşaltır (gövde canlı) ve yazılanı döndürür. */
  takeBufferedQuery(): string;
}

export function CommandPaletteBody({ open, onOpenChange, openSeq, takeBufferedQuery }: CommandPaletteBodyProps) {
  const navigate = useNavigate();
  const t = useT();
  // v0.9.855 — pivot href'leri operatörün BAKTIĞI pencereyi taşısın diye
  // sayfanın aralığı (useUrlRange önceliğiyle) buradan çözülür.
  const { search: locationSearch } = useLocation();
  const { user } = useAuth();
  const [query, setQuery] = useState('');
  const services = usePaletteServices(query, open);
  const endpoints = usePaletteEndpoints(query, open, locationSearch);
  const oracleOps = usePaletteOracleOps(query, open, locationSearch);
  // v0.7.89 — pinned + recently-viewed services, refreshed each open.
  const [pivotSvcs, setPivotSvcs] = useState<PaletteResult[]>([]);
  // Param-prompt sub-mode (v0.5.457).
  const [activeAction, setActiveAction] = useState<Action | null>(null);
  const [paramIdx, setParamIdx] = useState(0);
  const [paramValues, setParamValues] = useState<ParamValues>({});
  const [running, setRunning] = useState(false);
  // id-suggest / duration süzme alt durumu (v0.5.459).
  const [suggestQuery, setSuggestQuery] = useState('');
  const [suggestResults, setSuggestResults] = useState<SuggestItem[]>([]);
  const [suggestLoading, setSuggestLoading] = useState(false);

  const backToList = () => {
    setActiveAction(null);
    setParamIdx(0);
    setParamValues({});
    setRunning(false);
    setSuggestQuery('');
    setSuggestResults([]);
    setSuggestLoading(false);
  };
  // Reset all transient state on every open so re-opening doesn't
  // resume mid-action from a previous session.
  const resetState = () => { setQuery(''); backToList(); };

  // Debounced id-suggest fetch (200ms + cancellation, v0.5.459).
  useEffect(() => {
    if (!activeAction) return;
    const cur = activeAction.params[paramIdx];
    if (!cur || cur.kind !== 'id-suggest' || !cur.suggest) return;
    const q = suggestQuery;
    let cancelled = false;
    setSuggestLoading(true);
    const tm = window.setTimeout(async () => {
      try {
        const rows = await cur.suggest!(q);
        if (!cancelled) setSuggestResults(rows);
      } catch {
        if (!cancelled) setSuggestResults([]);
      } finally {
        if (!cancelled) setSuggestLoading(false);
      }
    }, 200);
    return () => { cancelled = true; clearTimeout(tm); };
  }, [activeAction, paramIdx, suggestQuery]);

  // Kapı her açılışta openSeq'i artırır → geçici durum sıfırlanır; ilk
  // açılışta kapının tuş tamponu (gövde yüklenirken yazılanlar) sorguya düşer.
  const firstOpen = useRef(true);
  useEffect(() => {
    if (!open) return;
    resetState();
    if (firstOpen.current) {
      firstOpen.current = false;
      const q = takeBufferedQuery();
      if (q) setQuery(q);
    }
    // v0.7.89 — pivot rotasyonu her açılışta: ★ önce, sonra son bakılanlar.
    // PENCERESİZ, bilerek (v0.9.967/968): pencere katmanı navHref'te.
    setPivotSvcs(pivotServiceRows(getPinnedServices(), getRecentServices(), serviceHref));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- resetState yalnız setState çağırır (kimliği önemsiz)
  }, [open, openSeq, takeBufferedQuery]);

  // v0.9.1270 (B5#1) sayfa adı sidebar'la tek kaynaktan; v0.9.1272 rol süzgeci.
  const isAdmin = user?.role === 'admin';
  const localizedPages = useMemo(() => localizePages(t, isAdmin), [t, isAdmin]);
  const results = useMemo(() => buildPaletteResults({
    query, pages: localizedPages, services, endpoints, oracleOps, pivotSvcs,
    role: user?.role, seeAllLabel: t('search.seeAll'),
  }), [query, services, endpoints, oracleOps, pivotSvcs, user?.role, localizedPages, t]);

  const enterAction = (a: Action) => {
    setActiveAction(a);
    setParamIdx(0);
    setParamValues({});
    setSuggestQuery('');
    setSuggestResults([]);
  };

  const pick = (r: PaletteResult) => {
    if (r.kind === 'action' && r.action) { enterAction(r.action); return; }
    if (r.to) {
      navigate(navHref(r.to, locationSearch));
      onOpenChange(false);
    }
  };

  // runActionWithValues — değerleri açıkça okur (state flush beklemez).
  const runActionWithValues = async (values: ParamValues) => {
    if (!activeAction || running) return;
    setRunning(true);
    try {
      const msg = await activeAction.run(values);
      toast.success(msg);
      onOpenChange(false);
      resetState();
    } catch (e) {
      const m = e instanceof Error ? e.message : String(e);
      toast.error(`${activeAction.label} failed: ${m}`);
      setRunning(false);
    }
  };

  // advanceParam — değeri sakla, sonraki parametreye geç ya da çalıştır.
  const advanceParam = (paramName: string, value: string | SuggestItem | number) => {
    if (!activeAction) return;
    const next = { ...paramValues, [paramName]: value };
    setParamValues(next);
    if (paramIdx + 1 < activeAction.params.length) {
      setParamIdx(paramIdx + 1);
      setSuggestQuery('');
      setSuggestResults([]);
    } else {
      void runActionWithValues(next);
    }
  };

  const onPromptPick = (p: PromptPick) => {
    const cur = activeAction?.params[paramIdx];
    if (!cur || running) return;
    if (p.type === 'suggest') advanceParam(cur.name, p.item);
    else if (p.type === 'duration') advanceParam(cur.name, p.seconds);
    else {
      const raw = paramValues[cur.name];
      const val = typeof raw === 'string' ? raw.trim() : '';
      if (cur.required && !val) return;
      advanceParam(cur.name, val);
    }
  };

  // Q3 — eylem kipinde Esc listeye döner. Olay portal içinden React ağacında
  // buraya kabarır; preventDefault = "öğe Esc'i harcadı" → keyboard.ts yığına
  // dokunmaz (escBridge sözleşmesi), palet açık kalır.
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'Escape' || !activeAction || running) return;
    e.preventDefault();
    backToList();
  };

  let groups: CommandPaletteGroup[];
  let queryProp: { value: string; onChange(v: string): void; placeholder?: string };
  let loading = false;
  let empty: string | undefined;
  if (activeAction) {
    const m = promptModel({ action: activeAction, paramIdx, paramValues, suggestQuery, suggestResults, suggestLoading, running });
    const cur = activeAction.params[paramIdx];
    groups = [{ id: 'prompt', label: m.heading, items: m.rows.map(r => ({ id: r.id, label: r.label, hint: r.hint, run: () => onPromptPick(r.pick) })) }];
    queryProp = {
      value: m.inputValue,
      placeholder: m.placeholder,
      onChange: v => {
        if (cur?.kind === 'text') setParamValues({ ...paramValues, [cur.name]: v });
        else setSuggestQuery(v);
      },
    };
    loading = m.loading;
    // Satırsız kip (öneri yok / çalışıyor): başlık kaybolmasın diye boş iletide.
    empty = m.empty ? `${m.heading} · ${m.empty}` : undefined;
  } else {
    groups = groupPaletteResults(results).map(g => ({
      id: g.id,
      label: g.label,
      items: g.rows.map((r, i) => ({
        // Benzersiz: aynı hedef iki satırda görünebilir (cmdk değeri = seçim kimliği).
        id: `${g.id}#${i}:${r.kind}:${r.to ?? r.action?.id ?? ''}`,
        label: r.label,
        hint: r.hint,
        href: r.to,
        run: () => pick(r),
      })),
    }));
    queryProp = { value: query, onChange: setQuery, placeholder: SEARCH_PLACEHOLDER };
    empty = 'No matches.';
  }

  return (
    <div className="contents" onKeyDown={onKeyDown}>
      <CommandPaletteView open={open} onOpenChange={onOpenChange} groups={groups}
        query={queryProp} loading={loading} empty={empty} />
    </div>
  );
}
