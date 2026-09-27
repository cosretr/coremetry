import { Fragment, useCallback, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import { Badge, IconButton, LinkButton, MenuItem, Popover } from '@/components/ui';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import type { ArgoCDPin, ArgoCDTokenStatus } from '@/lib/types';
import { rowActivation } from '@/lib/a11y';
import {
  applyBuffer, domKey, hubName, instanceChanged, tokenRank, tokenState,
  type BufferErrors, type Draft, type HubDraft, type InstanceBuffer, type InstanceDraft, type Issue, type RemoteCluster, type TokenState,
} from './argocdForm';
import { ArgoCDInstanceForm } from './ArgoCDInstanceForm';

// ArgoCDInstancesPanel — v0.10.974 — Argo CD "Instance'lar" kayıt listesi
// (mockup Main; tablo standardı T1 kayıt listesi → DataTable: sıralanır,
// genişlik ayarlanır, satır tıklanabilir).
//
//   • Görünüm SIRALI (varsayılan Hub ↑), PUT gövdesi TASLAK sırası —
//     sunucunun `instances[N]` hata yolu taslağa eşlenir (argocdForm).
//   • Boş değerler (metricsJob, API URL) yöne bakmadan SONA; boş yeni satır
//     ("Instance ekle") HEP en sonda, sıralamaya girmez.
//   • Sayfa kendi `<tbody>`sini çizer: açık satırın altında satır içi form
//     (tek açık form, seçili satırın vurgu rayı, aria-expanded/-controls);
//     Kaydet'in sorunları satırın altında tam genişlik hata satırı.
//   • Tek eylem sütunu: ⋯ = Popover (menu) + MenuItem. Pin'i olan instance
//     kaldırılamaz (pin düzenleyicisi yok; pins[] API'den).
//   • v0.10.974 — menünün "Düzenle"si AÇ komutudur, aç/kapa değil (mockup
//     onEdit): form zaten o satırda açıksa yalnız menü kapanır (`onEdit`); satır
//     tıklaması ve ad düğmesi aç/kapa kalır (`onOpen`).
//   • v0.10.974 — Kaydet reddinde hatalı DEĞER de işaretlenir (States (c)):
//     sorunlu alanın hücresi `err` tonu (kolon tone()'u üzerinden — ham
//     .cell-err yazılsa sonraki .cell-muted/.cell-faint onu ezerdi) ve satırın
//     hata satırına aria-describedby.

const SEP: CSSProperties = { height: 1, margin: 'var(--sp-2) 0', background: 'var(--divider)' };

/** Sunucu/istemci alan adı → tablo kolonu (hatalı değerin hücresi). */
const FIELD_COL: Record<string, string> = {
  id: 'name', name: 'name', hubClusterId: 'hub', hubNamespace: 'ns', metricsJob: 'job', apiUrl: 'url',
  tokenRef: 'token', clearTokenRef: 'token', enabled: 'status',
};

interface TokenCell { text: string; title: string; state: TokenState }

function tokenCell(i: InstanceDraft, tokens: Record<string, ArgoCDTokenStatus>): TokenCell {
  const state = tokenState(i, tokens);
  const ref = i.tokenInput.trim() || i.storedRef;
  switch (state) {
    case 'none': return { state, text: 'yok · yalnız metrik', title: "tokenRef yok: API işçisi bu instance'ı atlar; durum yalnız metriklerden okunur." };
    case 'pending': return { state, text: 'yeni · kaydedince denetlenir', title: ref };
    case 'unresolved': return { state, text: 'çözülemedi', title: tokens[i.savedId]?.error || `${ref} çözülemedi` };
    case 'resolved': return { state, text: 'kayıtlı', title: `${ref} · çözüldü` };
    default: return { state, text: 'kayıtlı', title: ref };
  }
}

export function ArgoCDInstancesPanel({
  instances, base, hubs, clusters, tokens, pins, buffer, bufErr, pending, issues, msg,
  onOpen, onEdit, onToggle, onRemove, onBufferChange, onApply, onCancel, onRemoveBuffer,
}: {
  instances: InstanceDraft[];
  base: Draft;
  hubs: HubDraft[];
  clusters: RemoteCluster[];
  tokens: Record<string, ArgoCDTokenStatus>;
  pins: ArgoCDPin[];
  buffer: InstanceBuffer | null;
  bufErr: BufferErrors;
  pending: boolean;
  issues: Issue[];
  /** Engellenen kaldırmanın iletisi (role=alert). */
  msg: string;
  onOpen: (key: string) => void;
  /** Menünün "Düzenle"si: açık satırda formu kapatmaz. */
  onEdit: (key: string) => void;
  onToggle: (key: string) => void;
  onRemove: (key: string, focus: { ok: string; blocked: string }) => void;
  onBufferChange: (next: InstanceBuffer) => void;
  onApply: () => void;
  onCancel: () => void;
  onRemoveBuffer: () => void;
}) {
  const hn = useCallback((id: string) => hubName(id, clusters), [clusters]);
  // (satır anahtarı, hatalı kolon) çiftleri — Kaydet'in istemci/sunucu
  // sorunlarından. İmza dizgesiyle memo: `issues` her çizimde yeni dizi, kolon
  // tanımları yalnız içerik değişince yenilensin.
  const badSig = issues.flatMap(is => {
    if (is.target.kind !== 'instance') return [];
    const col = FIELD_COL[is.target.field];
    return col ? [`${is.target.key}\u0000${col}`] : [];
  }).join('\u0001');
  const badSet = useMemo(() => new Set(badSig ? badSig.split('\u0001') : []), [badSig]);
  const bad = useCallback((r: InstanceDraft, col: string) => badSet.has(`${r.key}\u0000${col}`), [badSet]);
  const columns = useMemo<ColumnDef<InstanceDraft>[]>(() => [
    { id: 'name', label: 'Instance', flex: true, minWidth: 140, naturalDir: 'asc', sortValue: r => (r.name || r.id).toLowerCase() },
    { id: 'hub', label: 'Hub', width: 90, mono: true, naturalDir: 'asc', sortValue: r => `${hn(r.hubClusterId)} ${r.id}`, tone: r => (bad(r, 'hub') ? 'err' : 'muted') },
    { id: 'ns', label: 'Namespace', width: 140, mono: true, naturalDir: 'asc', sortValue: r => `${r.hubNamespace} ${hn(r.hubClusterId)}`, tone: r => (bad(r, 'ns') ? 'err' : undefined) },
    { id: 'job', label: 'metricsJob', width: 170, mono: true, naturalDir: 'asc', sortValue: r => r.metricsJob || null, tone: r => (bad(r, 'job') ? 'err' : 'muted') },
    { id: 'url', label: 'API URL', width: 200, mono: true, naturalDir: 'asc', sortValue: r => r.apiUrl || null,
      tone: r => (bad(r, 'url') ? 'err' : r.apiUrl ? 'muted' : 'faint') },
    { id: 'token', label: 'Token', width: 120, naturalDir: 'asc',
      sortValue: r => `${tokenRank(tokenState(r, tokens))} ${r.id}`,
      tone: r => { if (bad(r, 'token')) return 'err'; const s = tokenState(r, tokens); return s === 'unresolved' ? 'err' : s === 'none' ? 'faint' : 'muted'; } },
    { id: 'status', label: 'Durum', width: 70, naturalDir: 'asc', sortValue: r => `${r.enabled ? 0 : 1} ${r.id}`, tone: r => (bad(r, 'status') ? 'err' : r.enabled ? 'muted' : 'faint') },
    { id: 'act', label: 'Eylemler', kind: 'actions', width: 48 },
  ], [hn, tokens, bad]);
  const dt = useDataTable<InstanceDraft>({
    storageKey: 'settings.argocd.instances', columns, rows: instances, initialSort: { id: 'hub', dir: 'asc' },
  });

  const [menu, setMenu] = useState<string | null>(null);
  const anchorRef = useRef<HTMLElement | null>(null);
  const closeMenu = useCallback(() => setMenu(null), []);
  const menuRow = menu ? instances.find(i => i.key === menu) : undefined;

  const blank = buffer?.isNew ? applyBuffer(buffer) : null;
  const colSpan = dt.visibleColumns.length;
  const pinCount = (id: string) => pins.filter(p => p.instanceId === id).length;

  const form = buffer && (
    <tr id="acd-inst-edit">
      <td colSpan={colSpan} className="row-detail">
        <ArgoCDInstanceForm buffer={buffer} errors={bufErr} pending={pending} hubs={hubs} clusters={clusters} tokens={tokens}
          onChange={onBufferChange} onApply={onApply} onCancel={onCancel} onRemove={onRemoveBuffer} />
      </td>
    </tr>
  );

  const renderRow = (r: InstanceDraft, isBlank: boolean): ReactNode => {
    const k = domKey(r.key);
    const open = !!buffer && buffer.key === r.key;
    const label = r.id || 'yeni instance';
    const rowIssues = issues.filter(i => i.target.kind === 'instance' && i.target.key === r.key);
    const errId = rowIssues.length ? `acd-inst-err-${k}` : undefined;
    const tag = isBlank || r.origin === 'new' ? 'yeni' : instanceChanged(r, base) ? 'değişti' : '';
    const tok = tokenCell(r, tokens);
    const errFor = (col: string) => (errId && bad(r, col) ? errId : undefined);
    return (
      <Fragment key={r.key}>
        <tr {...rowActivation(() => onOpen(r.key))} className={open ? 'row-selected' : undefined}>
          <td {...dt.cellProps(r, 'name')}>
            <span className="row gap-3">
              <LinkButton tone="muted" underline="none" id={`acd-inst-btn-${k}`} aria-expanded={open} aria-controls="acd-inst-edit"
                aria-describedby={errId} onClick={e => { e.stopPropagation(); onOpen(r.key); }}>
                {r.name && <span>{r.name}</span>}
                <span className={bad(r, 'name') ? 'mono is-err' : r.name || isBlank ? 'mono cell-faint' : 'mono'}>{label}</span>
              </LinkButton>
              {tag && <Badge tone="neutral">{tag}</Badge>}
            </span>
          </td>
          <DataTableCell dt={dt} col="hub" row={r} value={hn(r.hubClusterId)} aria-describedby={errFor('hub')} />
          <DataTableCell dt={dt} col="ns" row={r} value={r.hubNamespace} aria-describedby={errFor('ns')} />
          <DataTableCell dt={dt} col="job" row={r} value={r.metricsJob} aria-describedby={errFor('job')} />
          <DataTableCell dt={dt} col="url" row={r} value={r.apiUrl ? r.apiUrl.replace(/^https?:\/\//, '') : 'yok'} title={r.apiUrl || undefined}
            aria-describedby={errFor('url')} />
          <DataTableCell dt={dt} col="token" row={r} value={tok.text} title={tok.title} aria-describedby={errFor('token')} />
          <DataTableCell dt={dt} col="status" row={r} value={r.enabled ? 'açık' : 'kapalı'} aria-describedby={errFor('status')} />
          <td {...dt.cellProps(r, 'act')}>
            <IconButton size="xs" icon="⋯" aria-label={`${label} eylemleri`} aria-haspopup="menu" id={`acd-inst-menu-${k}`}
              aria-expanded={menu === r.key} disabled={isBlank}
              onClick={e => { e.stopPropagation(); anchorRef.current = e.currentTarget; setMenu(m => (m === r.key ? null : r.key)); }} />
          </td>
        </tr>
        {open && form}
        {errId && (
          <tr>
            <td colSpan={colSpan} id={errId} tabIndex={-1} className="td-full is-err">
              {rowIssues.map((is, ix) => (
                <div key={ix}><span className="mono">{is.path}:</span> {is.message}</div>
              ))}
            </td>
          </tr>
        )}
      </Fragment>
    );
  };

  return (
    <>
      <div className="table-wrap">
        <table {...dt.tableProps} aria-label="Argo CD instance'ları">
          <DataTableColgroup dt={dt} />
          <DataTableHead dt={dt} />
          <tbody>
            {dt.sortedRows.length === 0 && !blank
              ? <DataTableState dt={dt} kind="empty" message="Instance yok — keşifle ya da elle ekleyin." />
              : <>
                  {dt.sortedRows.map(r => renderRow(r, false))}
                  {blank && renderRow(blank, true)}
                </>}
          </tbody>
        </table>
        <Popover key={menu ?? ''} anchorRef={anchorRef} open={!!menuRow} onClose={closeMenu} kind="menu"
          ariaLabel={menuRow ? `${menuRow.id} eylemleri` : 'Instance eylemleri'} width={200}>
          {menuRow && (
            <>
              <MenuItem onClick={() => { closeMenu(); onEdit(menuRow.key); }}>Düzenle</MenuItem>
              <MenuItem onClick={() => { closeMenu(); onToggle(menuRow.key); }}>{menuRow.enabled ? 'Devre dışı bırak' : 'Etkinleştir'}</MenuItem>
              <div role="separator" style={SEP} />
              <MenuItem danger title={pinCount(menuRow.id) ? `${pinCount(menuRow.id)} pin bağlı` : undefined}
                onClick={() => { closeMenu(); onRemove(menuRow.key, { ok: 'acd-inst-add', blocked: `acd-inst-menu-${domKey(menuRow.key)}` }); }}>
                Tablodan kaldır
              </MenuItem>
            </>
          )}
        </Popover>
      </div>
      {msg && <div role="alert" className="field-error">{msg}</div>}
    </>
  );
}
