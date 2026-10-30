import { useEffect, useMemo, useState, type FormEvent } from 'react';
import { Spinner, Empty } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { ButtonGroup } from '@/components/shadcn/button-group';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { useConfirm } from '@/components/coremetry/Confirm';
import { DataTable, type ColumnDef } from '@/components/coremetry/DataTable';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/coremetry/overlay/Dialog';
import { cn } from '@/lib/cn';
import { api, type CustomRole, type AvailablePage } from '@/lib/api';

// v0.9.871 (tutarlılık denetimi BT13) — paylaşılan primitif. Kolon kümesi,
// sıra, etiketler ve hücre içerikleri AYNEN korundu; kazanılan tek şey
// sıralama + yeniden boyutlandırma + kalıcı genişlik.
// v0.10.942 (tablo standardı dilim 3) — ikincil sütun tonu ve "sayfa yok"
// uyarısı kolon bayrağında; eylem hücresi `kind: 'actions'` kolonu
// (`minWidth = width`: sığdırma onu eski sabit `trailing` gibi kilitler).
const ROLE_COLS: ColumnDef<CustomRole>[] = [
  { id: 'name',  label: 'Name',  sortValue: r => r.name,             naturalDir: 'asc', width: 220 },
  // Hücre sayfa adlarını virgülle basıyor; sıralama da GÖRÜNENE göre
  // olsun (sayıya göre sıralamak metin gösteren bir kolonda şaşırtır).
  { id: 'pages', label: 'Pages', sortValue: r => r.pages.join(', '), naturalDir: 'asc', flex: true,
    tone: r => (r.pages.length === 0 ? 'err' : 'muted') },
  { id: 'actions', label: 'Actions', kind: 'actions', width: 160, minWidth: 160 },
];

// Sonuç/hata kutusu — eski satır içi color-mix değerleri, utility olarak.
const NOTE_ERR = 'text-(--err) bg-(--err)/8 border-(--err)/30';

// ── Custom roles tab ────────────────────────────────────────────────────────
//
// Operator-defined subsets of viewer's page access. Each role names a
// set of sidebar paths the user is allowed to see; the frontend
// filters the sidebar + redirects direct-URL access via AppShell's
// custom-role guard. Custom roles ONLY apply when the user's base
// role is viewer — admin/editor get no further restriction.
//
// Page catalogue is sourced from /api/admin/pages so the checkbox grid
// stays in sync with the backend's canonical sidebar registry. A new
// page lands in the sidebar → it appears here automatically on next
// load (default-unchecked, so new features stay hidden until an admin
// opts them in).
export function CustomRolesTab() {
  const confirm = useConfirm();
  const [roles, setRoles] = useState<CustomRole[] | null | undefined>(undefined);
  const [pages, setPages] = useState<AvailablePage[] | null | undefined>(undefined);
  const [editing, setEditing] = useState<CustomRole | null>(null);
  const [creating, setCreating] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const load = () => {
    setRoles(undefined);
    Promise.all([api.listCustomRoles(), api.listAvailablePages()])
      .then(([r, p]) => {
        setRoles(r.roles ?? []);
        setPages(p.pages ?? []);
      })
      .catch(() => { setRoles(null); setPages(null); });
  };
  useEffect(load, []);

  const remove = async (name: string) => {
    if (!await confirm({
      title: 'Özel rol silinsin mi?',
      body: <><b>{name}</b> rolü silinecek. Bu role atanmış kullanıcılar
        <b> kısıtlamasız viewer</b>’a düşer — yani sayfa kısıtları kalkar.</>,
      confirmLabel: 'Rolü sil',
      danger: true,
    })) return;
    setBusy(name);
    setMsg(null);
    try {
      await api.deleteCustomRole(name);
      setMsg({ kind: 'ok', text: `Deleted "${name}"` });
      load();
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(null);
    }
  };

  if (roles === undefined || pages === undefined) return <Spinner />;
  if (roles === null || pages === null) {
    return <Empty icon="!" title="Failed to load custom roles">Reload the page.</Empty>;
  }

  const cell = (r: CustomRole, col: ColumnDef<CustomRole>) => {
    switch (col.id) {
      case 'name': return <span className="font-semibold">{r.name}</span>;
      case 'pages': return r.pages.length === 0 ? '(none — user will see no nav)' : r.pages.join(', ');
      case 'actions': return (
        <ButtonGroup aria-label={`${r.name} actions`}>
          <Button variant="secondary" size="sm" onClick={() => setEditing(r)}>Edit</Button>
          <Button variant="ghost-danger" size="sm" onClick={() => void remove(r.name)} disabled={busy === r.name}>
            Delete
          </Button>
        </ButtonGroup>
      );
      default: return null;
    }
  };

  return (
    <div>
      <div className="mb-3 flex items-baseline gap-3">
        <span className="flex-1 text-[length:var(--fs-sm)] text-(--text2)">
          Custom roles subset the <b>viewer</b> base role to a chosen set of
          pages — e.g. a "readonly-3" that only sees traces, metrics, logs.
          Admin / editor roles are unaffected.
        </span>
        <Button size="sm" onClick={() => setCreating(true)}>+ New role</Button>
      </div>

      {msg && (
        <div className={cn('mb-2.5 rounded-sm border px-2.5 py-1.5 text-[length:var(--fs-md)]', msg.kind === 'ok' ? 'text-(--ok) bg-(--ok)/8 border-(--ok)/30' : NOTE_ERR)}>
          {msg.text}
        </div>
      )}

      {/* v0.10.954 — tablo standardı T12: boş hâl tablonun İÇİNDE, başlık
          durur (rol + sayfa listesi yükleme kapısı yukarıda kalır). */}
      <div className="table-wrap">
        <DataTable<CustomRole> storageKey="settings-custom-roles" columns={ROLE_COLS} rows={roles}
          sort={{ initial: { id: 'name', dir: 'asc' } }} getRowKey={r => r.name} renderCell={cell}
          state={{ kind: 'empty', message: "Henüz özel rol yok — bir viewer'a sayfaların yalnız bir alt kümesini açmak için bir tane oluştur." }} />
      </div>

      {(creating || editing) && (
        <RoleEditorModal
          existing={editing}
          pages={pages}
          onClose={() => { setCreating(false); setEditing(null); }}
          onSaved={() => { setCreating(false); setEditing(null); load(); }}
        />
      )}
    </div>
  );
}

function RoleEditorModal({ existing, pages, onClose, onSaved }: {
  existing: CustomRole | null;
  pages: AvailablePage[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(existing?.name ?? '');
  const [selected, setSelected] = useState<Set<string>>(() => new Set(existing?.pages ?? []));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Group pages by their group key so the checkbox grid mirrors
  // the sidebar's grouping — easier to scan than a flat list.
  const byGroup = useMemo(() => {
    const m = new Map<string, AvailablePage[]>();
    for (const p of pages) {
      const k = p.group || '_ungrouped';
      const arr = m.get(k) ?? [];
      arr.push(p);
      m.set(k, arr);
    }
    return m;
  }, [pages]);

  const toggle = (id: string) => {
    setSelected(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      await api.upsertCustomRole({ name: name.trim(), pages: [...selected] });
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  // Faz 2 dilim 1 R10 (D8) — ui/Modal → coremetry Dialog (480px = eski `md`).
  return (
    <Dialog open onOpenChange={o => { if (!o) onClose(); }}>
      <DialogContent className="sm:max-w-[480px]" aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle>{existing ? `Edit role — ${existing.name}` : 'New custom role'}</DialogTitle>
        </DialogHeader>
        <form id="role-form" onSubmit={submit} className="flex min-w-0 flex-col gap-3">
          <Field label="Role name" value={name} onChange={e => setName(e.target.value)} required
            disabled={!!existing} placeholder="e.g. readonly-3, sre-readonly, audit-only"
            hint="Cannot be admin/editor/viewer." />
          <div>
            <div className="mb-1.5 text-[length:var(--fs-sm)] text-(--text2)">
              Pages this role can see ({selected.size} selected)
            </div>
            <div className="max-h-80 overflow-y-auto rounded-sm border border-(--border) p-2.5">
              {[...byGroup.entries()].map(([g, items]) => (
                <div key={g} className="mb-2">
                  {g !== '_ungrouped' && (
                    <div className="mb-1 text-[length:var(--fs-xs)] font-bold tracking-[0.6px] text-(--text3) uppercase">
                      {g.replace('navGroup.', '')}
                    </div>
                  )}
                  {items.map(p => (
                    <CheckboxField key={p.id} className="px-1 py-[3px]"
                      checked={selected.has(p.id)} onCheckedChange={() => toggle(p.id)}
                      label={<>
                        <code className="text-[length:var(--fs-xs)] text-(--text3)">{p.id}</code>{' '}
                        <span className="text-[length:var(--fs-md)] text-(--text)">{p.label.replace('nav.', '')}</span>
                      </>} />
                  ))}
                </div>
              ))}
            </div>
          </div>
          {error && (
            <div role="alert" className={cn('rounded-sm border px-2 py-1 text-[length:var(--fs-sm)]', NOTE_ERR)}>{error}</div>
          )}
        </form>
        <DialogFooter>
          <Button variant="secondary" size="sm" onClick={onClose}>Cancel</Button>
          <Button size="sm" type="submit" form="role-form" loading={busy}>Save</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
