import { useState, type FormEvent } from 'react';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { SelectField } from '@/components/coremetry/Field';
import { DataTableStateRow, type DataTableStateStaticProps } from '@/components/coremetry/DataTable';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/coremetry/overlay/Dialog';
import { api } from '@/lib/api';
import type { LDAPDirectoryUser, Role } from '@/lib/types';

// LDAPUserPicker — admin types a name/email, hits Search, picks a
// directory entry, picks a role, hits Provision. Pre-creates the
// users row so first-login lands them with the right access without
// having to wait for the group mapping to apply.
// Faz 2 dilim 1 R10 — shadcn Input/Table/Button + coremetry Dialog (D8: elle
// kurulmuş sabit-zeminli pencere gerçek dialog oldu — role=dialog, başlık,
// × kapat, odak tuzağı, Esc yığını, kaydırma kilidi). Kolonlar, metinler aynı.
export function LDAPUserPicker() {
  const [q, setQ] = useState('');
  const [results, setResults] = useState<LDAPDirectoryUser[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [provisionFor, setProvisionFor] = useState<LDAPDirectoryUser | null>(null);
  const [role, setRole] = useState<Role>('viewer');
  const [provisionMsg, setProvisionMsg] = useState<string | null>(null);
  // v0.10.967 — sonucun ait olduğu arama metni (kutu sonradan değişebilir):
  // sıfır sonuç, bir terimle arandıysa "eşleşme yok", boş terimle "dizin boş".
  const [searched, setSearched] = useState('');

  const search = async (e?: FormEvent) => {
    if (e) e.preventDefault();
    setSearched(q);
    setBusy(true); setError(null); setResults(null);
    try {
      const r = await api.searchLDAPUsers(q, 25);
      setResults(r.users ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Search failed');
    } finally {
      setBusy(false);
    }
  };

  const provision = async () => {
    if (!provisionFor) return;
    const email = provisionFor.email || provisionFor.username;
    if (!email) return;
    setBusy(true); setProvisionMsg(null);
    try {
      await api.provisionLDAPUser(email, role);
      setProvisionMsg(`Provisioned ${email} as ${role}.`);
      setProvisionFor(null);
    } catch (err) {
      setProvisionMsg(err instanceof Error ? err.message : 'Provision failed');
    } finally {
      setBusy(false);
    }
  };

  const showRows = error === null && !!results && results.length > 0;
  // Sıra eski paragraflarla aynı: hata önce, sonra sıfır sonuç. Aranan
  // terim kullanıcının süzgeci (sunucu tarafı) → "Eşleşme yok" (eski
  // "No matches."); tek temizle eylemi yok (kutuyu boşaltmak aramaz).
  const searchState: Omit<DataTableStateStaticProps, 'colSpan'> =
    error !== null ? { kind: 'error', message: `Dizin araması başarısız: ${error}` }
    : searched.trim() ? { kind: 'no-match' }
    : { kind: 'empty', message: 'Dizinde kullanıcı bulunamadı' };

  return (
    <div className="mt-[18px] rounded-lg border border-(--border) bg-(--bg2) p-4">
      <h3 className="mb-1.5 text-[length:var(--fs-md)] font-semibold">Pre-provision a directory user</h3>
      <p className="mb-3 text-[length:var(--fs-xs)] text-(--text3)">
        Find a user in your LDAP, pick a role, and we'll create their Coremetry
        row up-front. They keep that role even if their AD groups would map them
        to a different one — useful for "trust this person specifically" cases.
      </p>
      <form onSubmit={search} className="mb-3 flex gap-2">
        <Input value={q} onChange={e => setQ(e.target.value)} aria-label="Name, email or username"
          placeholder="Name, email or username"
          className="h-8 flex-1 border-(--border-control) bg-field text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field" />
        <Button type="submit" size="sm" loading={busy}>Search</Button>
      </form>
      {/* v0.10.967 — tablo standardı dilim 5 (P-2): arama hatası (kırmızı
          satır) ve "No matches." paragrafı tablonun İÇİNE indi, başlık durur.
          Kapı aynı: tablo ilk arama sonuçlanınca çizilir (öncesi girdi
          bekleyen bir seçici; aranırken sonuçlar sıfırlanıyor, gösterge
          Search düğmesinin kendisi). Hata ve sonuç birlikte olamaz: arama
          ikisini de sıfırlayarak başlar. */}
      {(error !== null || results !== null) && (
        // v0.10.942 — statik tablo: seçici liste (≤25 arama sonucu, Provision ile seçilir; T1).
        // Faz 2 dilim 1 R10 — shadcn Table (satır ritmi --row-h, yoğunlukla); kolonlar aynı.
        <Table className="tabular-nums">
          <TableHeader>
            <TableRow className="bg-(--bg) text-(--text2)">
              <TableHead>Name</TableHead>
              <TableHead>Username</TableHead>
              <TableHead>Email</TableHead>
              <TableHead className="col-actions w-[100px]" aria-label="Eylemler" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {/* v0.10.967 (P-2) — statik tablonun durum satırı; colSpan = thead'deki 4 <th>. */}
            {!showRows ? <DataTableStateRow colSpan={4} {...searchState} /> : (results ?? []).map(u => (
              <TableRow key={u.dn}>
                <TableCell>{u.displayName || '—'}</TableCell>
                <TableCell><code>{u.username}</code></TableCell>
                <TableCell>{u.email || '—'}</TableCell>
                <TableCell className="col-actions">
                  <Button variant="secondary" size="sm" type="button"
                          onClick={() => setProvisionFor(u)}>
                    Provision
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {/* v0.9.988 (D6.2) — görüntü alanına kelepçeli genişlik: Dialog'un
          max-w-[calc(100%-2rem)] tabanı + 380px tavan. */}
      <Dialog open={provisionFor !== null} onOpenChange={o => { if (!o) setProvisionFor(null); }}>
        <DialogContent className="max-h-[calc(100vh-32px)] overflow-auto sm:max-w-[380px]">
          {provisionFor && (
            <>
              <DialogHeader>
                <DialogTitle>Provision LDAP user</DialogTitle>
                <DialogDescription asChild>
                  <div className="text-[length:var(--fs-sm)] text-(--text2)">
                    <div>{provisionFor.displayName || provisionFor.username}</div>
                    <div className="text-(--text3)">{provisionFor.email || provisionFor.dn}</div>
                  </div>
                </DialogDescription>
              </DialogHeader>
              <SelectField label="Role" value={role} onChange={e => setRole(e.target.value as Role)}>
                <option value="viewer">Viewer (read only)</option>
                <option value="editor">Editor (dashboards / monitors / alerts)</option>
                <option value="admin">Admin (full access)</option>
              </SelectField>
              <DialogFooter>
                <Button type="button" variant="secondary" size="sm" onClick={() => setProvisionFor(null)}>Cancel</Button>
                <Button type="button" size="sm" onClick={() => { void provision(); }} loading={busy}>
                  Provision
                </Button>
              </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>
      {provisionMsg && (
        <div className="mt-2.5 text-[length:var(--fs-sm)] text-(--ok)">{provisionMsg}</div>
      )}
    </div>
  );
}
