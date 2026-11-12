import { useMemo, useRef, useState, FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { useAuth } from '@/components/AuthProvider';
import { Button, Field, IconButton, Modal, SearchField, Stack } from '@/components/ui';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { DashboardListFilters } from '@/components/coremetry/dashboard/DashboardListFilters';
import { useDashboardRowActions } from '@/components/coremetry/dashboard/useDashboardRowActions';
import { TemplateGallery } from '@/components/coremetry/dashboard/TemplateGallery';
import { csvParam, boolParam, strParam, useUrlParams, type ParamSpec } from '@/lib/urlParams';
import { filterDashboards, tagChips, toggleTag } from '@/lib/dashboardList';
import { api } from '@/lib/api';
import { parseDashboardImport } from '@/lib/dashboardIO';
import { toast } from '@/lib/toast';
import { tsLong, agoTR } from '@/lib/utils';
import type { DashboardEditor, DashboardSummary } from '@/lib/types';
import { PageControls } from '@/components/coremetry/PageControls';
import { PageShell } from '@/components/coremetry/PageShell';

// STAR_PAGE (v0.9.780) — yıldızlar saved_views'te yaşıyor, YENİ ŞEMA YOK
// (CLAUDE.md: kullanıcı-kaydı state için saved_views). Satır başına
// page='dashboard-star', queryString=<pano id>, name=<pano adı>.
// Kullanıcı-başı olması doğru davranış: yıldız kişisel bir kısayol,
// etiketler ise panonun kendisine ait ve paylaşılan.
//
// /api/views'te rol kapısı yok — viewer da yıldızlayabilir, öyle olmalı.
// `shared` ASLA gönderilmiyor: true olsaydı yıldız herkese yapışır ve
// admin olmayan kimse söküp atamazdı.
const STAR_PAGE = 'dashboard-star';

// Columns for the shared sortable + resizable DataTable primitive. The
// list is a small fetched array (saved dashboards), so client-side sort
// applies.
//
// Sıralama (v0.9.780): varsayılan yıldız DESC. Sunucu zaten
// updated_at DESC döndürüyor ve sortRows STABLE olduğundan sonuç
// "yıldızlılar üstte, her grup içinde en son güncellenen önce" —
// yani eski varsayılan grupların İÇİNDE aynen korunuyor.
//
// v0.10.947 (tablo standardı) — ikincil kolonların rengi `tone`da (S3).
//
// Dilim 3.1 (Faz 3): coremetry DataTable — satır gerçek bağlantı (yeni sekme
// çalışır), ⋯ satır menüsü (Aç · Çoğalt · Dışa aktar · Sil), yıldız ve
// etiket hücreleri kendi düğmelerini taşır (`ownLink`).
//
// Dilim 3.2: "Panel" (liste yanıtındaki panelCount), "Son düzenleyen"
// (audit_log'dan türetilir, kolon yok — S7) ve kişisel "Son görüntülemeniz"
// (saved_views page='dashboard-recent'). Eksik değer soluk "—".
interface ListMeta {
  starMap: Map<string, string>;
  editors: Map<string, DashboardEditor>;
  recent: Map<string, number>; // pano id → unix ms
}
function dashCols({ starMap, editors, recent }: ListMeta): ColumnDef<DashboardSummary>[] {
  return [
    { id: 'star',        label: 'Favori',     sortValue: r => (starMap.has(r.id) ? 1 : 0), numeric: true, width: 36, minWidth: 36, ownLink: true, priority: 1 },
    { id: 'name',        label: 'Dashboard',  sortValue: r => r.name,        naturalDir: 'asc', width: 240, minWidth: 160, priority: 1 },
    { id: 'tags',        label: 'Tags',       sortValue: r => (r.tags ?? []).join(','), naturalDir: 'asc', width: 160, minWidth: 100, priority: 2 },
    { id: 'panels',      label: 'Panel',      sortValue: r => r.panelCount ?? -1, numeric: true, width: 64, minWidth: 56, priority: 3 },
    { id: 'description', label: 'Description', sortValue: r => r.description, naturalDir: 'asc', width: 260, minWidth: 140, flex: true, priority: 4, tone: () => 'muted' },
    { id: 'editor',      label: 'Son düzenleyen', sortValue: r => editors.get(r.id)?.actor ?? '', naturalDir: 'asc', width: 170, minWidth: 110, priority: 3, tone: () => 'muted' },
    { id: 'updatedAt',   label: 'Updated',    sortValue: r => r.updatedAt,   numeric: true,     width: 130, minWidth: 100, priority: 2, tone: () => 'muted' },
    { id: 'recent',      label: 'Son görüntülemeniz', sortValue: r => recent.get(r.id) ?? 0, numeric: true, width: 150, minWidth: 110, priority: 2, tone: () => 'muted' },
  ];
}

// Liste süzgeci URL'de (CLAUDE.md "URL = source of truth"): ?q, ?tags, ?fav.
interface ListParams { q: string; tags: string[]; fav: boolean }
const LIST_PARAMS: ParamSpec<ListParams> = { q: strParam(), tags: csvParam(), fav: boolParam() };

export default function DashboardsPage() {
  const { user } = useAuth();
  const navigate = useNavigate();
  const searchRef = useRef<HTMLInputElement>(null);
  const [showNew, setShowNew] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const [importing, setImporting] = useState(false);
  const [params, setParams] = useUrlParams(LIST_PARAMS);
  const q = params.q;
  const setQ = (v: string) => setParams({ q: v });

  // v0.6.50 — import a dashboard from a previously-exported JSON
  // file. Reuses POST /api/dashboards (createDashboard) so the
  // imported board lands as a fresh dashboard with a new id; no
  // new backend route needed. Validation lives in
  // parseDashboardImport so it's unit-testable + shared with any
  // future drag-drop surface.
  const onImportFile = async (file: File) => {
    setImporting(true);
    try {
      const text = await file.text();
      const payload = parseDashboardImport(text); // throws on bad shape
      const d = await api.createDashboard(payload);
      toast.success(`Imported "${payload.name}"`);
      navigate(`/dashboard?id=${d.id}&edit=1`);
    } catch (err) {
      toast.error('Import failed: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      setImporting(false);
      if (fileRef.current) fileRef.current.value = ''; // allow re-import of same file
    }
  };

  const dashboardsQ = useQuery<DashboardSummary[]>({
    queryKey: ['dashboards', 'list'],
    queryFn: async () => (await api.listDashboards()) ?? [],
    staleTime: 60_000,
  });
  const items = dashboardsQ.isLoading
    ? undefined
    : dashboardsQ.isError
      ? null
      : dashboardsQ.data ?? [];

  // ── Yıldızlar (v0.9.780) ──────────────────────────────────────────
  // saved_views(page='dashboard-star') → Map<pano id, saved view id>.
  const starsQ = useQuery({
    queryKey: ['dashboard-stars'],
    queryFn: async () => (await api.savedViews(STAR_PAGE)) ?? [],
    staleTime: 60_000,
  });
  const starMap = useMemo(() => {
    const m = new Map<string, string>();
    // TUZAK: createSavedView UPSERT DEĞİL — her POST yeni satır üretir
    // (Alerts.tsx'teki "server dedups" yorumu yanlış). Çift-tık yarışında
    // aynı pano için iki satır kalabilir; okuma tarafı Map'e indirgeyerek
    // zararsızlaştırıyor, silme ilk bulduğu satırı kaldırır.
    for (const v of starsQ.data ?? []) {
      if (v.queryString && !m.has(v.queryString)) m.set(v.queryString, v.id);
    }
    return m;
  }, [starsQ.data]);

  // Dilim 3.1 — arama (ad + açıklama + etiket) + etiket çipleri (VE) +
  // yalnız favoriler; saf çekirdek lib/dashboardList.
  const starred = useMemo(() => new Set(starMap.keys()), [starMap]);
  const filtered = useMemo(
    () => (items ? filterDashboards(items, params, starred) : items),
    [items, params, starred],
  );
  const chips = useMemo(() => tagChips(items ?? [], params.tags), [items, params.tags]);
  const filtersActive = q.trim() !== '' || params.tags.length > 0 || params.fav;

  const [starBusy, setStarBusy] = useState<string | null>(null);
  const toggleStar = async (d: DashboardSummary) => {
    if (starBusy) return;
    setStarBusy(d.id);
    try {
      const viewId = starMap.get(d.id);
      if (viewId) {
        await api.deleteSavedView(viewId);
      } else {
        await api.createSavedView({
          // name BOŞ OLAMAZ: boş ad saved_views'te tombstone demek
          // (okuma yolu name != '' filtreliyor), yani yıldız daha
          // doğarken silinmiş sayılırdı.
          name: d.name || d.id,
          page: STAR_PAGE,
          queryString: d.id,
          // `shared` bilinçli GÖNDERİLMİYOR — bkz. STAR_PAGE notu.
        });
      }
      await starsQ.refetch();
    } catch (err) {
      toast.error('Yıldız güncellenemedi: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      setStarBusy(null);
    }
  };

  // Dilim 3.2 — son düzenleyen (paylaşılan, sunucu 60 sn) + kişisel son
  // görüntülenenler. İkisi de liste açılınca bir kez okunur; polling yok.
  const editorsQ = useQuery({
    queryKey: ['dashboards', 'editors'],
    queryFn: ({ signal }) => api.dashboardEditors(signal),
    staleTime: 60_000,
  });
  const recentQ = useQuery({
    queryKey: ['dashboards', 'recent'],
    queryFn: ({ signal }) => api.dashboardRecent(signal),
    staleTime: 60_000,
  });
  const editors = useMemo(() => {
    const m = new Map<string, DashboardEditor>();
    for (const e of editorsQ.data?.editors ?? []) m.set(e.dashboardId, e);
    return m;
  }, [editorsQ.data]);
  const recent = useMemo(() => {
    const m = new Map<string, number>();
    for (const e of recentQ.data?.entries ?? []) m.set(e.id, e.at);
    return m;
  }, [recentQ.data]);

  const isAdmin = user?.role === 'admin' || user?.role === 'editor';

  // Shared sortable + resizable table with operator-speed keyboard nav
  // (j/k move, Enter/o open, "/" focuses the filter). Called
  // unconditionally (hooks rule) with [] while loading.
  const columns = useMemo(() => dashCols({ starMap, editors, recent }), [starMap, editors, recent]);
  const rowActions = useDashboardRowActions(isAdmin, items ?? []);

  // v0.10.967 — tablo standardı dilim 5 (T12 + P-1): yükleniyor / hata /
  // eşleşme yok / boş tablonun İÇİNDE, başlık durur. Sıra eskisiyle aynı
  // (hata filtered'ı da null yapar → bayat satır kalmaz). Eşleşme yok yalnız
  // kaynakta pano VARKEN; "Filtreleri temizle" aramanın kendi ✕'iyle aynı
  // setter, odak arama kutusuna döner. "+ New dashboard" CTA'sı boş satırın
  // detail yuvasında (P-1) — silinmedi, tablonun dışına da taşınmadı.
  const tableState: DataTableStateInput<DashboardSummary> =
    items === undefined ? { kind: 'loading' }
    : items === null ? { kind: 'error' }
    : items.length > 0 ? {
        kind: 'no-match',
        message: q.trim() ? `“${q}” ile eşleşen kayıtlı pano yok` : 'Süzgeçle eşleşen kayıtlı pano yok',
        onClearFilters: () => setParams({ q: '', tags: [], fav: false }),
        returnFocusRef: searchRef,
      }
    : {
        kind: 'empty',
        message: isAdmin
          ? 'Henüz pano yok — metrik, trace ve logları tek bir görünümde birleştirmek için bir tane oluştur.'
          : 'Henüz pano yok — pano oluşturması için bir admin\'e başvur.',
        detail: isAdmin && <Button variant="primary" onClick={() => setShowNew(true)}>+ New dashboard</Button>,
      };

  return (
    <>
      <Topbar title="Dashboards" />
      <PageShell>
        {/* v0.10.1319 K1 — eylemler sağ kümede: ikincil (Import) primary'nin
            SOLUNDA, primary EN SAĞDA. */}
        <PageControls sticky
          actions={isAdmin && (
            <>
              <Button variant="secondary" onClick={() => fileRef.current?.click()}
                title="Import a dashboard from an exported JSON file" loading={importing}>
                ↑ Import JSON
              </Button>
              <Button variant="primary" onClick={() => setShowNew(true)}>+ New dashboard</Button>
            </>
          )}>
          {/* v0.9.1012 (M8) — ayrı "Clear" butonu atomun kutu-içi ✕'ine
              devredildi: temizleme affordance'ı aramanın KENDİSİNDE. */}
          <SearchField ref={searchRef} value={q} onChange={setQ}
            placeholder="Filter dashboards…" aria-label="Filter dashboards"
            hint="/" width={220} />
          <span style={{ color: 'var(--text3)', fontSize: 12 }}>
            {filtered?.length ?? 0} dashboard{(filtered?.length ?? 0) === 1 ? '' : 's'}
          </span>
        </PageControls>
        {/* Gizli dosya girdisi barın DIŞINDA (v0.10.1319): içeride kalsaydı
            dar ekranda "Filtreler (N)" sayacı onu bir filtre alanı sayardı. */}
        {isAdmin && (
          <input ref={fileRef} type="file" accept="application/json,.json"
            style={{ display: 'none' }}
            onChange={e => {
              const f = e.target.files?.[0];
              if (f) onImportFile(f);
            }} />
        )}

        {/* v0.10.1997 (dilim 3.3) — şablon şeridi tablonun ÜSTÜNDE, tek satır;
            kurulum yazma eylemi → yalnız editor+. */}
        {isAdmin && <TemplateGallery />}

        {chips.length > 0 || params.fav || starMap.size > 0 ? (
          <DashboardListFilters chips={chips} selected={params.tags} fav={params.fav}
            onToggleTag={t => setParams({ tags: toggleTag(params.tags, t) })}
            onFav={v => setParams({ fav: v })} />
        ) : null}

        <div className="table-wrap">
          <DataTable<DashboardSummary>
            storageKey="dashboards"
            ariaLabel="Panolar"
            columns={columns}
            rows={filtered ?? []}
            // v0.9.780 — yıldızlılar üstte. Sunucu sırası zaten updated_at DESC
            // ve sortRows stable, dolayısıyla grup İÇİ sıra eski varsayılanın
            // aynısı kalıyor.
            sort={{ initial: { id: 'star', dir: 'desc' } }}
            getRowKey={d => d.id}
            getRowHref={d => `/dashboard?id=${encodeURIComponent(d.id)}`}
            state={tableState}
            rowActions={{ label: 'Eylemler', items: rowActions, triggerLabel: d => `${d.name} eylemleri` }}
            renderCell={(d, c) => {
              switch (c.id) {
                case 'star': return (
                  <IconButton
                    aria-label={starMap.has(d.id) ? `${d.name} yıldızını kaldır` : `${d.name} panosunu yıldızla`}
                    active={starMap.has(d.id)}
                    variant="bare" size="xs" className="ib-star"
                    tooltip={starMap.has(d.id) ? 'Yıldızı kaldır' : 'Yıldızla — listenin başına gelir'}
                    disabled={starBusy === d.id}
                    onClick={e => { e.stopPropagation(); void toggleStar(d); }}
                    icon={starMap.has(d.id) ? '★' : '☆'} />
                );
                case 'name': return <span className="cell-strong">{d.name}</span>;
                case 'tags': return <span title={(d.tags ?? []).join(', ') || undefined}><TagBadges tags={d.tags} /></span>;
                case 'description': return d.description || <span className="cell-empty">—</span>;
                case 'panels': return d.panelCount ?? <span className="cell-empty">—</span>;
                case 'editor': {
                  const e = editors.get(d.id);
                  return e
                    ? <span title={`${e.actor} · ${tsLong(e.at)}`}>{e.actor}</span>
                    : <span className="cell-empty" title="Denetim kaydında düzenleme yok (hazır pano ya da 365 günden eski)">—</span>;
                }
                case 'updatedAt': return <span title={`Updated ${tsLong(d.updatedAt)}`}>{tsLong(d.updatedAt)}</span>;
                case 'recent': {
                  const at = recent.get(d.id);
                  return at
                    ? <span title={tsLong(at * 1e6)}>{agoTR(at * 1e6)}</span>
                    : <span className="cell-empty">hiç</span>;
                }
              }
              return null;
            }} />
        </div>

        {showNew && isAdmin && (
          <NewDashboardModal
            onClose={() => setShowNew(false)}
            onCreated={(id) => { setShowNew(false); navigate(`/dashboard?id=${id}&edit=1`); }}
          />
        )}
      </PageShell>
    </>
  );
}

// TagBadges (v0.9.780) — etiket rozetleri. İlk 3 + "+N": kolon
// genişliği sabit (tableLayout:fixed) ve taşan içerik sessizce
// kırpılırdı; sayı görünür kalsın diye kalanı tek rozette toplanıyor,
// tamamı hücrenin title'ında.
function TagBadges({ tags }: { tags?: string[] }) {
  const list = tags ?? [];
  if (list.length === 0) return <span style={{ color: 'var(--text3)' }}>—</span>;
  const shown = list.slice(0, 3);
  const rest = list.length - shown.length;
  return (
    <span className="row gap-1" style={{ flexWrap: 'nowrap', minWidth: 0 }}>
      {shown.map(t => (
        <span key={t} className="badge b-gray" style={{
          maxWidth: 90, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
        }}>{t}</span>
      ))}
      {rest > 0 && <span className="badge b-gray" title={list.join(', ')}>+{rest}</span>}
    </span>
  );
}

function NewDashboardModal({ onClose, onCreated }: {
  onClose: () => void; onCreated: (id: string) => void;
}) {
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      const d = await api.createDashboard({ name, description, panels: [] });
      onCreated(d.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={true}
      onClose={onClose}
      title="New dashboard"
      size="sm"
      initialFocus="input[name=name]"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button variant="primary" type="submit" form="new-dashboard-form" loading={busy}>Create</Button>
        </>
      }>
      <form id="new-dashboard-form" onSubmit={submit}>
        <Stack gap={3}>
          <Field
            label="Name"
            name="name"
            required
            value={name}
            onChange={e => setName(e.target.value)} />
          <Field
            label="Description (optional)"
            value={description}
            onChange={e => setDescription(e.target.value)} />
          {error && (
            <div style={{ color: 'var(--err)', fontSize: 12 }}>{error}</div>
          )}
        </Stack>
      </form>
    </Modal>
  );
}
