import { useEffect, useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { NativeSelect } from '@/components/shadcn/native-select';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { Field, SelectField, TextareaField, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { SectionHead } from '@/components/coremetry/SectionHead';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import type { LDAPConfig, LDAPDirectoryAttrs, LDAPGroupRoleMapping, LDAPGroupSyncConfig, LDAPGroupSyncPreview, LDAPGroupSyncSummary, Role } from '@/lib/types';
import { tsLong } from '@/lib/utils';
import { FlashBox, useSettingsLoad, SettingsLoadError } from './shared';
import { LDAPUserPicker } from './LdapUserPicker';

// LDAPTab — enterprise auth configuration. Three sections:
//   1. Connection — host/port/TLS/bind credentials (Test button verifies
//      the service-account bind without saving).
//   2. Group → Role mapping — admin lists AD groups whose members
//      should land as admin / editor / viewer in Coremetry. First
//      match wins (admin > editor > viewer).
//   3. Directory user picker — search the LDAP for a user, pick a
//      role, post to /api/users/from-ldap to pre-provision them so
//      they show up in /users with explicit role even before first
//      sign-in.
//
// Bind password is never echoed back from the API; the GET endpoint
// returns the literal "__SET__" sentinel when one is saved, which we
// translate into a "leave empty to keep current" affordance.
//
// Faz 2 dilim 1 R10 — shadcn/coremetry bileşikleri: shared `Field2` →
// coremetry Field (`small` = 180px, geniş alan satırda esner), `SectionTitle`
// → SectionHead (açıklama paragrafı `description`), statik tablolar shadcn
// Table + tabular-nums (D6). Alan sırası, etiketler, ipuçları, kaydetme
// gövdesi aynı (secret sözleşmesi: LdapTab.secret.test).

// Alan satırı (eski shared `Row`: sarar, 12px aralık) + esneyen geniş alan
// (eski Field2: flex 1, en az 200px).
const ROW = 'mb-3 flex flex-wrap items-start gap-3';
const GROW = 'min-w-[200px] flex-1';
// Tablo hücresindeki çıplak denetimler — Field anatomisinin denetim sınıfı (D1).
const CELL_CTL = 'border-(--border-control) bg-field text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field';
const MONO_XS = 'font-mono text-[length:var(--fs-xs)] md:text-[length:var(--fs-xs)]';
const CARD = 'rounded-lg border border-(--border) bg-(--bg2) p-4';

export function LDAPTab() {
  const [cfg, setCfg] = useState<LDAPConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  // Track whether the bind password input has been touched. We wipe
  // the "__SET__" sentinel on first focus so the user types a fresh
  // value into an empty box, not on top of the placeholder.
  const [pwTouched, setPwTouched] = useState(false);

  // v0.9.1043 — C4'ün dokuzuncu yazımı (ajan bulgusu). Eski catch
  // `setCfg(emptyLDAP())` yapıyordu: GET hatasında boş-ama-truthy config
  // `if (!cfg)` kapısını geçiyor, form BOŞ çiziliyor ve Kaydet aktif
  // kalıyordu. Kaydet bütün satırı değiştirir (ldap.go PutSettings —
  // yalnız BindPassword korunur) ve `enabled:false` yazar: tek tıkla
  // host/baseDN/groupRoleMap silinip LDAP org genelinde sessizce
  // kapanırdı. v0.9.938 kapısının aynısı: hata dalında form hiç çizilmez.
  const { error: loadErr, retry } = useSettingsLoad(
    () => api.getLDAPSettings(),
    c => setCfg(c),
  );

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!cfg) return <Spinner />;

  const update = (patch: Partial<LDAPConfig>) => setCfg({ ...cfg, ...patch });

  // Strip the "__SET__" sentinel before any outbound call. Server
  // treats empty BindPassword as "preserve saved one".
  const outbound = (): LDAPConfig => {
    const c = { ...cfg };
    if (!pwTouched && c.bindPassword === '__SET__') c.bindPassword = '';
    return c;
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      const next = await api.putLDAPSettings(outbound());
      setCfg(next);
      setPwTouched(false);
      setMsg({ kind: 'ok', text: 'Saved.' });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Save failed' });
    } finally {
      setBusy(false);
    }
  };

  const test = async () => {
    setBusy(true); setMsg(null);
    try {
      const r = await api.testLDAPConnection(outbound());
      if (r.ok) setMsg({ kind: 'ok', text: 'Connection OK — bind succeeded.' });
      else setMsg({ kind: 'err', text: r.error || 'Connection failed' });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Test failed' });
    } finally {
      setBusy(false);
    }
  };

  const pwStored = cfg.bindPassword === '__SET__' && !pwTouched;

  return (
    <div className="max-w-[800px]">
      <h2 className="mb-1.5 text-[length:var(--fs-lg)] font-semibold">LDAP / Active Directory</h2>
      <p className="mb-4 text-[length:var(--fs-md)] text-(--text2)">
        Authenticate users against your corporate directory. Domain users sign in
        with their normal credentials; their Coremetry role is resolved from AD
        group membership via the mapping table below. Local accounts (bootstrap
        admin) keep working as a fallback.
      </p>

      <form onSubmit={save} className={CARD}>
        {/* ── Section 1: Enable + connection ─────────────────────────── */}
        <CheckboxField className="mb-3.5" checked={cfg.enabled} onCheckedChange={v => update({ enabled: v })}
          label={<span className="text-[length:var(--fs-md)] font-semibold">Enable LDAP authentication</span>} />

        <SectionHead title="Connection" />
        <div className={ROW}>
          <Field label="Host" hint="e.g. ldap.corp.example.com" className={GROW}
            value={cfg.host} onChange={e => update({ host: e.target.value })} placeholder="ldap.example.com" />
          <Field label="Port" hint="636 for LDAPS, 389 for plain/StartTLS" small
            type="number" value={cfg.port || ''}
            onChange={e => update({ port: parseInt(e.target.value, 10) || 0 })} placeholder="636" />
        </div>
        <div className={ROW}>
          <CheckboxField checked={cfg.useTLS}
            onCheckedChange={v => update({ useTLS: v, startTLS: v ? false : cfg.startTLS })}
            label="Use LDAPS (direct TLS, default port 636)" />
          <CheckboxField checked={cfg.startTLS} disabled={cfg.useTLS}
            onCheckedChange={v => update({ startTLS: v })}
            label="StartTLS upgrade (legacy, port 389)" />
          <CheckboxField checked={cfg.skipVerify}
            onCheckedChange={v => update({ skipVerify: v })}
            label="Skip cert verification (dev only)" />
        </div>
        <TextareaField label="Internal CA certificate (PEM, optional)" className="mb-3"
          hint="Paste the PEM bundle if your AD uses an internal CA. Leave empty to use system roots."
          value={cfg.caCert || ''} onChange={e => update({ caCert: e.target.value })}
          rows={3} controlClassName={MONO_XS} placeholder="-----BEGIN CERTIFICATE-----..." />

        <div className={ROW}>
          <Field label="Bind DN (service account)" hint="Used to look up users + groups" className={GROW}
            value={cfg.bindDN} onChange={e => update({ bindDN: e.target.value })}
            placeholder="CN=svc-coremetry,OU=Service Accounts,DC=corp,DC=example" />
        </div>
        <div className={ROW}>
          <Field label="Bind password" className={GROW}
            hint={pwStored
              ? 'A password is saved. Leave empty to keep it; type a new one to rotate.'
              : 'Service-account password.'}
            type="password"
            value={pwTouched ? cfg.bindPassword : (cfg.bindPassword === '__SET__' ? '' : cfg.bindPassword)}
            onFocus={() => { if (!pwTouched && cfg.bindPassword === '__SET__') { setPwTouched(true); update({ bindPassword: '' }); } }}
            onChange={e => { setPwTouched(true); update({ bindPassword: e.target.value }); }}
            placeholder={pwStored ? '••••••••••••' : ''}
            autoComplete="off" />
        </div>

        {/* ── Section 2: Search ──────────────────────────────────────── */}
        <SectionHead title="User search" />
        <div className={ROW}>
          <Field label="Base DN" hint="Where to search for users" className={GROW}
            value={cfg.baseDN} onChange={e => update({ baseDN: e.target.value })} placeholder="OU=Users,DC=corp,DC=example" />
        </div>
        <div className={ROW}>
          <Field label="User search filter" hint="{{username}} is replaced at runtime" className={GROW}
            value={cfg.userSearchFilter} onChange={e => update({ userSearchFilter: e.target.value })}
            placeholder="(sAMAccountName={{username}})" />
        </div>
        <div className={ROW}>
          <Field label="User attribute" small
            value={cfg.userAttribute} onChange={e => update({ userAttribute: e.target.value })} placeholder="sAMAccountName" />
          <Field label="Email attribute" small
            value={cfg.emailAttribute} onChange={e => update({ emailAttribute: e.target.value })} placeholder="mail" />
          <Field label="Display attribute" small
            value={cfg.displayAttribute} onChange={e => update({ displayAttribute: e.target.value })} placeholder="displayName" />
        </div>
        {/* v0.8.538 — the identity attributes and the team pair are two
            Rows, not one five-field Row. Row now wraps, so one Row would
            reflow on its own, but it would break 4+1 and split Team
            attribute from Team regex — a pair whose hints reference each
            other. Splitting 3+2 keeps them together and leaves every
            field at its 180px basis instead of squeezing to ~150px. */}
        <div className={ROW}>
          <Field label="Team attribute" small
            hint="boş = department→ou · dn-ou = DN'deki en derin OU (alt ekip) · veya attribute adı"
            value={cfg.teamAttribute ?? ''} onChange={e => update({ teamAttribute: e.target.value })}
            placeholder="örn. displayName / division / dn-ou" />
          <Field label="Team regex" small controlClassName="font-mono"
            hint={'opsiyonel — ilk yakalama grubu ekip olur; ör. "…ÜNVAN-Ekip" için -([^-]+)$ (son tireden sonrası). Eşleşme yoksa ekip boş kalır.'}
            value={cfg.teamRegex ?? ''} onChange={e => update({ teamRegex: e.target.value })} placeholder="-([^-]+)$" />
        </div>
        {/* v0.8.430 — attribute discovery. Operator-reported: users.team
            herkes için üst division ("TECHNOLOGY") geliyordu çünkü AD bunu
            department'ta tutuyor; alt ekibin HANGİ attribute'ta olduğunu
            görmek için bir kullanıcının tüm directory attribute'larını dök. */}
        <InspectPanel onPick={(attr, pattern) => update({ teamAttribute: attr, teamRegex: pattern })} />

        {/* v0.10.1732 — kullanıcı detay sayfasının dizin öznitelikleri. */}
        <DirectoryAttrsSection value={cfg.directoryAttrs} onChange={v => update({ directoryAttrs: v })} />

        {/* ── Section 3: Groups ──────────────────────────────────────── */}
        <SectionHead title="Group lookup (optional)" description={<>
          Some directories don't populate <code>memberOf</code> on user entries.
          Set a group base + filter to do a separate group search; otherwise leave blank.
        </>} />
        <div className={ROW}>
          <Field label="Group search base" className={GROW}
            value={cfg.groupSearchBase} onChange={e => update({ groupSearchBase: e.target.value })}
            placeholder="OU=Groups,DC=corp,DC=example" />
        </div>
        <div className={ROW}>
          <Field label="Group filter" hint="{{userDN}} is replaced at runtime" className={GROW}
            value={cfg.groupFilter} onChange={e => update({ groupFilter: e.target.value })} placeholder="(member={{userDN}})" />
        </div>
        <div className={ROW}>
          <CheckboxField className="py-2" checked={!!cfg.skipMemberOfFetch}
            onCheckedChange={v => update({ skipMemberOfFetch: v })}
            label={<span className="font-semibold">Skip memberOf in user search (AD MaxValRange / 1MB workaround)</span>}
            hint={<span className="leading-normal">
              Set when users with very large nested-group memberships trip
              AD's <code>MaxValRange</code> (1500) or <code>MaxReceiveBuffer</code>
              (~1MB) caps and login fails with "size limit" / "1MB area" /
              <code>LDAP_ADMIN_LIMIT_EXCEEDED</code>. Drops the
              <code>memberOf</code> attribute from the user search; the
              separate group search (above) then becomes the authoritative
              role source. <b>Requires Group search base + filter to be set.</b>
            </span>} />
        </div>

        {/* ── Section 4: Role mapping ────────────────────────────────── */}
        <SectionHead title="Group → role mapping" description={<>
          When a user signs in, we walk their group memberships and pick the
          highest-privilege match. Group string can be a full DN or a CN
          fragment — match is case-insensitive substring.
        </>} />
        {/* v0.10.942 — statik tablo: düzenlenebilir eşleme listesi (T1).
            v0.10.977 — dilim 3'ün "sınıf karşılığı yok" diye bıraktığı 4px hücre
            dolgusu düştü: giriş kutuları taban `tbody td` ritminde (T5/T6,
            yoğunluk ayarı artık ulaşır); silme hücresi tek sağ eylem sütunu
            (`col-actions`, T8). Başlık dolgusu da tabana indi ki sütun metni
            hücreyle hizalı kalsın.
            Faz 2 dilim 1 R10 — shadcn Table (satır ritmi --row-h), kolonlar aynı. */}
        <Table className="mb-2 tabular-nums">
          <TableHeader>
            <TableRow className="bg-(--bg) text-(--text2)">
              <TableHead>Group (DN or CN substring)</TableHead>
              <TableHead className="w-[160px]">Role</TableHead>
              <TableHead className="col-actions w-8" aria-label="Eylemler" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {(cfg.groupRoleMap || []).map((m, i) => (
              <TableRow key={i}>
                <TableCell>
                  <Input value={m.group} aria-label="Group (DN or CN substring)"
                    onChange={e => updateMapping(cfg, setCfg, i, { group: e.target.value })}
                    placeholder="CN=Coremetry-Admins,OU=Groups,DC=corp,DC=example"
                    className={cn('h-7', CELL_CTL)} />
                </TableCell>
                <TableCell>
                  <NativeSelect size="sm" value={m.role} aria-label="Role" className={cn('h-7 data-[size=sm]:h-7', CELL_CTL)}
                    onChange={e => updateMapping(cfg, setCfg, i, { role: e.target.value as Role })}>
                    <option value="admin">admin</option>
                    <option value="editor">editor</option>
                    <option value="viewer">viewer</option>
                  </NativeSelect>
                </TableCell>
                <TableCell className="col-actions">
                  <Button variant="ghost-danger" size="sm"
                    onClick={() => removeMapping(cfg, setCfg, i)}
                    title="Remove this group mapping"
                    aria-label="Remove this group mapping">×</Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <Button type="button" variant="secondary" size="sm" className="mb-3"
          onClick={() => addMapping(cfg, setCfg)}>
          + Add mapping
        </Button>

        <div className={ROW}>
          <SelectField label="Default role" hint="Applied when no group matches" className={GROW}
            value={cfg.defaultRole} onChange={e => update({ defaultRole: e.target.value as Role })}>
            <option value="viewer">viewer</option>
            <option value="editor">editor</option>
            <option value="admin">admin</option>
          </SelectField>
        </div>

        <GroupSyncSection cfg={cfg} update={update} />

        {msg && (
          <div className={cn('mt-3.5 mb-3 rounded-sm border px-2.5 py-1.5 text-[length:var(--fs-sm)]',
            msg.kind === 'ok' ? 'text-(--ok) bg-(--ok)/10 border-(--ok)/35' : 'text-(--err) bg-(--err)/8 border-(--err)/30')}>
            {msg.text}
          </div>
        )}

        {/* D4 — birincil (Save) en sağda, ikincil solunda. */}
        <div className="mt-4">
          <ActionRow
            secondary={<Button type="button" variant="secondary" size="sm" disabled={busy} onClick={() => { void test(); }}>
              Test connection
            </Button>}
            confirm={<Button type="submit" size="sm" loading={busy}>Save</Button>} />
        </div>
      </form>

      {cfg.enabled && (
        <LDAPUserPicker />
      )}
      {cfg.enabled && <GroupSyncStatus />}
    </div>
  );
}

// GroupSyncSection — v0.8.527: periyodik AD grup→üye senkron ayarları.
// Alanlar cfg.groupSync üzerine yazılır ve formun mevcut Save'iyle
// birlikte kaydedilir (ayrı bir kaydetme yok). Durum/senkron/preview
// ayrı admin çağrıları — form ALTINDA GroupSyncStatus'ta.
function GroupSyncSection({ cfg, update }: {
  cfg: LDAPConfig;
  update: (patch: Partial<LDAPConfig>) => void;
}) {
  const gs = cfg.groupSync ?? emptyGroupSync();
  const patch = (p: Partial<LDAPGroupSyncConfig>) =>
    update({ groupSync: { ...gs, ...p } });
  const linesToList = (s: string) => s.split('\n').map(x => x.trim()).filter(Boolean);

  return (
    <>
      <SectionHead title="Group sync (directory → members)" description={<>
        Periyodik olarak kapsamdaki AD gruplarını ve (nested dahil) efektif
        üyelerini keşfeder; yetkilendirme için kullanılır. Login-anı grup
        aramasından bağımsızdır. Kaydettikten sonra aşağıdaki karttan
        <b> Şimdi senkronla</b> / <b>Önizle</b> ile test et.
      </>} />
      <CheckboxField className="mb-3" checked={gs.enabled} onCheckedChange={v => patch({ enabled: v })}
        label={<span className="text-[length:var(--fs-md)] font-semibold">Enable group sync</span>} />
      <div className={ROW}>
        <Field label="Sync interval" hint="ör. 30m" small
          value={gs.syncInterval} onChange={e => patch({ syncInterval: e.target.value })} placeholder="30m" />
        <Field label="Timeout" hint="tam senkron başına, ör. 60s" small
          value={gs.timeout} onChange={e => patch({ timeout: e.target.value })} placeholder="60s" />
        <Field label="Page size" hint="AD MaxPageSize 1000 altı" small
          type="number" value={gs.pageSize || ''}
          onChange={e => patch({ pageSize: parseInt(e.target.value, 10) || 0 })} placeholder="500" />
        <Field label="Max members / group" hint="aşan grup kırpılır" small
          type="number" value={gs.maxGroupMembers || ''}
          onChange={e => patch({ maxGroupMembers: parseInt(e.target.value, 10) || 0 })} placeholder="50000" />
      </div>
      <div className={ROW}>
        <Field label="Users base DN" hint="kullanıcı aramasının kökü" className={GROW} controlClassName="font-mono"
          value={gs.usersBaseDN} onChange={e => patch({ usersBaseDN: e.target.value })} placeholder="OU=Users,DC=corp,DC=example" />
        <Field label="User filter" small controlClassName="font-mono"
          value={gs.userFilter} onChange={e => patch({ userFilter: e.target.value })} placeholder="(objectClass=user)" />
        <Field label="Username attr" hint="üye kimliği" small
          value={gs.userNameAttribute} onChange={e => patch({ userNameAttribute: e.target.value })} placeholder="sAMAccountName" />
      </div>
      <div className={ROW}>
        <Field label="Groups base DN" hint="grup keşfinin kökü" className={GROW} controlClassName="font-mono"
          value={gs.groupsBaseDN} onChange={e => patch({ groupsBaseDN: e.target.value })} placeholder="OU=Groups,DC=corp,DC=example" />
        <Field label="Group filter" small controlClassName="font-mono"
          value={gs.groupFilter} onChange={e => patch({ groupFilter: e.target.value })} placeholder="(objectClass=group)" />
      </div>
      <div className={ROW}>
        <TextareaField label="Include prefixes" hint="her satır bir DN/OU soneki — boşsa tüm gruplar" className={GROW}
          value={(gs.includePrefixes || []).join('\n')}
          onChange={e => patch({ includePrefixes: linesToList(e.target.value) })}
          rows={2} spellCheck={false} controlClassName={MONO_XS}
          placeholder="OU=DistributionGroups,OU=Groups,DC=corp,DC=example" />
        <TextareaField label="Exclude prefixes" hint="kapsam dışı bırakılacaklar" className={GROW}
          value={(gs.excludePrefixes || []).join('\n')}
          onChange={e => patch({ excludePrefixes: linesToList(e.target.value) })}
          rows={2} spellCheck={false} controlClassName={MONO_XS} />
      </div>
      {/* Sır kaynağı referansları (dosya/env) — doluysa yukarıdaki inline
          CA/bind şifresini EZER (audit kararı). Değer sırrın kendisi değil,
          yol/anahtar olduğundan düz gösterilir. */}
      <div className={ROW}>
        <Field label="CA file path" hint="doluysa inline CA cert'i ezer" small controlClassName="font-mono"
          value={cfg.caFile ?? ''} onChange={e => update({ caFile: e.target.value })} placeholder="/etc/coremetry/ldap-ca.pem" />
        <Field label="Bind password file" hint="doluysa inline şifreyi ezer" small controlClassName="font-mono"
          value={cfg.bindPasswordFile ?? ''} onChange={e => update({ bindPasswordFile: e.target.value })}
          placeholder="/etc/coremetry/ldap-bind.pass" />
        <Field label="Bind password env" hint="ortam değişkeni adı" small controlClassName="font-mono"
          value={cfg.bindPasswordEnv ?? ''} onChange={e => update({ bindPasswordEnv: e.target.value })}
          placeholder="COREMETRY_LDAP_BIND_PASSWORD" />
      </div>
    </>
  );
}

// GroupSyncStatus — v0.8.527: canlı snapshot durumu + Şimdi senkronla +
// dry-run önizleme. Form ayrı kaydedildikten sonra kullanılır; her üç
// çağrı da admin-gated ve CH'ye (preview hariç) yazar.
function GroupSyncStatus() {
  const [sum, setSum] = useState<LDAPGroupSyncSummary | null | undefined>(undefined);
  const [prev, setPrev] = useState<LDAPGroupSyncPreview | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const load = () => { api.getLdapGroupSync().then(setSum).catch(() => setSum(null)); };
  useEffect(load, []);

  const syncNow = async () => {
    setBusy(true); setMsg(null);
    try {
      const s = await api.syncLdapGroupsNow();
      setSum(s);
      setMsg({ kind: 'ok', text: `Senkron tamam — ${s.stats.groups} grup, ${s.stats.users} üye, eşleşme %${(s.stats.matchRatio * 100).toFixed(0)}.` });
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(false); }
  };
  const preview = async () => {
    setBusy(true); setMsg(null); setPrev(null);
    try {
      setPrev(await api.previewLdapGroupSync());
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(false); }
  };

  if (sum === undefined) return <div className="mt-[18px]"><Spinner /></div>;

  const zeroMatch = !!sum && sum.stats.totalAlias > 0 && sum.stats.matchRatio === 0;

  return (
    <div className={cn('mt-[18px]', CARD)}>
      <h3 className="mb-2 flex items-center gap-2 text-[length:var(--fs-md)] font-semibold">
        Group sync durumu
        {/* K5 — ayarın hâli nötr (ne sağlık ne sapma). */}
        {sum && <StatusBadge status="unknown">{sum.enabled ? 'aktif' : 'kapalı'}</StatusBadge>}
      </h3>
      {!sum || !sum.configured ? (
        <p className="text-[length:var(--fs-sm)] text-(--text3)">
          LDAP yapılandırılmamış. Bağlantıyı kurup kaydettikten sonra senkron çalışır.
        </p>
      ) : (
        <>
          <div className="mb-2.5 flex flex-wrap gap-4 text-[length:var(--fs-sm)] tabular-nums">
            <span>Snapshot: <b>{sum.synced ? 'var' : 'yok'}</b></span>
            {sum.syncedAt && <span>Son senkron: <b className="mono">{tsLong(new Date(sum.syncedAt).getTime() * 1e6)}</b></span>}
            <span>Gruplar: <b className="mono">{sum.stats.groups}</b></span>
            <span>Üyeler: <b className="mono">{sum.stats.users}</b></span>
            {sum.stats.truncated > 0 && <span className="text-(--warn)">kırpılan: <b>{sum.stats.truncated}</b></span>}
            <span title="Alias'ların Coremetry kullanıcılarıyla kesişim oranı">
              Kimlik eşleşmesi: <b className={zeroMatch ? 'text-(--err)' : 'mono'}>
                %{(sum.stats.matchRatio * 100).toFixed(0)}</b>
            </span>
          </div>
          {zeroMatch && (
            <FlashBox kind="err">
              Senkron çalıştı ama HİÇBİR üye bir Coremetry kullanıcısıyla eşleşmedi —
              userNameAttribute / alias eşlemesini users.email veya users.ldap_username
              ile kontrol edin (Inspect ile alanı doğrulayın).
            </FlashBox>
          )}
          <div className="mb-2 flex gap-2">
            <Button size="sm" type="button" onClick={() => { void syncNow(); }} loading={busy}>
              ⟳ Şimdi senkronla
            </Button>
            <Button variant="secondary" size="sm" type="button" disabled={busy} onClick={() => { void preview(); }}>
              Önizle (dry-run)
            </Button>
          </div>
          {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
          {prev && (
            <div className="mt-2 text-[length:var(--fs-sm)]">
              <div className="mb-1.5 tabular-nums">
                Kapsamda <b>{prev.totalGroupsInScope}</b> grup · örneklenen <b>{prev.sampledGroups}</b> ·
                kimlik eşleşmesi <b>%{(prev.matchRatio * 100).toFixed(0)}</b> ({prev.matched}/{prev.totalAliases})
              </div>
              {prev.warning && <FlashBox kind="err">{prev.warning}</FlashBox>}
              <div className="table-wrap is-scroll max-h-[260px]">
                {/* v0.10.942 — statik tablo: önizleme en çok 10 grup örnekler, sıralanmaz (T1). */}
                <Table className="tabular-nums">
                  <TableHeader>
                    <TableRow><TableHead>Grup (CN)</TableHead><TableHead className="num">Üye</TableHead><TableHead>Örnek üyeler</TableHead></TableRow>
                  </TableHeader>
                  <TableBody>
                    {prev.groups.map(g => (
                      <TableRow key={g.uid}>
                        <TableCell className="font-mono" title={g.dn}>{g.cn}</TableCell>
                        <TableCell className="num">{g.memberCount}</TableCell>
                        <TableCell className="font-mono whitespace-normal text-(--text2)">{g.sampleMembers.join(', ') || '—'}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  );
}

function emptyGroupSync(): LDAPGroupSyncConfig {
  return {
    enabled: false, syncInterval: '30m', timeout: '60s', pageSize: 500,
    usersBaseDN: '', userFilter: '(objectClass=user)', userNameAttribute: 'sAMAccountName',
    groupsBaseDN: '', groupFilter: '(objectClass=group)',
    includePrefixes: [], excludePrefixes: [], maxGroupMembers: 50000,
  };
}

function updateMapping(
  cfg: LDAPConfig, set: (c: LDAPConfig) => void,
  i: number, patch: Partial<LDAPGroupRoleMapping>,
) {
  const next = [...cfg.groupRoleMap];
  next[i] = { ...next[i], ...patch };
  set({ ...cfg, groupRoleMap: next });
}
function addMapping(cfg: LDAPConfig, set: (c: LDAPConfig) => void) {
  set({ ...cfg, groupRoleMap: [...cfg.groupRoleMap, { group: '', role: 'viewer' }] });
}
function removeMapping(cfg: LDAPConfig, set: (c: LDAPConfig) => void, i: number) {
  set({ ...cfg, groupRoleMap: cfg.groupRoleMap.filter((_, j) => j !== i) });
}


// InspectPanel (v0.8.430) — bir kullanıcının okunabilir TÜM directory
// attribute'larını listeler; "Team attribute" için doğru alanı seçmeden
// önce nereye bakacağını gösterir. Salt-okunur, isteğe bağlı çağrı —
// liste render'ında hiçbir fetch yok.
// v0.8.523 — onPick: bir ekip adayına tıklanınca Team attribute +
// Team regex form alanlarını doldurur (operatör: "regex yazmayayım,
// UI'dan seçeyim"). Kaydet'e basmak yine operatörde.
function InspectPanel({ onPick }: { onPick: (attr: string, pattern: string) => void }) {
  const [u, setU] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [picked, setPicked] = useState('');
  const [res, setRes] = useState<Awaited<ReturnType<typeof api.ldapInspect>> | null>(null);

  const run = async () => {
    if (!u.trim()) return;
    setBusy(true); setErr(''); setRes(null);
    try {
      setRes(await api.ldapInspect(u.trim()));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mt-2.5 mb-3 rounded-md border border-(--border) bg-(--bg1) px-3 py-2.5">
      <div className="mb-2 flex items-center gap-2">
        <span className="text-[length:var(--fs-xs)] font-bold tracking-[0.4px] text-(--text2) uppercase">
          Kullanıcı incele
        </span>
        <Input value={u} onChange={e => setU(e.target.value)} aria-label="Kullanıcı incele"
          onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); void run(); } }}
          placeholder="kullanıcı adı" className={cn('h-8 w-[200px]', CELL_CTL)} />
        <Button variant="secondary" size="sm" type="button" onClick={() => { void run(); }} disabled={busy || !u.trim()}>
          {busy ? 'Sorgulanıyor…' : 'Attribute\'ları getir'}
        </Button>
        {err && <span className="text-[length:var(--fs-sm)] text-(--err)">{err}</span>}
      </div>
      {res && (
        <>
          <div className="mono mb-1.5 text-[length:var(--fs-xs)] text-(--text2) [overflow-wrap:anywhere]">
            {res.dn}
          </div>
          <div className="mb-2 flex flex-wrap gap-3 text-[length:var(--fs-xs)]">
            <span>Şu anki team değeri: <b className="mono">{res.team || '—'}</b></span>
            <span title="Team attribute = dn-ou seçilirse bu değer yazılır">
              dn-ou verirdi: <b className="mono">{res.deepestOu || '—'}</b>
            </span>
          </div>
          {picked && (
            // "✓ Seçildi" — kullanıcının az önce bastığı adayın geri bildirimi (K5: yeşil serbest).
            <div className="mb-1.5 text-[length:var(--fs-sm)] text-(--ok)">
              ✓ Seçildi: <b className="mono">{picked}</b> — yukarıdaki Team attribute/regex
              alanları dolduruldu; <b>Kaydet</b>'e basıp çıkış/giriş yapınca uygulanır.
            </div>
          )}
          <div className="table-wrap is-scroll max-h-[360px]">
            {/* v0.10.942 — statik tablo: seçici liste, aday düğmesi Team alanlarını doldurur (T1). */}
            <Table className="tabular-nums">
              <TableHeader>
                <TableRow><TableHead>Attribute</TableHead><TableHead>Değer(ler)</TableHead><TableHead>Ekip adayları — tıkla, seç</TableHead></TableRow>
              </TableHeader>
              <TableBody>
                {Object.entries(res.attributes).sort(([a], [b]) => a.localeCompare(b)).map(([k, vs]) => (
                  <TableRow key={k}>
                    <TableCell className="font-mono">{k}</TableCell>
                    <TableCell className="font-mono whitespace-normal [overflow-wrap:anywhere]">{vs.join(' · ')}</TableCell>
                    <TableCell className="whitespace-normal">
                      {/* v0.8.523 — sunucu ürettiği canlı-önizlemeli adaylar:
                          operatör ÇIKAN DEĞERİ görür, tıklar; regex arkada. */}
                      <div className="flex flex-wrap gap-1 py-1">
                        {(res.teamCandidates?.[k] ?? []).map(c => (
                          <Button key={c.pattern || 'raw'} variant="secondary" size="xs"
                            title={`${c.label}${c.pattern ? ` — regex: ${c.pattern}` : ''}`}
                            onClick={() => { onPick(k, c.pattern); setPicked(c.extracted); }}>
                            {c.extracted.length > 40 ? c.extracted.slice(0, 40) + '…' : c.extracted}
                          </Button>
                        ))}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </>
      )}
    </div>
  );
}

// DirectoryAttrsSection — v0.10.1732 (kullanıcı detay sayfası S2): Users ›
// kullanıcı detayının İletişim / Kuruluş kartlarını dolduran dizin öznitelik
// adları. Virgül = geri-düşme listesi; boş alan o bilgiyi okumaz. Sunucu boş
// blob'a AD varsayılanlarını koyar (internal/ldap/directory.go).
const DIRECTORY_FIELDS: { key: keyof LDAPDirectoryAttrs; label: string; placeholder: string }[] = [
  { key: 'title', label: 'Ünvan', placeholder: 'title' },
  { key: 'department', label: 'Departman', placeholder: 'department' },
  { key: 'division', label: 'Müdürlük', placeholder: 'division' },
  { key: 'level', label: 'Seviye', placeholder: 'boş = gösterme' },
  { key: 'employeeId', label: 'Sicil no', placeholder: 'employeeID,employeeNumber' },
  { key: 'mobile', label: 'Telefon', placeholder: 'mobile,telephoneNumber' },
  { key: 'office', label: 'Ofis', placeholder: 'physicalDeliveryOfficeName' },
  { key: 'manager', label: 'Yönetici (DN)', placeholder: 'manager' },
];
const EMPTY_DIRECTORY_ATTRS: LDAPDirectoryAttrs = {
  title: '', department: '', division: '', employeeId: '', mobile: '', office: '', manager: '', level: '',
};

function DirectoryAttrsSection({ value, onChange }: {
  value?: LDAPDirectoryAttrs;
  onChange: (v: LDAPDirectoryAttrs) => void;
}) {
  const v = value ?? EMPTY_DIRECTORY_ATTRS;
  return (
    <>
      <SectionHead title="Dizin öznitelikleri (kullanıcı detayı)" description={<>
        Users › kullanıcı detayındaki ünvan, departman, sicil, telefon, ofis ve yönetici alanları
        her LDAP girişinde bu özniteliklerden okunur. Virgül geri-düşme listesidir
        (<code>employeeID,employeeNumber</code>); boş alan okunmaz.
      </>} />
      <div className={ROW}>
        {DIRECTORY_FIELDS.map(f => (
          <Field key={f.key} label={f.label} small controlClassName="font-mono"
            value={v[f.key] ?? ''} placeholder={f.placeholder}
            onChange={e => onChange({ ...v, [f.key]: e.target.value })} />
        ))}
      </div>
    </>
  );
}
