import { Fragment, useEffect, useState } from 'react';
import { Button, SearchField, DisclosureButton } from '@/components/ui';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { api } from '@/lib/api';
import { tsLong } from '@/lib/utils';
import type { WikiMode, WikiPageRow, WikiPagesPage, WikiSyncStatus } from '@/lib/types';
import { pagesEmptyText } from './wikiKnowledge';

// WikiPagesTable — v0.10.1124 (operatör: "wiki içeriğini sayfada göremiyorum").
// İndeksteki wiki sayfaları: proje, wiki, başlık (Azure DevOps sayfasına
// bağlantı), son güncelleme, parça sayısı. SUNUCU-SAYFALI (≤100 satır,
// GET /api/wiki/pages) → useDataTable yalnız boyutlandırma yarısı (sıralama
// yok — tablo standardı: sunucu-sayfalı tablo resize-only). Başlık/yol
// süzgeci sunucuda (350 ms gecikmeli). Satır açılımı içeriğin ilk ~1000
// karakteri — YALNIZ yönetici (sunucu önizlemeyi başkasına hiç göndermez).

const WIKI_PAGE_SIZE = 100;
const DEBOUNCE_MS = 350;

const COLS: ColumnDef<WikiPageRow>[] = [
  { id: 'project', label: 'Proje', width: 140 },
  { id: 'wiki', label: 'Wiki', width: 150 },
  { id: 'title', label: 'Sayfa', width: 320, ownLink: true },
  { id: 'updated', label: 'Son güncelleme', width: 170 },
  { id: 'chunks', label: 'Parça', width: 80, numeric: true },
];

function updatedMs(r: WikiPageRow): number {
  const t = Date.parse(r.updatedAt);
  return Number.isFinite(t) ? t : 0;
}

export function WikiPagesTable({ status, mode }: { status?: WikiSyncStatus; mode: WikiMode }) {
  const [q, setQ] = useState('');
  const [debounced, setDebounced] = useState('');
  const [page, setPage] = useState(0);
  const [data, setData] = useState<WikiPagesPage | null | undefined>(undefined);
  const [open, setOpen] = useState<string | null>(null);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    const id = window.setTimeout(() => { setDebounced(q.trim()); setPage(0); }, DEBOUNCE_MS);
    return () => window.clearTimeout(id);
  }, [q]);

  useEffect(() => {
    let live = true;
    setData(undefined);
    api.getWikiPages(debounced, page * WIKI_PAGE_SIZE, WIKI_PAGE_SIZE)
      .then(r => { if (live) setData(r); })
      .catch(() => { if (live) setData(null); });
    return () => { live = false; };
  }, [debounced, page, reload]);

  const rows = data?.rows ?? [];
  const total = data?.total ?? 0;
  const hasMore = (page + 1) * WIKI_PAGE_SIZE < total;
  const dt = useDataTable<WikiPageRow>({
    storageKey: 'settings-wiki-pages', columns: COLS, rows,
    server: { page, pageSize: WIKI_PAGE_SIZE, hasMore, onPage: setPage },
  });
  const preview = !!data?.preview;
  const key = (r: WikiPageRow) => `${r.wikiId}\u0000${r.path}`;

  return (
    <div data-testid="wiki-pages" style={{ marginTop: 12 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap', marginBottom: 6 }}>
        <h4 style={{ fontSize: 12, fontWeight: 600, margin: 0 }}>İndeksteki sayfalar</h4>
        <SearchField value={q} onChange={setQ} placeholder="başlık ya da yol süz…" aria-label="Wiki sayfası süz" width={240} />
        <span style={{ fontSize: 12, color: 'var(--text3)' }}>
          {data ? `${total} sayfa${total > WIKI_PAGE_SIZE ? ` · ${page * WIKI_PAGE_SIZE + 1}–${Math.min(total, (page + 1) * WIKI_PAGE_SIZE)}` : ''}` : ''}
        </span>
      </div>
      <div className="table-wrap">
        <table {...dt.tableProps}>
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {rows.length === 0
              ? <DataTableState dt={dt} {...(data === undefined ? { kind: 'loading' as const }
                : data === null ? { kind: 'error' as const, message: 'Wiki sayfaları okunamadı.', onRetry: () => setReload(n => n + 1) }
                : debounced ? { kind: 'no-match' as const, onClearFilters: () => setQ('') }
                : { kind: 'empty' as const, message: pagesEmptyText(status, mode) })} />
              : rows.map(r => {
                const k = key(r);
                const expanded = open === k;
                return (
                  <Fragment key={k}>
                    <tr>
                      <DataTableCell dt={dt} col="project" row={r} value={r.project} />
                      <DataTableCell dt={dt} col="wiki" row={r} value={r.wiki} />
                      <DataTableCell dt={dt} col="title" row={r} title={r.path}>
                        {preview && (
                          <DisclosureButton expanded={expanded} aria-label={`${r.title} önizlemesi`}
                            onClick={() => setOpen(expanded ? null : k)} style={{ marginRight: 4 }} />
                        )}
                        {r.url && /^https?:\/\//.test(r.url)
                          ? <a href={r.url} target="_blank" rel="noopener noreferrer">{r.title || r.path}</a>
                          : <span>{r.title || r.path}</span>}
                      </DataTableCell>
                      <DataTableCell dt={dt} col="updated" row={r} value={updatedMs(r) ? tsLong(updatedMs(r) * 1e6) : ''} />
                      <DataTableCell dt={dt} col="chunks" row={r} value={r.chunks} />
                    </tr>
                    {expanded && preview && (
                      <tr data-testid="wiki-page-preview">
                        <td colSpan={COLS.length}>
                          <pre style={{ whiteSpace: 'pre-wrap', fontSize: 12, margin: '4px 0', maxHeight: 260, overflow: 'auto' }}>
                            {r.preview || '(boş sayfa)'}
                          </pre>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })}
          </tbody>
        </table>
      </div>
      {(page > 0 || hasMore) && (
        <div style={{ display: 'flex', gap: 8, marginTop: 6 }}>
          <Button variant="secondary" size="sm" disabled={page === 0} onClick={() => setPage(p => Math.max(0, p - 1))}>← Önceki</Button>
          <Button variant="secondary" size="sm" disabled={!hasMore} onClick={() => setPage(p => p + 1)}>Sonraki →</Button>
        </div>
      )}
    </div>
  );
}
