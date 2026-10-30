import { useCallback, useMemo, type ReactNode } from 'react';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { DataTable, type ColumnDef, type MenuItemSpec } from '@/components/coremetry/DataTable';
import type { ArgoCDTokenStatus } from '@/lib/types';
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
//   • Faz 2 dilim 1 R15 — useDataTable + elle tbody → coremetry/DataTable
//     (dondurulmuş API'nin R15 ekleriyle, operatör onayı 2026-10-30): ⋯
//     paylaşılan Popover + MenuItem → `rowActions` (tek DropdownMenu; satır
//     başına ad "<id> eylemleri" + id `acd-inst-menu-<k>` — engellenen
//     kaldırma odağı oraya döndürür), boş yeni satır → `pinnedRows` (sıralama
//     dışı, ⋯ devre dışı), açık satır → `openRow` (row-selected + satır tıkı /
//     Enter aç-kapa), satır içi form + hata satırı → TEK `detail` hücresi
//     (form `#acd-inst-edit`, hata bloğu `#acd-inst-err-<k>`). Hücre
//     `aria-describedby`/`title` hücre içeriğinin span'inde. Menüdeki ayırıcı
//     çizgi düştü (MenuItemSpec'te ayırıcı yok); "N pin bağlı" ipucu kaldırma
//     reddinin iletisinde zaten yazıyor.

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
  instances, base, hubs, clusters, tokens, buffer, bufErr, pending, issues, msg,
  onOpen, onEdit, onToggle, onRemove, onBufferChange, onApply, onCancel, onRemoveBuffer,
}: {
  instances: InstanceDraft[];
  base: Draft;
  hubs: HubDraft[];
  clusters: RemoteCluster[];
  tokens: Record<string, ArgoCDTokenStatus>;
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
  ], [hn, tokens, bad]);

  const blank = buffer?.isNew ? applyBuffer(buffer) : null;
  const isBlank = (r: InstanceDraft) => !!blank && r.key === blank.key;
  const rowIssues = (r: InstanceDraft) => issues.filter(i => i.target.kind === 'instance' && i.target.key === r.key);
  const errIdOf = (r: InstanceDraft) => (rowIssues(r).length ? `acd-inst-err-${domKey(r.key)}` : undefined);
  const isOpen = (r: InstanceDraft) => !!buffer && buffer.key === r.key;

  const cell = (r: InstanceDraft, col: ColumnDef<InstanceDraft>): ReactNode => {
    const errId = errIdOf(r);
    const errFor = (c: string) => (errId && bad(r, c) ? errId : undefined);
    const text = (v: string, title?: string) => (
      <span aria-describedby={errFor(col.id)} title={title ?? (v || undefined)}>{v || <span className="cell-empty">—</span>}</span>
    );
    switch (col.id) {
      case 'name': {
        const k = domKey(r.key);
        const open = isOpen(r);
        const nb = isBlank(r);
        const label = r.id || 'yeni instance';
        const tag = nb || r.origin === 'new' ? 'yeni' : instanceChanged(r, base) ? 'değişti' : '';
        return (
          <span className="inline-flex items-center gap-3">
            <Button variant="link" size="xs" className="h-auto gap-1.5 p-0 text-(--text2) no-underline hover:no-underline"
              id={`acd-inst-btn-${k}`} aria-expanded={open} aria-controls="acd-inst-edit"
              aria-describedby={errId} onClick={e => { e.stopPropagation(); onOpen(r.key); }}>
              {r.name && <span>{r.name}</span>}
              <span className={bad(r, 'name') ? 'font-mono text-status-critical' : r.name || nb ? 'font-mono text-(--text3)' : 'font-mono'}>{label}</span>
            </Button>
            {tag && <Badge variant="secondary">{tag}</Badge>}
          </span>
        );
      }
      case 'hub': return text(hn(r.hubClusterId));
      case 'ns': return text(r.hubNamespace);
      case 'job': return text(r.metricsJob);
      case 'url': return text(r.apiUrl ? r.apiUrl.replace(/^https?:\/\//, '') : 'yok', r.apiUrl || '');
      case 'token': { const tok = tokenCell(r, tokens); return text(tok.text, tok.title); }
      case 'status': return text(r.enabled ? 'açık' : 'kapalı');
      default: return null;
    }
  };

  // Açık satırın altında satır içi form; Kaydet'in sorunları satırın altında
  // tam genişlik hata bloğu — ikisi aynı ayrıntı hücresinde, bu sırayla.
  const detail = (r: InstanceDraft): ReactNode => {
    const ri = rowIssues(r);
    const errId = errIdOf(r);
    return (
      <>
        {isOpen(r) && buffer && (
          <div id="acd-inst-edit">
            <ArgoCDInstanceForm buffer={buffer} errors={bufErr} pending={pending} hubs={hubs} clusters={clusters} tokens={tokens}
              onChange={onBufferChange} onApply={onApply} onCancel={onCancel} onRemove={onRemoveBuffer} />
          </div>
        )}
        {errId && (
          <div id={errId} tabIndex={-1} className={isOpen(r) ? 'mt-3 text-status-critical' : 'text-status-critical'}>
            {ri.map((is, ix) => (
              <div key={ix}><span className="font-mono">{is.path}:</span> {is.message}</div>
            ))}
          </div>
        )}
      </>
    );
  };

  const items = (r: InstanceDraft): MenuItemSpec[] => [
    { id: 'edit', label: 'Düzenle', onSelect: () => onEdit(r.key) },
    { id: 'toggle', label: r.enabled ? 'Devre dışı bırak' : 'Etkinleştir', onSelect: () => onToggle(r.key) },
    { id: 'remove', label: 'Tablodan kaldır', danger: true,
      onSelect: () => onRemove(r.key, { ok: 'acd-inst-add', blocked: `acd-inst-menu-${domKey(r.key)}` }) },
  ];

  return (
    <>
      <div className="table-wrap">
        <DataTable<InstanceDraft> storageKey="settings.argocd.instances" columns={columns} rows={instances}
          ariaLabel="Argo CD instance'ları" sort={{ initial: { id: 'hub', dir: 'asc' } }}
          getRowKey={r => r.key} renderCell={cell}
          pinnedRows={blank ? [blank] : undefined}
          openRow={{ isOpen, onActivate: r => onOpen(r.key) }}
          detail={{ isOpen: r => isOpen(r) || !!errIdOf(r), render: detail, cellClassName: r => (isOpen(r) ? 'row-detail' : 'td-full') }}
          rowActions={{
            label: 'Instance eylemleri', items,
            triggerLabel: r => `${r.id || 'yeni instance'} eylemleri`,
            triggerId: r => `acd-inst-menu-${domKey(r.key)}`,
            isDisabled: isBlank,
          }}
          state={{ kind: 'empty', message: 'Instance yok — keşifle ya da elle ekleyin.' }} />
      </div>
      {msg && <div role="alert" className="text-[length:var(--fs-xs)] text-status-critical">{msg}</div>}
    </>
  );
}
