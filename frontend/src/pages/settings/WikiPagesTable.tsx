import { useEffect, useState } from 'react';
import { ChevronDown, ChevronRight, Search, X } from 'lucide-react';
import { appLinkKind } from '@/lib/safeAppPath';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
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
//
// Faz 2 dilim 1 R12 — coremetry/DataTable: önizleme satırı elle yazılmış
// `<td colSpan>` yerine `detail` (R5/G4; açık/kapalı durumu bu sayfada);
// açma düğmesi shadcn ghost Button + lucide chevron, aria-expanded aynı;
// SearchField → shadcn Input type=search + Search ikonu + temizle düğmesi
// (plan G16 tarifi; görünür etiket eklenmedi — erişilebilir ad aynı).

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
  const preview = !!data?.preview;
  const key = (r: WikiPageRow) => `${r.wikiId}\u0000${r.path}`;
  const state: DataTableStateInput<WikiPageRow> =
    data === undefined ? { kind: 'loading' }
    : data === null ? { kind: 'error', message: 'Wiki sayfaları okunamadı.', onRetry: () => setReload(n => n + 1) }
    : debounced ? { kind: 'no-match', onClearFilters: () => setQ('') }
    : { kind: 'empty', message: pagesEmptyText(status, mode) };

  const cell = (r: WikiPageRow, col: ColumnDef<WikiPageRow>) => {
    switch (col.id) {
      case 'project': return r.project;
      case 'wiki': return r.wiki;
      case 'title': {
        const expanded = open === key(r);
        return (
          <span className="inline-flex min-w-0 max-w-full items-center gap-1" title={r.path}>
            {preview && (
              <Button variant="ghost" size="icon-xs" aria-expanded={expanded} aria-label={`${r.title} önizlemesi`}
                onClick={() => setOpen(expanded ? null : key(r))}>
                {expanded ? <ChevronDown aria-hidden="true" /> : <ChevronRight aria-hidden="true" />}
              </Button>
            )}
            {r.url && appLinkKind(r.url) === 'external' /* v0.10.1315 — tek kapı */
              ? <a href={r.url} target="_blank" rel="noopener noreferrer" className="dt-trunc" title={r.title || r.path}>{r.title || r.path}</a>
              : <span className="dt-trunc" title={r.title || r.path}>{r.title || r.path}</span>}
          </span>
        );
      }
      case 'updated': return updatedMs(r) ? tsLong(updatedMs(r) * 1e6) : '';
      case 'chunks': return r.chunks;
      default: return null;
    }
  };

  return (
    <div data-testid="wiki-pages" className="mt-3">
      <div className="mb-1.5 flex flex-wrap items-center gap-2.5">
        <h4 className="m-0 text-[length:var(--fs-sm)] font-semibold">İndeksteki sayfalar</h4>
        <span className="relative inline-flex w-[240px] max-w-full items-center">
          <Search aria-hidden="true" className="pointer-events-none absolute left-2 size-3.5 text-(--text3)" />
          <Input type="search" value={q} onChange={e => setQ(e.target.value)}
            placeholder="başlık ya da yol süz…" aria-label="Wiki sayfası süz"
            className="h-8 bg-field border-(--border-control) pr-7 pl-7 text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field" />
          {q !== '' && (
            <Button variant="ghost" size="icon-xs" aria-label="Clear search" className="absolute right-1"
              onClick={() => setQ('')}>
              <X aria-hidden="true" />
            </Button>
          )}
        </span>
        <span className="text-[length:var(--fs-sm)] text-(--text3)">
          {data ? `${total} sayfa${total > WIKI_PAGE_SIZE ? ` · ${page * WIKI_PAGE_SIZE + 1}–${Math.min(total, (page + 1) * WIKI_PAGE_SIZE)}` : ''}` : ''}
        </span>
      </div>
      <div className="table-wrap">
        <DataTable<WikiPageRow> storageKey="settings-wiki-pages" columns={COLS} rows={rows}
          sort={{ server: { page, pageSize: WIKI_PAGE_SIZE, hasMore, onPage: setPage } }}
          getRowKey={key}
          state={state}
          renderCell={cell}
          detail={{
            isOpen: r => preview && open === key(r),
            // v0.10.1705 — sınıfsız ayrıntı <td>'si genel `tbody td` kuralını
            // (nowrap + max-width 320px + overflow hidden) alıyordu: tüm satıra
            // yayılan önizleme 320px'te kesiliyordu. cell-wrap sınırı kaldırır.
            cellClassName: () => 'cell-wrap',
            render: r => (
              <pre data-testid="wiki-page-preview" className="my-1 max-h-[260px] overflow-auto text-[length:var(--fs-sm)] whitespace-pre-wrap">
                {r.preview || '(boş sayfa)'}
              </pre>
            ),
          }} />
      </div>
      {(page > 0 || hasMore) && (
        <div className="mt-1.5 flex gap-2">
          <Button variant="outline" size="sm" disabled={page === 0} onClick={() => setPage(p => Math.max(0, p - 1))}>← Önceki</Button>
          <Button variant="outline" size="sm" disabled={!hasMore} onClick={() => setPage(p => p + 1)}>Sonraki →</Button>
        </div>
      )}
    </div>
  );
}
