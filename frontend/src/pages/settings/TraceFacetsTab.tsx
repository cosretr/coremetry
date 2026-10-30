// TraceFacetsTab.tsx — v0.10.303 (trace arama Dilim 2b, Datadog "facets").
//
// Yaygın-değerli attribute'lar (channel, tenant…) için terfi kolonu +
// set(0) indeksi: Dilim 1'in hash indeksi nadir değerde budar, yaygın
// değerde budamaz — orası burası. Kayıt system_settings'te; app-managed
// kurulumda DDL boot/PUT'ta kendisi, dış Distributed prod'da üretilen SQL
// elle (0013 emsali). Durum sütunları BU pod'un gördüğüdür ("kolon VAR ≠
// DOLU": routed = probe doğruladı, filtre kolona gidiyor).
//
// Faz 2 dilim 1 R8 — shadcn/coremetry: coremetry/DataTable (aynı kolonlar,
// aynı storageKey), coremetry/Field + SelectField, shadcn Button,
// StatusBadge (D7); Stack/Row → flex utility; inline stil yok.
import { useState, type ReactNode } from 'react';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { Spinner } from '@/components/Spinner';
import { CopyButton } from '@/components/CopyButton';
import { api } from '@/lib/api';
import type { TraceFacet, TraceFacetStatus, TraceFacetsResponse } from '@/lib/types';
import { FlashBox, humanize, useSettingsLoad, SettingsLoadError } from './shared';

type FacetRow = TraceFacet & { status?: TraceFacetStatus };

// v0.10.942 (tablo standardı dilim 3) — `act` sahte sıralanabilir değil,
// eylem kolonu (T3/T8); 11px kod hücreleri `mono` + `tone: muted` (S3).
// R8: Durum kolonu `numeric` bayrağını bıraktı — bayrak yalnız sıralama
// yönü içindi (rozet hücresi `num` almıyordu, başlık sağa yaslıydı);
// sıralama değeri sayı olduğundan karşılaştırma ve ilk yön (desc) aynı.
const COLS: ColumnDef<FacetRow>[] = [
  { id: 'key', label: 'Attribute', sortValue: r => r.key, naturalDir: 'asc', width: 180, mono: true },
  { id: 'spellings', label: 'Yazımlar', sortValue: r => (r.spellings ?? []).join(','), naturalDir: 'asc', flex: true, mono: true, tone: () => 'muted' },
  { id: 'scope', label: 'Kapsam', sortValue: r => r.scope ?? 'span', naturalDir: 'asc', width: 90 },
  { id: 'type', label: 'Tip', sortValue: r => r.type ?? 'lc', naturalDir: 'asc', width: 70 },
  { id: 'column', label: 'Kolon', sortValue: r => r.status?.column ?? '', naturalDir: 'asc', width: 170, mono: true, tone: () => 'muted' },
  { id: 'state', label: 'Durum (bu pod)', sortValue: r => (r.status?.routed ? 2 : r.status?.columnExists ? 1 : 0), width: 150 },
  { id: 'act', label: 'Eylemler', kind: 'actions', width: 60 },
];

// v0.10.929 (K5) — 'kolona yönleniyor' normal son hâl: nötr; renk yalnız eksik kolon/indekste.
export function facetStateLabel(st?: TraceFacetStatus): { text: string; tone: 'warning' | 'neutral' } {
  if (!st) return { text: 'kaydedilmedi', tone: 'neutral' };
  if (st.routed) return { text: 'kolona yönleniyor', tone: 'neutral' };
  if (st.columnExists) return { text: st.indexExists ? 'kolon var · doğrulanmadı' : 'kolon var · indeks yok', tone: 'warning' };
  return { text: 'kolon yok', tone: 'warning' };
}

const EMPTY = <span className="cell-empty">—</span>;

export function TraceFacetsTab() {
  const [facets, setFacets] = useState<TraceFacet[]>([]);
  const [status, setStatus] = useState<TraceFacetStatus[]>([]);
  const [bootManaged, setBootManaged] = useState(true);
  const [sql, setSql] = useState('');
  const [note, setNote] = useState('');
  const [showSql, setShowSql] = useState(false);
  const [draft, setDraft] = useState<{ key: string; spellings: string; scope: 'span' | 'resource'; type: 'lc' | 'string' }>({ key: '', spellings: '', scope: 'span', type: 'lc' });
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const apply = (d: TraceFacetsResponse) => {
    setFacets(d.facets ?? []); setStatus(d.status ?? []); setBootManaged(!!d.bootManaged); setSql(d.migrationSql ?? ''); setNote(d.note ?? '');
  };
  const { loaded, error: loadErr, retry } = useSettingsLoad(() => api.getTraceFacets(), apply);

  const rows: FacetRow[] = facets.map(f => ({ ...f, status: status.find(s => s.key === f.key) }));

  const save = async (next: TraceFacet[]) => {
    setBusy(true); setMsg(null);
    try {
      const d = await api.putTraceFacets(next);
      apply(d);
      setMsg({ kind: 'ok', text: d.note ?? 'Kaydedildi.' });
    } catch (e) {
      setMsg({ kind: 'err', text: humanize(e) });
    } finally { setBusy(false); }
  };
  const add = () => {
    const key = draft.key.trim();
    if (!key) { setMsg({ kind: 'err', text: 'Attribute anahtarı boş.' }); return; }
    const spellings = draft.spellings.split(',').map(s => s.trim()).filter(Boolean);
    void save([...facets, { key, spellings: spellings.length ? spellings : undefined, scope: draft.scope, type: draft.type }]);
    setDraft({ key: '', spellings: '', scope: 'span', type: 'lc' });
  };
  const remove = (key: string) => void save(facets.filter(f => f.key !== key));

  const renderCell = (r: FacetRow, col: ColumnDef<FacetRow>): ReactNode => {
    switch (col.id) {
      case 'key': return r.key;
      case 'spellings': return (r.spellings ?? [r.key]).join(', ');
      case 'scope': return r.scope ?? 'span';
      case 'type': return r.type ?? 'lc';
      case 'column': return r.status?.column || EMPTY;
      case 'state': {
        const st = facetStateLabel(r.status);
        return <StatusBadge status={st.tone === 'warning' ? 'warn' : 'unknown'}>{st.text}</StatusBadge>;
      }
      case 'act': return (
        <Button variant="ghost" size="xs" disabled={busy} onClick={() => remove(r.key)}
          title="Kaydı sil (kolon/indeks ClickHouse'ta kalır; rollback SQL'i elle)">Sil</Button>
      );
      default: return null;
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;
  return (
    <div className="flex flex-col gap-4">
      <p className="field-hint">
        Yaygın değerli attribute'lar (channel, tenant, region…) için terfi kolonu + <code>set(0)</code> indeksi — Datadog "facet" karşılığı.
        Nadir değerler (id, txn_ref) zaten hash indeksiyle (attr_kvh) hızlı; buraya yalnız her span'de görülen anahtarları ekleyin.
        {bootManaged
          ? ' Bu kurulum uygulama yönetimli: kolon/indeks DDL\'i kaydetmeyle gönderilir (küme kipinde ertelenebilir); filtreler, probe kolonu DOLU görünce kolona yönlenir.'
          : ' Bu kurulum dış Distributed ClickHouse: üretilen SQL\'i elle koşun, sonra pod\'ları yeniden başlatın.'}
      </p>
      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
      {note && !msg && <FlashBox kind="ok">{note}</FlashBox>}
      <div className="flex flex-wrap items-end gap-4">
        <Field className="w-auto" label="Attribute anahtarı" value={draft.key} placeholder="tenant" autoComplete="off"
          onChange={e => setDraft(d => ({ ...d, key: e.target.value }))} />
        <Field className="w-auto" label="Ek yazımlar (virgülle)" hint="aynı anahtarın büyük harf / eski adları" value={draft.spellings} placeholder="TENANT_ID, tenantId" autoComplete="off"
          onChange={e => setDraft(d => ({ ...d, spellings: e.target.value }))} />
        <SelectField className="w-auto min-w-[120px]" label="Kapsam" value={draft.scope}
          onChange={e => setDraft(d => ({ ...d, scope: e.target.value as 'span' | 'resource' }))}>
          <option value="span">span</option>
          <option value="resource">resource</option>
        </SelectField>
        <SelectField className="w-auto min-w-[120px]" label="Tip" value={draft.type}
          onChange={e => setDraft(d => ({ ...d, type: e.target.value as 'lc' | 'string' }))}>
          <option value="lc">LowCardinality (≤10k değer)</option>
          <option value="string">String</option>
        </SelectField>
        <Button disabled={busy || !draft.key.trim()} onClick={add}>Facet ekle</Button>
      </div>
      {/* v0.10.954 — tablo standardı T12: boş durum tablonun İÇİNDE, başlık
          durur (ayar yükleme kapısı yukarıda kalır). */}
      <div className="table-wrap">
        <DataTable<FacetRow> storageKey="trace-facets" columns={COLS} rows={rows}
          sort={{ initial: { id: 'key', dir: 'asc' } }}
          getRowKey={r => r.key}
          renderCell={renderCell}
          state={{ kind: 'empty', message: 'Kayıtlı facet yok — yerleşik terfi kolonları (channel_code, function_code, k8s.*) kodda; burası operatörün eklediği ek facet\'ler.' }} />
      </div>
      <div className="flex items-center gap-4">
        <Button variant="secondary" onClick={() => setShowSql(v => !v)} aria-expanded={showSql}>
          {showSql ? 'SQL\'i gizle' : 'Prod SQL\'ini göster'}
        </Button>
        {showSql && <CopyButton value={sql} title="SQL'i kopyala" />}
      </div>
      {showSql && (
        <pre className="m-0 overflow-x-auto whitespace-pre rounded-md border border-(--border) bg-(--bg2) p-2.5 text-[length:var(--fs-xs)]">{sql}</pre>
      )}
    </div>
  );
}
