import { useEffect, useMemo, useRef, useState } from 'react';
import { Input } from '@/components/shadcn/input';
import { Button } from '@/components/shadcn/button';
import { FIELD_SURFACE } from '@/components/coremetry/Field/fieldSurface';

// NamespaceCombobox — namespace typeahead (v0.9.34, design handoff
// "Namespace typeahead"). Native <select> DEĞİL (yüzlerce seçenek):
// focus dropdown'ı açar, "N of M" başlık, büyük-küçük duyarsız
// substring filtre, ~80 render-cap + "+K more — refine search",
// "All namespaces" reset, ✕ seçili olanı temizler. Dışarı tık/blur
// kapatır (ColumnManager v0.8.76 anchor dersi: left:0, içeriğe açılır).
const RENDER_CAP = 80;

export function NamespaceCombobox({ namespaces, value, onPick, onClear }: {
  namespaces: string[];
  value: string;
  onPick: (ns: string) => void;
  onClear: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, [open]);

  const q = query.trim().toLowerCase();
  const filtered = useMemo(
    () => (q ? namespaces.filter(n => n.toLowerCase().includes(q)) : namespaces),
    [namespaces, q]);
  const shown = filtered.slice(0, RENDER_CAP);
  const overflow = filtered.length - shown.length;

  const pick = (ns: string) => { onPick(ns); setOpen(false); setQuery(''); };

  // Clusters S5 (v0.10.2170, plan §0 D4) — shadcn giydirmesi: ham <input>
  // → shadcn Input, ui/IconButton → shadcn Button, satır içi stil + JS hover
  // → .cl-ns sınıfları. Davranış (odakta açılır, N of M, ~80 render tavanı,
  // All namespaces, ✕) aynı.
  // D4 İSTİSNASI (picker kuralı): liste lazy/sunucu aramalı DEĞİL, eager —
  // gerekçe: seçenekler cluster'ın tam namespace kümesi, Namespaces sorgusuyla
  // zaten çekilmiş (ek fetch yok) ve ≤ birkaç yüz; istemci süzgeci yeter.
  return (
    <div ref={ref} className="cl-ns">
      <Input
        className={`cl-ns__input ${FIELD_SURFACE}`}
        value={open ? query : value}
        onFocus={() => setOpen(true)}
        onChange={e => { setQuery(e.target.value); if (!open) setOpen(true); }}
        aria-label="Filter namespaces"
        placeholder={value || 'Filter namespaces…'} />
      {/* v0.10.924 — ✕ girdinin yanında akar (eski yerleşim korunur). */}
      {value && !open && (
        <Button variant="ghost" size="icon-xs" onClick={onClear}
          aria-label="Clear namespace" title="Clear namespace">✕</Button>
      )}
      {open && (
        <div className="cl-ns__pop">
          <div className="cl-ns__head">
            {filtered.length} of {namespaces.length} namespaces
          </div>
          <div className="cl-ns__list">
            <div className="cl-ns__opt cl-ns__opt--all" onClick={() => { onClear(); setOpen(false); setQuery(''); }}>
              All namespaces
            </div>
            {shown.map(n => (
              <div key={n} onClick={() => pick(n)}
                className={`mono cl-ns__opt${n === value ? ' is-sel' : ''}`}>
                {n}
              </div>
            ))}
            {overflow > 0 && (
              <div className="cl-ns__note">
                +{overflow} more — refine search
              </div>
            )}
            {filtered.length === 0 && (
              <div className="cl-ns__note">
                No namespace matches “{query}”.
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
