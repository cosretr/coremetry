import { useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { CATALOG } from './catalog';
import { ALL_CELLS, cellKey, parseCell, type Cell } from './cells';

// /design shadcn bölümü — v0.10.1619 (shadcn Faz 1 R10, plan §8.1).
// Her CATALOG anahtarı × hücre (tema × yoğunluk). Varsayılan: 9 hücre alt
// alta; `?cell=dark/compact` tek hücreye daraltır (replace:true — URL tek
// doğruluk kaynağı). Hücre `<div data-theme data-density>`: tokens.css
// shadcn değişkenlerini `[data-theme]`'de, globals.css koyu bloğu
// `[data-theme="dark"]`'ta da bildirir; yoğunluk kuralları torun seçicisi.
// Bilinen sınır: globals.css'in `[data-theme="light"] .x` gibi soy
// kuralları <html>'in temasından sızar (eski CSS); shadcn yüzeyi yalnız
// token okur, etkilenmez.

export function ShadcnCatalog() {
  const [sp, setSp] = useSearchParams();
  const raw = sp.get('cell');
  const only = useMemo(() => parseCell(raw), [raw]);
  const cells: readonly Cell[] = only ? [only] : ALL_CELLS;
  return (
    <section className="stack gap-4" aria-label="shadcn kataloğu">
      <div className="dc-row">
        <span className="dc-label">hücre</span>
        <NativeSelect size="sm" aria-label="Hücre" value={only ? cellKey(only) : ''}
          onChange={e => {
            const next = new URLSearchParams(sp);
            if (e.target.value) next.set('cell', e.target.value); else next.delete('cell');
            setSp(next, { replace: true });
          }}>
          <NativeSelectOption value="">hepsi (9)</NativeSelectOption>
          {ALL_CELLS.map(c => <NativeSelectOption key={cellKey(c)} value={cellKey(c)}>{cellKey(c)}</NativeSelectOption>)}
        </NativeSelect>
      </div>
      {cells.map(c => (
        <div key={cellKey(c)} className="dc-cell" data-theme={c.theme} data-density={c.density}>
          <h2 className="dc-cell-h">{cellKey(c)}</h2>
          {Object.entries(CATALOG).map(([name, render]) => (
            <div key={name} className="dc-entry">
              <h3 className="dc-entry-h">{name}</h3>
              {render({ cell: c, forceOpen: false })}
            </div>
          ))}
        </div>
      ))}
    </section>
  );
}
