// coremetry/FilterBar/FilterChipEditor — v0.10.1854 (Log Search Faz 2 dilim
// 2.3; plan §6 "chip row + editor popover land in the Logs slice"). Tek çipin
// düzenleyicisi: alan · işleç · değer(ler). Değer önerisi YALNIZ çağıranın
// `suggestValues` (sunucu araması — asla katalog) ucundan; 180 ms gecikme +
// iptal sinyali (yeni tuş eski isteği keser). Saf sunum: çip dışarıda yaşar.
import { useEffect, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { FILTER_OPS, type FilterChip, type FilterOp } from '@/lib/filterChips';
import { CM_STRINGS } from '../strings';

export const FILTER_OP_LABELS: Record<FilterOp, string> = {
  '=': 'eşittir', '!=': 'eşit değil', '~': 'içerir', '!~': 'içermez',
  exists: 'var', '!exists': 'yok', '>=': '≥ (en az)', '<=': '≤ (en çok)',
  in: 'şunlardan biri', '!in': 'hiçbiri',
};
const SUGGEST_DEBOUNCE_MS = 180;
const SUGGEST_MAX = 20;

export interface FilterChipEditorProps {
  chip: FilterChip;
  /** Sunulan işleçler (varsayılan hepsi). */
  ops?: readonly FilterOp[];
  /** Sunucu araması (gecikmeli, iptal edilebilir) — asla tam katalog. */
  suggestValues?(key: string, q: string, signal: AbortSignal): Promise<string[]>;
  onApply(chip: FilterChip): void;
  onCancel(): void;
}

const isList = (op: FilterOp) => op === 'in' || op === '!in';
const noValue = (op: FilterOp) => op === 'exists' || op === '!exists';

export function FilterChipEditor({ chip, ops = FILTER_OPS, suggestValues, onApply, onCancel }: FilterChipEditorProps) {
  const [key, setKey] = useState(chip.key);
  const [op, setOp] = useState<FilterOp>(chip.op);
  const [val, setVal] = useState(isList(chip.op) ? (chip.values ?? []).join(', ') : (chip.value ?? ''));
  const [sugg, setSugg] = useState<string[]>([]);

  const lastToken = isList(op) ? (val.split(',').pop() ?? '').trim() : val.trim();
  useEffect(() => {
    const k = key.trim();
    if (!suggestValues || !k || noValue(op)) { setSugg([]); return; }
    const ac = new AbortController();
    const t = window.setTimeout(() => {
      suggestValues(k, lastToken, ac.signal)
        .then(v => { if (!ac.signal.aborted) setSugg(v.slice(0, SUGGEST_MAX)); })
        .catch(() => { if (!ac.signal.aborted) setSugg([]); });
    }, SUGGEST_DEBOUNCE_MS);
    return () => { ac.abort(); clearTimeout(t); };
  }, [key, op, lastToken, suggestValues]);

  const commit = () => {
    const k = key.trim();
    if (!k) return;
    const flags = { ...(chip.pinned ? { pinned: true } : {}), ...(chip.disabled ? { disabled: true } : {}) };
    if (noValue(op)) { onApply({ key: k, op, ...flags }); return; }
    if (isList(op)) {
      const values = val.split(',').map(v => v.trim()).filter(Boolean);
      onApply({ key: k, op, values, ...flags });
      return;
    }
    onApply({ key: k, op, value: val.trim(), ...flags });
  };
  const pick = (s: string) => setVal(v => {
    if (!isList(op)) return s;
    const parts = v.split(',').map(x => x.trim()).filter(Boolean);
    if (v.trim() !== '' && !v.trim().endsWith(',')) parts.pop(); // yazılan son parçayı tamamla
    return [...parts.filter(p => p !== s), s].join(', ');
  });

  return (
    <form className="grid w-72 gap-2 text-sm" aria-label="Süzgeci düzenle"
      onSubmit={e => { e.preventDefault(); commit(); }}>
      <Input className="h-8 font-mono" aria-label="Süzgeç alanı" placeholder="alan" value={key}
        onChange={e => setKey(e.target.value)} />
      <NativeSelect size="sm" aria-label="İşleç" value={op} className="w-full"
        onChange={e => setOp(e.target.value as FilterOp)}>
        {ops.map(o => <NativeSelectOption key={o} value={o}>{FILTER_OP_LABELS[o]}</NativeSelectOption>)}
      </NativeSelect>
      {!noValue(op) && (
        <Input className="h-8 font-mono" aria-label="Süzgeç değeri"
          placeholder={isList(op) ? 'değerler (virgülle)' : 'değer'} value={val}
          onChange={e => setVal(e.target.value)} />
      )}
      {sugg.length > 0 && !noValue(op) && (
        <div className="flex flex-wrap gap-1" role="group" aria-label={CM_STRINGS.suggestions}>
          {sugg.map(s => (
            <Button key={s} type="button" variant="outline" size="xs" className="max-w-full font-mono"
              title={s} onClick={() => pick(s)}><span className="truncate">{s}</span></Button>
          ))}
        </div>
      )}
      <div className="flex justify-end gap-1.5">
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>{CM_STRINGS.cancel}</Button>
        <Button type="submit" size="sm" disabled={!key.trim()}>Uygula</Button>
      </div>
    </form>
  );
}
