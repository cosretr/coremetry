import { useEffect, useState, type FormEvent } from 'react';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { useConfirm } from '@/components/coremetry/Confirm';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/coremetry/overlay/Dialog';
import { cn } from '@/lib/cn';
import { api, type MaintenanceWindow } from '@/lib/api';
import { tsLong } from '@/lib/utils';
import { formatDateTime, parseDateTime } from '@/lib/rangePicker';

// v0.9.871 (tutarlılık denetimi MT7) — 30 günlük pencere listesi paylaşılan
// primitife geçti. Kolon kümesi/sıra/etiket AYNEN korundu.
//
// Durum rozetinin mantığı tek yere alındı: hem hücre hem sortValue buradan
// okuyor. Önceden `active`/`upcoming` satır içinde türetiliyordu ve sıralama
// diye bir şey yoktu; ikisini ayrı yazmak, rozetin gösterdiğiyle sıralamanın
// ayrışacağı klasik zemin.
type WindowStatus = 'disabled' | 'active' | 'upcoming' | 'past';

export function maintenanceStatus(w: MaintenanceWindow, nowNs: number): WindowStatus {
  if (w.disabled) return 'disabled';
  if (w.startAt <= nowNs && nowNs <= w.endAt) return 'active';
  if (w.startAt > nowNs) return 'upcoming';
  return 'past';
}

// Aciliyet sırası: şu an susturan pencere en üstte.
const STATUS_RANK: Record<WindowStatus, number> = { active: 3, upcoming: 2, past: 1, disabled: 0 };

// Kolonlar MODÜL kapsamında: `now` her render'da değişiyor ve kolonları
// ona bağlamak columnLayoutSig'i her render'da tazeleyip operatörün
// sürüklediği genişlikleri atardı. `now` yalnız sortValue'nun İÇİNDE,
// sıralama anında okunuyor.
// v0.10.942 (tablo standardı dilim 3) — hücre görünümü kolon bayraklarında
// (11px ikincil metin → ton, S3); eylem hücresi `kind: 'actions'` kolonu,
// `minWidth = width` onu eski sabit `trailing` gibi kilitler.
const WINDOW_COLS: ColumnDef<MaintenanceWindow>[] = [
  { id: 'service',  label: 'Service',  sortValue: w => w.service,        naturalDir: 'asc', width: 180, mono: true },
  { id: 'severity', label: 'Severity', sortValue: w => w.severity,       naturalDir: 'asc', width: 110, tone: () => 'muted' },
  { id: 'starts',   label: 'Starts',   sortValue: w => w.startAt,        width: 175, tone: () => 'muted' },
  { id: 'ends',     label: 'Ends',     sortValue: w => w.endAt,          width: 175, tone: () => 'muted' },
  { id: 'reason',   label: 'Reason',   sortValue: w => w.reason ?? '',   naturalDir: 'asc', flex: true, tone: () => 'muted' },
  { id: 'by',       label: 'By',       sortValue: w => w.createdBy ?? '', naturalDir: 'asc', width: 150, mono: true, tone: () => 'faint' },
  { id: 'status',   label: 'Status',
    sortValue: w => STATUS_RANK[maintenanceStatus(w, Date.now() * 1e6)], width: 110 },
  { id: 'actions',  label: 'Actions',  kind: 'actions', width: 150, minWidth: 150 },
];

// v0.10.929 (K5) — DISABLED/PAST geçmiş kayıt: nötr (eskiden kırmızı/yeşil).
// ACTIVE amber kalır: şu an uyarıları susturan pencere bir sapma.
// Faz 2 dilim 1 R10 — `.badge b-*` + satır içi 9px → StatusBadge `sm`.
const STATUS_BADGE: Record<WindowStatus, { status: 'unknown' | 'warn' | 'info'; label: string }> = {
  disabled: { status: 'unknown', label: 'DISABLED' },
  active:   { status: 'warn',    label: 'ACTIVE' },
  upcoming: { status: 'info',    label: 'UPCOMING' },
  past:     { status: 'unknown', label: 'PAST' },
};

// Sonuç/hata kutusu — eski satır içi color-mix değerleri, utility olarak.
const NOTE_ERR = 'text-(--err) bg-(--err)/8 border-(--err)/30';

// ── Maintenance windows tab ────────────────────────────────────────────────
//
// Operator-declared time ranges that suppress alert
// notifications for matching (service, severity) tuples.
// Problems still open + auto-resolve as usual — only the
// live channel fan-out (Slack / email / Zoom / etc.) is
// skipped. After the window expires the /anomalies +
// /incidents pages still show the full timeline.

export function MaintenanceTab() {
  const confirm = useConfirm();
  const [items, setItems] = useState<MaintenanceWindow[] | null | undefined>(undefined);
  const [showAll, setShowAll] = useState(false);
  const [creating, setCreating] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const load = () => {
    setItems(undefined);
    api.listMaintenanceWindows(showAll)
      .then(r => setItems(r ?? []))
      .catch(() => setItems(null));
  };
  useEffect(load, [showAll]);

  const del = async (id: string) => {
    if (!await confirm({
      title: 'Bakım penceresi silinsin mi?',
      body: <>Pencere kaldırılacak ve bastırılan uyarılar <b>ANINDA</b>
        yeniden ateşlemeye başlayacak.</>,
      confirmLabel: 'Pencereyi sil',
      danger: true,
    })) return;
    try {
      await api.deleteMaintenanceWindow(id);
      setMsg({ kind: 'ok', text: 'Window removed' });
      load();
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : 'Delete failed' });
    }
  };

  const now = Date.now() * 1e6;
  // v0.10.954 — tablo standardı T12: yükleniyor / hata / boş tablonun
  // İÇİNDE, başlık durur; ↻ aynı `load`. v0.9.865 (MT1): hata satırı "boş
  // liste değil" uyarısını ve en pahalı sonucu (aktif pencere hâlâ
  // uyarıları susturuyor olabilir) taşımaya devam eder.
  const tableState: DataTableStateInput<MaintenanceWindow> =
    items === undefined ? { kind: 'loading' }
    : items === null ? {
        kind: 'error', onRetry: load,
        message: 'Bakım pencereleri okunamadı — bu bir hata, boş liste değil. Aktif bir pencere uyarıları hâlâ susturuyor olabilir.',
      }
    : {
        kind: 'empty',
        message: 'Bakım penceresi yok — planlı bir deploy öncesi etkilenen servislerde uyarıları susturmak için bir pencere tanımla. '
          + 'Pencereler kendiliğinden sona erer, temizlik gerekmez.',
      };

  const cell = (w: MaintenanceWindow, col: ColumnDef<MaintenanceWindow>) => {
    switch (col.id) {
      case 'service': return <span className="font-semibold">{w.service}</span>;
      // v0.10.942 — büyük harf satır içi textTransform yerine değerde (ASCII enum, lang=en: aynı glifler).
      case 'severity': return <span className="font-mono">{w.severity.toUpperCase()}</span>;
      case 'starts': return <span className="font-mono">{tsLong(w.startAt)}</span>;
      case 'ends': return <span className="font-mono">{tsLong(w.endAt)}</span>;
      case 'reason': return w.reason;
      case 'by': return w.createdBy;
      case 'status': {
        const b = STATUS_BADGE[maintenanceStatus(w, now)];
        return <StatusBadge status={b.status} size="sm">{b.label}</StatusBadge>;
      }
      case 'actions': return !w.disabled && (
        <Button variant="ghost-danger" size="sm" onClick={() => { void del(w.id); }}>End / delete</Button>
      );
      default: return null;
    }
  };

  return (
    <div>
      <div className="mb-3 text-[length:var(--fs-sm)] text-(--text2)">
        While an active window matches a problem's <code>(service, severity)</code>,
        the live channel fan-out is suppressed. Problems still open + auto-resolve
        so the post-window review on <code>/anomalies</code> + <code>/incidents</code>
        is intact. Service supports <code>*</code> (all), an exact name, or a
        <code>name*</code> prefix.
      </div>
      <div className="controls mb-3">
        <Button size="sm" onClick={() => setCreating(true)}>+ New maintenance window</Button>
        <CheckboxField className="ml-auto text-(--text2)" checked={showAll} onCheckedChange={setShowAll}
          label="Show past / disabled (last 30d)" />
      </div>
      {msg && (
        <div className={cn('mb-2.5 rounded-sm border px-2.5 py-1.5 text-[length:var(--fs-sm)]', msg.kind === 'ok' ? 'text-(--ok) bg-(--ok)/10 border-(--ok)/35' : NOTE_ERR)}>
          {msg.text}
        </div>
      )}
      <div className="table-wrap">
        <DataTable<MaintenanceWindow> storageKey="settings-maintenance-windows" columns={WINDOW_COLS} rows={items ?? []}
          sort={{ initial: { id: 'starts', dir: 'desc' } }} state={tableState}
          getRowKey={w => w.id} renderCell={cell} />
      </div>
      {creating && (
        <NewMaintenanceModal onClose={() => setCreating(false)}
          onCreated={() => { setCreating(false); load(); setMsg({ kind: 'ok', text: 'Window created' }); }} />
      )}
    </div>
  );
}

function NewMaintenanceModal({ onClose, onCreated }: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const [service, setService] = useState('*');
  const [severity, setSeverity] = useState('*');
  // Default to "right now → +60 min", local zone.
  // v0.9.879 — these were `<input type="datetime-local">`, whose time half
  // renders with an AM/PM segment on an en-US browser; the operator wants
  // 24-hour everywhere and `lang` is not a reliable override in Chrome.
  // Hand-drawn text fields now, on the SAME "YYYY-MM-DD HH:mm:ss" grammar
  // (and the same parser) as the global time picker's absolute inputs.
  // Trade accepted per brief: no native drop-down calendar here.
  const [startAt, setStartAt] = useState(() => formatDateTime(Date.now()));
  const [endAt, setEndAt] = useState(() => formatDateTime(Date.now() + 60 * 60_000));
  const [reason, setReason] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      const startMs = parseDateTime(startAt);
      const endMs = parseDateTime(endAt);
      if (startMs === null || endMs === null) {
        throw new Error('Invalid date — expected YYYY-MM-DD HH:mm:ss');
      }
      if (endMs <= startMs) throw new Error('End must be after start');
      await api.createMaintenanceWindow({
        service: service.trim() || '*',
        severity: severity || '*',
        startAt: startMs * 1e6,
        endAt: endMs * 1e6,
        reason: reason.trim(),
      });
      onCreated();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Create failed');
    } finally {
      setBusy(false);
    }
  };

  // Faz 2 dilim 1 R10 (D8) — ui/Modal → coremetry Dialog (480px = eski `md`);
  // başlık, × kapat, odak tuzağı, Esc yığını Dialog'un.
  return (
    <Dialog open onOpenChange={o => { if (!o) onClose(); }}>
      <DialogContent className="sm:max-w-[480px]" aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>New maintenance window</DialogTitle>
        </DialogHeader>
        <form id="new-mw-form" onSubmit={submit} className="flex flex-col gap-3">
          <Field label='Service ("*" for global · exact name · "payment*" prefix)' required value={service}
            onChange={e => setService(e.target.value)} />
          <SelectField label="Severity" value={severity} onChange={e => setSeverity(e.target.value)}>
            <option value="*">All severities</option>
            <option value="info">info only</option>
            <option value="warning">warning only</option>
            <option value="critical">critical only</option>
          </SelectField>
          <div className="flex gap-3">
            <Field label="Starts at" className="flex-1" controlClassName="font-mono" type="text" required value={startAt}
              spellCheck={false} placeholder="2026-08-10 14:00:00" inputMode="numeric"
              aria-invalid={parseDateTime(startAt) === null}
              onChange={e => setStartAt(e.target.value)} />
            <Field label="Ends at" className="flex-1" controlClassName="font-mono" type="text" required value={endAt}
              spellCheck={false} placeholder="2026-08-10 15:00:00" inputMode="numeric"
              aria-invalid={parseDateTime(endAt) === null}
              onChange={e => setEndAt(e.target.value)} />
          </div>
          <Field label='Reason (optional) — e.g. "deploy payment-api v2.34"' value={reason}
            onChange={e => setReason(e.target.value)} />
          {error && (
            <div role="alert" className={cn('rounded-sm border p-2 text-[length:var(--fs-sm)]', NOTE_ERR)}>{error}</div>
          )}
        </form>
        <DialogFooter>
          <Button variant="secondary" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" type="submit" form="new-mw-form" loading={busy}>Create</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
