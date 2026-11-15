// EndpointColumnFilter — v0.10.2209 (operatör: Endpoints başlığına /traces'in
// hunisi). Kabuk /traces'in ColumnFilterShell'i (Exceptions / Databases ile
// aynı). Gövdeler sayfanın MEVCUT URL durumuna yazar: Service → ?service
// (üstteki "Filter by service…" ile TEK durum; değerler /api/service-names
// sunucu araması), Path → ?search (üstteki yol aramasıyla aynı), Method →
// ?method, Status → ?status (2xx..5xx). Liste "Top N" kesik olduğu için her
// süzgeç SUNUCUYA gider (yeniden sorgu), yüklü satırları süzmez.
import { useState, type ReactNode } from 'react';
import { ColumnFilterShell } from '@/pages/traces/ColumnFilter';
import { Input } from '@/components/shadcn/input'; // ui/SearchField donmuş (uiImportRatchet)

const SEARCH_CTL = 'h-7 bg-field dark:bg-field';
import { Spinner } from '@/components/Spinner';
import { usePickerSearch } from '@/components/usePickerSearch';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import { ENDPOINT_METHODS, ENDPOINT_STATUS_CLASSES } from './endpointColFilter';

export type EndpointFilterCol = 'service' | 'path' | 'method' | 'status';

export function EndpointColumnFilter({ col, label, value, onChange }: {
  col: EndpointFilterCol; label: string; value: string; onChange: (v: string) => void;
}): ReactNode {
  return (
    <ColumnFilterShell label={label} active={!!value} summary={value} onClear={() => onChange('')}>
      {close => {
        const pick = (v: string) => { onChange(v); close(); };
        if (col === 'service') return <ServiceBody value={value} onPick={pick} />;
        if (col === 'path') return <PathBody value={value} onApply={pick} />;
        const opts = col === 'method' ? ENDPOINT_METHODS : ENDPOINT_STATUS_CLASSES;
        return (
          <div className="tcf-list" role="radiogroup" aria-label={label}>
            {opts.map(o => (
              <label key={o} className="tcf-row tcf-row--check">
                <input type="radio" name={`ep-col-${col}`} checked={o === value} onChange={() => pick(o)} />
                <span className="mono tcf-v">{o}</span>
              </label>
            ))}
          </div>
        );
      }}
    </ColumnFilterShell>
  );
}

function PathBody({ value, onApply }: { value: string; onApply: (v: string) => void }) {
  const [q, setQ] = useState(value);
  return (
    <Input type="search" className={SEARCH_CTL} value={q} onChange={e => setQ(e.target.value)} autoFocus
      aria-label="Filter endpoints by path" placeholder="Filter by path (substring)…"
      onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); onApply(q.trim()); } }} />
  );
}

function ServiceBody({ value, onPick }: { value: string; onPick: (v: string) => void }) {
  const tr = useT();
  const [q, setQ] = useState('');
  const res = usePickerSearch(q.trim(), '', s => api.serviceNames(s, 50).then(r => ({ items: r.names, total: r.total })));
  const rows = value && !res.items.includes(value) && (!q || value.includes(q.trim())) ? [value, ...res.items] : res.items;
  return (
    <>
      <Input type="search" className={SEARCH_CTL} value={q} onChange={e => setQ(e.target.value)} autoFocus
        aria-label={tr('traces.colFilter.search')} placeholder={tr('traces.colFilter.search')}
        onKeyDown={e => { if (e.key === 'Enter' && rows[0]) { e.preventDefault(); onPick(rows[0]); } }} />
      <div className="tcf-list" role="radiogroup" aria-label="Service" aria-busy={res.status === 'loading'}>
        {res.status === 'loading' && res.items.length === 0 && <div className="tcf-state"><Spinner /></div>}
        {res.status === 'error' && <div className="tcf-state tcf-err">{tr('traces.colFilter.error')}</div>}
        {res.status === undefined && rows.length === 0 && <div className="tcf-state">{tr('traces.colFilter.noMatch')}</div>}
        {rows.map(name => (
          <label key={name} className="tcf-row tcf-row--check" title={name}>
            <input type="radio" name="ep-col-service" checked={name === value} onChange={() => onPick(name)} />
            <span className="mono tcf-v">{name}</span>
          </label>
        ))}
      </div>
    </>
  );
}
