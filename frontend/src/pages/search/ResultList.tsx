// ResultList — v0.10.1335: /search sonuç listesi + kaynak durum şeridi.
//
// ≤120 satır (6 kaynak × 20): sanallaştırma gerekmez, satırlar CSS'te
// `content-visibility:auto` (.srch-row; tablo-standardı >100 satır kuralı).
// Klavye: ↑/↓ satırlar arasında odak + seçim (useTableNav'ın j/k/Enter'ı
// sayfada ayrıca kayıtlı), Enter odaklı OptionRow'u tıklar → çekmece.
// "Türe göre" gruplamada sıra çağırandan gelir (orderResults); başlık satırı
// gezinme indeksine GİRMEZ.
import type { KeyboardEvent, ReactNode } from 'react';
import type { SearchFacets, SearchResult, SearchResultType, SearchResponse } from '@/lib/types';
import { SEARCH_TYPES, type SearchGroup } from '@/lib/searchUrl';
import { ResultRow } from './ResultRow';
import { focusRow, sourceNote, typeCountLabel, type SourceNote } from './searchText';

export function ResultList({ rows, group, facets, terms, selected, t, openTarget, onOpen, onSelect, listRef }: {
  rows: SearchResult[];
  group: SearchGroup;
  facets: SearchFacets | undefined;
  terms: string[];
  selected: number;
  t: (k: string) => string;
  openTarget: (r: SearchResult) => { href: string; label: string } | null;
  onOpen: (r: SearchResult, idx: number) => void;
  onSelect: (idx: number) => void;
  listRef: React.RefObject<HTMLDivElement | null>;
}) {
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    const cur = (e.target as HTMLElement).closest('[data-row-idx]')?.getAttribute('data-row-idx');
    const from = cur == null ? -1 : Number(cur);
    const next = e.key === 'ArrowDown' ? Math.min(rows.length - 1, from + 1) : Math.max(0, from - 1);
    if (next < 0 || next === from) return;
    e.preventDefault();
    onSelect(next);
    focusRow(listRef.current, next);
  };

  const out: ReactNode[] = [];
  let lastType: SearchResultType | null = null;
  rows.forEach((r, i) => {
    if (group === 'type' && r.type !== lastType) {
      lastType = r.type;
      out.push(
        <div key={`h:${r.type}`} className="srch-group" role="presentation">
          <span>{t(`search.type.${r.type}`)}</span>
          <span className="srch-group-n">{typeCountLabel(facets?.type[r.type])}</span>
        </div>,
      );
    }
    const target = openTarget(r);
    out.push(
      <ResultRow key={`${r.type}:${r.id}:${i}`} r={r} idx={i} selected={i === selected} terms={terms}
        typeLabel={t(`search.type.${r.type}`)}
        openHref={target?.href ?? null} openLabel={target?.label ?? ''}
        onOpen={onOpen} />,
    );
  });

  return (
    <div className="srch-list" ref={listRef} onKeyDown={onKeyDown}>
      {out}
    </div>
  );
}

/** SourceStrip — kaynak başına dürüstlük şeridi. "okunamadı" (warn) ile
 *  "atlandı / not" (muted) ayrı tonda; hepsi ok ve notsuzsa hiç çizilmez. */
export function SourceStrip({ sources, t }: { sources: SearchResponse['sources'] | undefined; t: (k: string) => string }) {
  const notes: SourceNote[] = [];
  for (const ty of SEARCH_TYPES) {
    const st = sources?.[ty];
    if (!st) continue;
    const n = sourceNote(ty, st, t);
    if (n) notes.push(n);
  }
  if (notes.length === 0) return null;
  return (
    <ul className="srch-sources" aria-label="Source status">
      {notes.map(n => (
        <li key={n.type} className={n.tone === 'warn' ? 'srch-src is-warn' : 'srch-src'} data-src={n.type}>{n.text}</li>
      ))}
    </ul>
  );
}
