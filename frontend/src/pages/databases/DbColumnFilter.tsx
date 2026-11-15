// DbColumnFilter — v0.10.2205 (operatör: Databases "Called from services"
// tablosuna /traces'in başlık hunisi). Kabuk /traces'in ColumnFilterShell'i
// (aynı tetik, etkin işaret, Temizle; gövde yalnız açıkken). Değer listesi
// sayfanın ZATEN yüklü satırlarından (liste sunucu sayfalı değil: tek
// /api/databases okuması, tam küme) — ek istek yok, eager katalog yok. Seçim
// sayfanın URL durumuna yazar (System → ?dbsys, Database → ?dbname —
// üstteki seçicilerle TEK durum; Instance → ?dbinst, Top callers → ?dbcaller).
import { useMemo, useState, type ReactNode } from 'react';
import { ColumnFilterShell } from '@/pages/traces/ColumnFilter';
import { Input } from '@/components/shadcn/input'; // ui/SearchField donmuş (uiImportRatchet)

const SEARCH_CTL = 'h-7 bg-field dark:bg-field';
import { useT } from '@/lib/i18n';
import { dbFilterValues } from './dbFilterValues';

export function DbColumnFilter({ label, value, values, onPick }: {
  label: string; value: string; values: readonly string[]; onPick: (v: string) => void;
}): ReactNode {
  return (
    <ColumnFilterShell label={label} active={!!value} summary={value} onClear={() => onPick('')}>
      {close => <Body label={label} value={value} values={values} onPick={v => { onPick(v); close(); }} />}
    </ColumnFilterShell>
  );
}

function Body({ label, value, values, onPick }: { label: string; value: string; values: readonly string[]; onPick: (v: string) => void }) {
  const tr = useT();
  const [q, setQ] = useState('');
  const { shown, more } = useMemo(() => dbFilterValues(values, q, value), [values, q, value]);
  return (
    <>
      <Input type="search" className={SEARCH_CTL} value={q} onChange={e => setQ(e.target.value)} autoFocus
        aria-label={tr('traces.colFilter.search')} placeholder={tr('traces.colFilter.search')}
        onKeyDown={e => { if (e.key === 'Enter' && shown[0]) { e.preventDefault(); onPick(shown[0]); } }} />
      <div className="tcf-list" role="radiogroup" aria-label={label}>
        {shown.length === 0 && <div className="tcf-state">{q ? tr('traces.colFilter.noMatch') : tr('traces.colFilter.empty')}</div>}
        {shown.map(v => (
          <label key={v} className="tcf-row tcf-row--check" title={v}>
            <input type="radio" name={`db-col-filter-${label}`} checked={v === value} onChange={() => onPick(v)} />
            <span className="mono tcf-v">{v}</span>
          </label>
        ))}
      </div>
      {more > 0 && <p className="tcf-hint">+{more} — aramayı daralt</p>}
    </>
  );
}
