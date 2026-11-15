// FilterQueryBox — v0.10.264 Traces "Add filter" yeniden tasarımı, Öneri A
// (mockup onayı 2026-09-02: tek satır sorgu kutusu; Datadog/Honeycomb faset
// araması). Yalnız Traces kullanır; Explore/Logs FilterBuilder'da kalır.
//
// Akış: yaz → anahtar önerileri (sayfanın penceresindeki gözlem sayısıyla
// sıralı) → Enter/Tab → op çipleri (varsayılan =) → Enter/Tab → değer
// önerileri (sayım + pay çubuğu, yazdıkça daralır, 200 ms debounce) →
// Enter çipi bitirir, kutu bir sonraki filtre için boşalır. Satır içi
// "anahtar op değer" (http.route=/x, channel_code in A,B) Enter ile tek
// adımda. Çip üç parçalı (anahtar · op · DEĞER), tık = düzenle, × = kaldır,
// Backspace boş kutuda son çipi düzenler, Esc vazgeçer.
//
// v0.10.2203 (operator-reported) — düzenleme YERİNDE: çipin anahtar / op /
// değerine tık o çipi kendi konumunda düzenleyiciye çevirir (çip iki kez
// görünmez; tıklanan adım açılır, metin seçili), öneri listesi çipin altına
// demirlenir. Enter çipi aynı konumda değiştirir (tek onChange), Esc ya da
// odak kaybı vazgeçer ve çipi aynen bırakır. Yeni filtre sondaki kutuda.
//
// Veri kaynakları FilterBuilder ile AYNI (useAttributeKeys + attributeValues,
// aynı pencere kuralı) — sözleşme (FilterExpr, upsert aynı k+op) değişmedi.
// Saf çekirdek lib/filterQuery.ts (testli); burada yalnız durum + DOM.

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { Chip, IconButton } from '@/components/ui';
import { api } from '@/lib/api';
import { metricLabelQ } from '@/lib/metricLabelQuery'; // v0.10.875
import { useEscLayer } from '@/lib/escLayer';
import { attrKeyWindowParams } from '@/lib/attrKeyWindow';
import { useAttributeKeys } from '@/lib/useAttributeKeys';
import { useUrlRange } from '@/lib/useUrlRange';
import { timeRangeToNs, fmtNum } from '@/lib/utils';
import type { FilterExpr, FilterOp } from '@/lib/types';
import { isHttpStatusKey, NO_HTTP_STATUS_TITLE } from '@/lib/httpStatusDisplay'; // v0.10.1519
import {
  FILTER_OPS, OP_NEEDS_VALUE, OP_SHORT, isListOp, opFromShorthand, parseInlineFilter, splitListValues,
  chipValueLabel, chipDisplay, filterKey, upsertFilter, pushRecent, parseRecent, rankKeys, withExtraKeys,
} from '@/lib/filterQuery';

type Step = 'key' | 'op' | 'value';

interface Draft {
  step: Step;
  k: string;
  op: FilterOp;
  text: string;
  /** Düzenlenen çipin indeksi (Backspace / tık ile açıldıysa). */
  editing?: number;
  /** v0.10.1160 — çipin op'una tıklandı: yalnız op değişir, değer korunur. */
  keep?: string[];
}

interface ValueRow { value: string; count: number }

export interface FilterQueryBoxProps {
  value: FilterExpr[];
  onChange: (next: FilterExpr[]) => void;
  /** Anahtar başına tohum değerler (örn. kind → server/client…). */
  suggestedValues?: Record<string, string[]>;
  /** Sabit hızlı çipler (status_code = error …). */
  quick?: FilterExpr[];
  /** localStorage anahtarı; verilmezse son kullanılanlar tutulmaz. */
  recentKey?: string;
  /** Test/kompozisyon: anahtar keşfini atla, bu listeyi kullan. */
  keyOptions?: string[];
  placeholder?: string;
  /** v0.10.270 — metrik modu (Explore metrik kaynağı): anahtarlar metrikte
   *  görülmüş etiketler (api.metricAttrKeys), değerler etiket uzayı
   *  (api.metricLabels; sayım yok → pay çubuğu çizilmez). FilterBuilder'ın
   *  metricName/metricService sözleşmesiyle aynı. */
  metricName?: string;
  metricService?: string;
  /** v0.10.1115 — span attribute'u olmayan, sunucunun tanıdığı sentetik
   *  anahtarlar (op_group); span kipinde öneri listesinin sonuna eklenir. */
  extraKeys?: readonly string[];
  /** v0.10.1336 — rehberli tur çapaları: etkin çip barı (`.fq-bar`) ve hızlı
   *  çip satırı (`.fq-quick`). Kimlikler çağıranda literal (TOUR_ANCHORS). */
  tourBar?: string;
  tourQuick?: string;
}

// v0.10.1424 — hint/recent counts use the shared fmtNum (K/M/B/T); the local
// K/M copy is gone.

function loadRecent(key?: string): FilterExpr[] {
  if (!key) return [];
  try { return parseRecent(localStorage.getItem(key)); } catch { return []; }
}

export function FilterQueryBox({ value, onChange, suggestedValues, quick = [], recentKey, keyOptions, placeholder, metricName, metricService, extraKeys, tourBar, tourQuick }: FilterQueryBoxProps) {
  const metricMode = !!metricName?.trim();
  const [metricKeys, setMetricKeys] = useState<string[]>([]);
  useEffect(() => {
    if (!metricMode) { setMetricKeys([]); return; }
    let cancelled = false;
    api.metricAttrKeys(metricName!.trim(), metricService ?? '')
      .then(keys => { if (!cancelled) setMetricKeys(keys ?? []); })
      .catch(() => { if (!cancelled) setMetricKeys([]); });
    return () => { cancelled = true; };
  }, [metricMode, metricName, metricService]);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [hi, setHi] = useState(0);
  const [liveValues, setLiveValues] = useState<ValueRow[]>([]);
  const [liveLoading, setLiveLoading] = useState(false);
  const [recent, setRecent] = useState<FilterExpr[]>(() => loadRecent(recentKey));
  const inputRef = useRef<HTMLInputElement>(null);
  // v0.10.2203 — öneri listesinin demir noktası (düzenlenen çipin sol kenarı).
  const fqRef = useRef<HTMLDivElement>(null);
  const editChipRef = useRef<HTMLSpanElement>(null);
  const [popLeft, setPopLeft] = useState(0);
  const listId = 'fq-list';

  const [range] = useUrlRange();
  const rangeNs = useMemo(() => timeRangeToNs(range), [range]);
  const attrWindow = useMemo(() => attrKeyWindowParams(range), [range]);
  const { keys: spanKeys, observed } = useAttributeKeys(value, attrWindow, !keyOptions && !metricMode);
  const allKeys = useMemo(
    () => keyOptions ?? (metricMode ? metricKeys : withExtraKeys(spanKeys, extraKeys)),
    [keyOptions, metricMode, metricKeys, spanKeys, extraKeys],
  );
  const hints = useMemo(() => {
    const m: Record<string, number> = {};
    for (const o of observed) m[o.key] = o.count;
    return m;
  }, [observed]);

  useEscLayer(draft !== null, () => setDraft(null));

  // Değer önerileri: anahtar + yazılan metin (200 ms debounce), tohum + canlı.
  const typed = draft?.step === 'value' ? draft.text.trim() : '';
  const draftKey = draft?.step === 'value' ? draft.k : '';
  useEffect(() => {
    if (!draftKey) { setLiveValues([]); return; }
    let cancelled = false;
    setLiveLoading(true);
    const handle = setTimeout(() => {
      const fetch: Promise<ValueRow[]> = metricMode
        ? api.metricLabels(metricName!.trim(), draftKey, '24h', metricLabelQ(typed)).then(vals => { // v0.10.875 — 868 bu siteyi kaçırmıştı; ≥3 karakter rungu
            const t = typed.toLowerCase();
            return (vals ?? []).filter(v => !t || v.toLowerCase().includes(t)).map(v => ({ value: v, count: 0 }));
          })
        : api.attributeValues(draftKey, '1h', 200, typed || undefined, rangeNs)
            .then(rows => (rows ?? []).map(r => ({ value: r.value, count: r.count })));
      fetch
        .then(rows => { if (!cancelled) setLiveValues(rows); })
        .catch(() => { if (!cancelled) setLiveValues([]); })
        .finally(() => { if (!cancelled) setLiveLoading(false); });
    }, 200);
    return () => { cancelled = true; clearTimeout(handle); };
  }, [draftKey, typed, rangeNs, metricMode, metricName]);

  const valueOptions = useMemo<ValueRow[]>(() => {
    if (!draft || draft.step !== 'value') return [];
    const seed = (suggestedValues?.[draft.k] ?? []).filter(v => !typed || v.toLowerCase().includes(typed.toLowerCase()));
    const seen = new Set<string>();
    const out: ValueRow[] = [];
    for (const r of [...liveValues, ...seed.map(v => ({ value: v, count: 0 }))]) {
      if (seen.has(r.value)) continue;
      seen.add(r.value);
      out.push(r);
    }
    return out.slice(0, 12);
  }, [draft, liveValues, suggestedValues, typed]);
  const maxCount = useMemo(() => valueOptions.reduce((m, r) => Math.max(m, r.count), 0), [valueOptions]);

  const keyMatches = useMemo(
    () => (draft && draft.step === 'key' ? rankKeys(allKeys, draft.text, hints) : []),
    [draft, allKeys, hints],
  );
  const opMatches = useMemo<FilterOp[]>(() => {
    if (!draft || draft.step !== 'op') return [];
    const t = draft.text.trim();
    if (!t) return FILTER_OPS;
    const exact = opFromShorthand(t);
    return exact ? [exact, ...FILTER_OPS.filter(o => o !== exact)] : FILTER_OPS.filter(o => o.toLowerCase().startsWith(t.toLowerCase()));
  }, [draft]);

  useEffect(() => { setHi(0); }, [draft?.step, draft?.text]);

  // v0.10.2203 — yerinde düzenleyici açılınca odak + metin seçili; liste
  // düzenlenen çipin altına (kapsayıcıdan taşmadan).
  const editingIdx = draft?.editing;
  const refocus = useRef(false);
  useEffect(() => {
    if (editingIdx === undefined) {
      if (refocus.current) inputRef.current?.focus();
      refocus.current = false;
      return;
    }
    inputRef.current?.focus();
    inputRef.current?.select();
  }, [editingIdx, draft?.step]);
  useLayoutEffect(() => {
    const chip = editChipRef.current, box = fqRef.current;
    if (editingIdx === undefined || !chip || !box) { setPopLeft(0); return; }
    setPopLeft(Math.max(0, Math.min(chip.offsetLeft, box.clientWidth - 640)));
  }, [editingIdx, value.length]);

  const commit = useCallback((expr: FilterExpr, editing?: number) => {
    let next: FilterExpr[];
    if (editing !== undefined && editing >= 0 && editing < value.length) {
      next = value.map((f, i) => (i === editing ? expr : f));
    } else {
      next = upsertFilter(value, expr);
    }
    onChange(next);
    if (recentKey) {
      const r = pushRecent(recent, expr);
      setRecent(r);
      try { localStorage.setItem(recentKey, JSON.stringify(r)); } catch { /* private mode */ }
    }
    setDraft(null);
    inputRef.current?.focus();
    refocus.current = true; // v0.10.2203 — girdi çipten sona taşındıysa yeni düğüm de odak alsın
  }, [value, onChange, recentKey, recent]);

  const removeAt = (i: number) => onChange(value.filter((_, j) => j !== i));

  const editChip = (i: number) => {
    const f = value[i];
    if (!f) return;
    setDraft({ step: OP_NEEDS_VALUE[f.op] ? 'value' : 'op', k: f.k, op: f.op, text: chipValueLabel(f), editing: i });
  };

  // v0.10.2203 — anahtara tık: anahtar adımı (metin seçili); Enter yeni
  // anahtarı çipin op + değeriyle aynı konuma yazar.
  const editChipKey = (i: number) => {
    const f = value[i];
    if (!f) return;
    setDraft({ step: 'key', k: f.k, op: f.op, text: f.k, editing: i, keep: f.v });
  };

  // v0.10.1160 — Operator-reported: op'u değiştirmek için çipi kaldırıp yeniden
  // eklemek gerekiyordu (her tık değer adımını açıyordu). Op'a tık op listesini
  // açar; seçilen op çipin değeriyle yerinde yazılır.
  const editChipOp = (i: number) => {
    const f = value[i];
    if (!f) return;
    setDraft({ step: 'op', k: f.k, op: f.op, text: '', editing: i, keep: f.v });
  };

  const pickOp = (d: Draft, op: FilterOp) => {
    if (!OP_NEEDS_VALUE[op]) { commit({ k: d.k, op, v: [] }, d.editing); return; }
    const keep = d.keep ?? [];
    const isList = isListOp(op);
    if (keep.length > 0 && (isList || keep.length === 1)) { commit({ k: d.k, op, v: keep }, d.editing); return; }
    // Değer yok (∃ → =) ya da liste tek değerli op'a iniyor: değer adımı, eldeki metinle.
    setDraft({ ...d, step: 'value', op, text: keep.join(', '), keep: undefined });
  };

  const advance = () => {
    if (!draft) return;
    if (draft.step === 'key') {
      const inline = parseInlineFilter(draft.text);
      if (inline) { commit(inline, draft.editing); return; }
      const k = keyMatches[hi] ?? draft.text.trim();
      if (!k) return;
      // v0.10.2203 — çipin anahtarı düzenleniyorsa op + değer korunur, çip yerinde değişir.
      if (draft.editing !== undefined && draft.keep) { commit({ k, op: draft.op, v: draft.keep }, draft.editing); return; }
      setDraft({ ...draft, step: 'op', k, text: '' });
      return;
    }
    if (draft.step === 'op') {
      pickOp(draft, opMatches[hi] ?? opFromShorthand(draft.text) ?? '=');
      return;
    }
    // value
    const picked = valueOptions[hi]?.value;
    const raw = draft.text.trim() || picked || '';
    if (!raw) return;
    const isList = isListOp(draft.op);
    const v = isList ? splitListValues(raw) : [draft.text.trim() ? raw : picked ?? raw];
    if (v.length === 0) return;
    commit({ k: draft.k, op: draft.op, v }, draft.editing);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    const listLen = draft?.step === 'key' ? keyMatches.length : draft?.step === 'op' ? opMatches.length : valueOptions.length;
    if (e.key === 'ArrowDown' && listLen > 0) { e.preventDefault(); setHi(h => (h + 1) % listLen); return; }
    if (e.key === 'ArrowUp' && listLen > 0) { e.preventDefault(); setHi(h => (h - 1 + listLen) % listLen); return; }
    if (e.key === 'Enter') { e.preventDefault(); if (draft) advance(); return; }
    if (e.key === 'Tab' && draft) {
      e.preventDefault();
      if (draft.step === 'value') {
        const picked = valueOptions[hi]?.value;
        if (picked) setDraft({ ...draft, text: picked });
      } else {
        advance();
      }
      return;
    }
    if (e.key === 'Backspace' && (!draft || draft.text === '')) {
      if (draft && draft.step === 'value') { e.preventDefault(); setDraft({ ...draft, step: 'op', text: '' }); return; }
      if (draft && draft.step === 'op') { e.preventDefault(); setDraft({ ...draft, step: 'key', text: draft.k }); return; }
      if (!draft && value.length > 0) {
        e.preventDefault();
        const i = value.length - 1;
        const f = value[i];
        onChange(value.slice(0, i));
        setDraft({ step: OP_NEEDS_VALUE[f.op] ? 'value' : 'op', k: f.k, op: f.op, text: chipValueLabel(f) });
      }
    }
  };

  const onText = (t: string) => {
    if (!draft) {
      if (!t) return;
      setDraft({ step: 'key', k: '', op: '=', text: t });
      return;
    }
    setDraft({ ...draft, text: t });
  };

  const editing = draft?.editing !== undefined;
  const inputPlaceholder = !draft ? (placeholder ?? 'Filtre: anahtar yaz ya da http.route=/x')
    : draft.step === 'key' ? 'anahtar…' : draft.step === 'op' ? 'op (= ≠ ~ IN > < ∃)…'
      : isListOp(draft.op) ? 'değerler, virgülle…' : 'değer…';

  // v0.10.2203 — TEK girdi: düzenleme yokken çip barının sonunda, bir çip
  // düzenlenirken o çipin içinde (aynı combobox; ikinci girdi yok). Odak
  // kaybı yalnız yerinde düzenlemeyi iptal eder (çip aynen kalır).
  const editingAt = draft?.editing;
  const inputEl = (
    <input ref={inputRef} className={editing ? 'fq-input fq-edit' : 'fq-input'} value={draft?.text ?? ''} placeholder={inputPlaceholder}
      aria-label={editing ? `Filtreyi düzenle: ${value[editingAt ?? -1]?.k ?? ''}` : 'Filtre'} aria-autocomplete="list"
      aria-controls={draft ? listId : undefined} aria-activedescendant={draft ? `${listId}-${hi}` : undefined}
      size={editing ? Math.max(4, (draft?.text.length ?? 0) + 1) : undefined}
      onChange={e => onText(e.target.value)} onKeyDown={onKeyDown}
      onBlur={editing ? () => setDraft(d => (d && d.editing === editingAt ? null : d)) : undefined}
      autoComplete="off" spellCheck={false} />
  );

  return (
    <div className="fq" ref={fqRef}>
      <div className={draft ? 'fq-bar is-open' : 'fq-bar'} role="combobox" aria-expanded={draft !== null} aria-haspopup="listbox"
        aria-owns={draft ? listId : undefined} onClick={() => inputRef.current?.focus()} data-tour={tourBar}>
        <span className="fq-lens" aria-hidden>⌕</span>
        {value.map((f, i) => { const d = chipDisplay(f); if (draft && draft.editing === i) return (
          // v0.10.2203 — düzenlenen çip KENDİ konumunda düzenleyici olur (ikinci çip yok).
          <span key={`${filterKey(f)}#${i}`} ref={editChipRef} className="fq-chip is-editing" onClick={e => e.stopPropagation()}>
            {draft.step !== 'key' && <span className="fq-k">{draft.k}</span>}
            {draft.step === 'value' && <span className="fq-o mono">{OP_SHORT[draft.op]}</span>}
            {inputEl}
          </span>
        ); return (
          <span key={`${filterKey(f)}#${i}`} className="fq-chip"
            title={`${f.k} ${f.op} ${chipValueLabel(f)} — tık: düzenle`}>
            {/* v0.10.1093 — görünen metin chipDisplay'den (db_stmt_hash →
                "statement #…"); title ve düzenleme ham değeri taşır. */}
            <span className="fq-k" onClick={e => { e.stopPropagation(); editChipKey(i); }}>{d.key}</span>
            {d.op && <span className="fq-o mono" title="Operatörü değiştir" onClick={e => { e.stopPropagation(); editChipOp(i); }}>{d.op}</span>}
            {OP_NEEDS_VALUE[f.op] && (
              <span className="fq-v" onClick={e => { e.stopPropagation(); editChip(i); }}>
                {d.value}
                {isListOp(f.op) && f.v.length > 1 && <span className="fq-n">{f.v.length}</span>}
              </span>
            )}
            {/* v0.10.924 — buton bütünlüğü Faz 2: IconButton. `fq-x` birleşik
                çipin ayraç segmenti; `height: auto` onu çipin iç yüksekliğine
                gerer (`.fq-chip` align-items: stretch — sabit 20px kare
                ayraçta 2px çentik bırakırdı). */}
            <IconButton variant="ghost" size="xs" className="fq-x" style={{ height: 'auto' }}
              aria-label={`Filtreyi kaldır: ${f.k}`} onClick={e => { e.stopPropagation(); removeAt(i); }}
              icon="✕" />
          </span>
        ); })}
        {draft && draft.editing === undefined && draft.step !== 'key' && (
          <span className="fq-chip is-draft" aria-live="polite">
            <span className="fq-k">{draft.k}</span>
            <span className="fq-o mono">{draft.step === 'value' ? OP_SHORT[draft.op] : '…'}</span>
          </span>
        )}
        {!editing && inputEl}
        <span className="fq-hint" aria-hidden>
          <kbd>↑</kbd><kbd>↓</kbd> seç · <kbd>Tab</kbd> adım · <kbd>Enter</kbd> ekle · <kbd>Esc</kbd> vazgeç
        </span>
      </div>
      {draft && (
        <div className="fq-pop" style={editing && popLeft > 0 ? { left: popLeft } : undefined}>
          <div className="fq-steps">
            <div className={draft.step === 'key' ? 'on' : 'done'}>1 · Anahtar{draft.step !== 'key' && <span className="mono">{draft.k}</span>}</div>
            <div className={draft.step === 'op' ? 'on' : draft.step === 'value' ? 'done' : ''}>2 · Op{draft.step === 'value' && <span className="mono">{OP_SHORT[draft.op]}</span>}</div>
            <div className={draft.step === 'value' ? 'on' : ''}>3 · Değer</div>
          </div>
          <div className="fq-list" role="listbox" id={listId}>
            {draft.step === 'key' && (
              keyMatches.length === 0
                ? <div className="fq-empty">{draft.text ? 'Eşleşen anahtar yok — Enter ile olduğu gibi kullan' : 'Anahtar yazmaya başla'}</div>
                : keyMatches.map((k, i) => (
                  <div key={k} id={`${listId}-${i}`} role="option" aria-selected={i === hi} className={i === hi ? 'fq-opt sel' : 'fq-opt'}
                    onMouseEnter={() => setHi(i)} onMouseDown={e => {
                      e.preventDefault(); setHi(i);
                      if (draft.editing !== undefined && draft.keep) { commit({ k, op: draft.op, v: draft.keep }, draft.editing); return; } // v0.10.2203
                      setDraft({ ...draft, step: 'op', k, text: '' });
                    }}>
                    <span className="name mono">{k}</span>
                    {hints[k] ? <span className="cnt">{fmtNum(hints[k])} span</span> : null}
                  </div>
                ))
            )}
            {draft.step === 'op' && (
              <div className="fq-ops" role="presentation">
                {/* v0.10.927 — op seçeneği anahtar/değer kardeşleriyle AYNI desen:
                    role=option <div> (odak girdide, aria-activedescendant yönetir;
                    onMouseDown preventDefault odağı çalmaz). Ham düğme değil. */}
                {opMatches.map((op, i) => (
                  <div key={op} id={`${listId}-${i}`} role="option" aria-selected={i === hi}
                    className={i === hi ? 'fq-op sel mono' : 'fq-op mono'} title={op}
                    onMouseEnter={() => setHi(i)}
                    onMouseDown={e => { e.preventDefault(); setHi(i); pickOp(draft, op); }}>
                    {OP_SHORT[op]}{OP_SHORT[op] !== op ? <span className="fq-oplong"> {op}</span> : null}
                  </div>
                ))}
              </div>
            )}
            {draft.step === 'value' && (
              <>
                <div className="fq-grp">{liveLoading ? 'değerler yükleniyor…' : valueOptions.length ? `bu pencerede görülen değerler${typed ? ` — "${typed}" ile eşleşen` : ''}` : (typed ? 'öneri yok — Enter ile yazdığını ekle' : 'değer yaz')}</div>
                {valueOptions.map((r, i) => (
                  <div key={r.value} id={`${listId}-${i}`} role="option" aria-selected={i === hi} className={i === hi ? 'fq-opt sel' : 'fq-opt'}
                    onMouseEnter={() => setHi(i)}
                    onMouseDown={e => { e.preventDefault(); const isList = isListOp(draft.op); commit({ k: draft.k, op: draft.op, v: isList ? splitListValues(r.value) : [r.value] }, draft.editing); }}>
                    {/* v0.10.1519 — http.status_code = 0: durumsuz span'ler (değer "0" aynen işlenir). */}
                    {isHttpStatusKey(draft.k) && r.value === '0'
                      ? <span className="name mono" title={NO_HTTP_STATUS_TITLE}>0 — {NO_HTTP_STATUS_TITLE}</span>
                      : <span className="name mono">{r.value || '(boş)'}</span>}
                    {r.count > 0 && (
                      <span className="cnt bar">
                        <i style={{ width: `${Math.max(2, Math.round((r.count / (maxCount || 1)) * 72))}px` }} />
                        {fmtNum(r.count)}
                      </span>
                    )}
                  </div>
                ))}
              </>
            )}
          </div>
          <div className="fq-foot">
            <span><b>Backspace</b> boş kutuda son çip / önceki adım</span>
            <span><b>Satır içi</b> anahtar=değer · anahtar in a,b · anahtar exists</span>
          </div>
        </div>
      )}
      {(quick.length > 0 || recent.length > 0) && !draft && (
        <div className="fq-quick" data-tour={tourQuick}>
          <span className="fq-quick-label">Hızlı:</span>
          {quick.map(f => (
            <Chip key={`q:${filterKey(f)}`} size="xs" className="mono" onClick={() => commit(f)} title="Filtre olarak ekle">
              {f.k} {OP_SHORT[f.op]} {chipValueLabel(f)}
            </Chip>
          ))}
          {recent.filter(r => !quick.some(q => filterKey(q) === filterKey(r))).map(f => (
            <Chip key={`r:${filterKey(f)}`} size="xs" className="mono" onClick={() => commit(f)} title="Son kullanılan (bu tarayıcı)">
              ⟲ {f.k} {OP_SHORT[f.op]} {chipValueLabel(f)}
            </Chip>
          ))}
        </div>
      )}
    </div>
  );
}
