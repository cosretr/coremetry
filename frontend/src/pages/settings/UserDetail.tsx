import { useState, type ReactNode } from 'react';
import { Link, useLocation, useParams, useSearchParams } from 'react-router-dom';
import { navHref } from '@/lib/navHref';
import { useQueryClient } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { PageShell } from '@/components/coremetry/PageShell';
import { KeyValue } from '@/components/coremetry/KeyValue';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { useConfirm } from '@/components/coremetry/Confirm';
import { Badge } from '@/components/shadcn/badge';
import { Button } from '@/components/shadcn/button';
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/shadcn/tabs';
import { CopyButton } from '@/components/CopyButton';
import { useAuth } from '@/components/AuthProvider';
import { keys, useCustomRoles, useUserAudit, useUserDetail, useUserDirectory, useUserOrg, useUserAccess, useAvailablePages } from '@/lib/queries';
import { api, type UserRow } from '@/lib/api';
import type { AuditEntry, UserDetailUser, UserDirectory, UserDirectoryCheck, UserLdapGroup, UserOrgPerson } from '@/lib/types';
import { cn } from '@/lib/cn';
import { useT } from '@/lib/i18n';
import { tsLong, tsRel } from '@/lib/utils';
import { CustomRoleEditor, RoleEditor, TeamEditor } from '../Users';
import { directoryAttributeRows, orgRows, userSourceLabel, userStatus, userDetailTabFromParam, type UserDetailTab } from './userDetailModel';

// UserDetail — v0.10.1731 (operatör onayı 2026-10-31, mockup "Kullanıcı
// detayı"): Settings › Users satırından açılan kullanıcı sayfası. Başlık
// (foto, ad + kuruluş, e-posta ⧉, dizin kullanıcı adı, rozetler, son giriş),
// "Düzenle" (Users sayfasının satır içi rol / özel rol / takım editörleri),
// sekmeler Genel Bakış · Takımlar · Değişiklikler. Sekme URL'de (?tab=,
// replace). Yalnız admin (kullanıcı kayıtları GET /api/users gibi admin'e açık).

const CARD = 'min-w-0 overflow-hidden rounded-lg border border-(--border) bg-(--bg2)';

function Card({ title, count, wide, children }: { title: string; count?: number; wide?: boolean; children: ReactNode }) {
  return (
    <section className={cn(CARD, wide && 'md:col-span-2')} aria-label={title}>
      <h2 className="m-0 flex items-center gap-2 border-b border-(--border) px-3.5 py-2.5 text-[13px] font-semibold">
        {title}
        {count !== undefined && <Badge variant="secondary">{count}</Badge>}
      </h2>
      <div className="px-3.5 py-2">{children}</div>
    </section>
  );
}

function when(ns: number | undefined): ReactNode {
  if (!ns) return null;
  return <span title={tsLong(ns)}>{tsRel(ns)} · {tsLong(ns)}</span>;
}

function Avatar({ u }: { u: UserDetailUser }) {
  const initials = (u.fullName || u.email).split(/[\s@.]+/).filter(Boolean).slice(0, 2).map(p => p[0]).join('');
  if (u.hasPhoto) {
    return <img src={`/api/users/${encodeURIComponent(u.id)}/photo`} alt="" className="size-14 shrink-0 rounded-full object-cover" />;
  }
  return (
    <span aria-hidden="true"
      className="grid size-14 shrink-0 place-items-center rounded-full bg-(--bg3) text-base font-semibold uppercase text-(--text2)">
      {initials}
    </span>
  );
}

function errText(e: unknown): string {
  const msg = e instanceof Error ? e.message : String(e);
  const body = msg.replace(/^HTTP \d+:\s*/, '');
  try {
    const j = JSON.parse(body) as { error?: unknown };
    if (typeof j.error === 'string') return j.error;
  } catch { /* düz metin */ }
  return body || msg;
}

// v0.10.1733 — Dizin (AD) sekmesi: sekme açılınca (Radix içeriği yalnız etkin
// sekmede bağlar) servis hesabıyla okunan ham öznitelikler.
function DirectoryTab({ id }: { id: string }) {
  const q = useUserDirectory(id, true);
  if (q.isPending) return <Spinner label="Dizin okunuyor" />;
  if (q.isError) {
    return (
      <Empty icon="⚠" title="Dizin okunamadı"
        action={<Button variant="outline" size="sm" onClick={() => void q.refetch()}>Yeniden dene</Button>}>
        {errText(q.error)}
      </Empty>
    );
  }
  const rows = directoryAttributeRows(q.data.attributes ?? {});
  return (
    <Card title="Dizin öznitelikleri" count={rows.length}>
      <p className="mt-0 mb-2 text-xs text-(--text3)">
        Servis hesabıyla okunan ham değerler (ikili değerler boyut özeti). DN: <span className="font-mono">{q.data.dn}</span>
      </p>
      <KeyValue labelWidth="wide" items={rows.map(r => ({ id: r.k, k: r.k, v: r.v, mono: true }))} />
    </Card>
  );
}

const SCOPE_TEXT: Record<'all' | 'viewer', string> = {
  all: 'Tüm izinler — rol kısıtı yok (admin: ayarlar ve yıkıcı işlemler dahil; editor: kural, preset ve durum düzenleme).',
  viewer: 'Tüm sayfalar, salt-okunur — özel rol atanmamış.',
};

// v0.10.1734 — Yetkiler sekmesi: rolün çözülmüş hâli. Özel rollü viewer'da
// sayfa listesi, etiketler sayfa kataloğundan (/api/admin/pages).
function PermissionsTab({ id }: { id: string }) {
  const q = useUserAccess(id, true);
  const perms = q.data?.permissions;
  const pagesQ = useAvailablePages(perms?.scope === 'pages');
  const t = useT();
  if (q.isPending) return <Spinner label="Yetkiler okunuyor" />;
  if (q.isError) {
    return (
      <Empty icon="⚠" title="Yetkiler okunamadı"
        action={<Button variant="outline" size="sm" onClick={() => void q.refetch()}>Yeniden dene</Button>}>
        {errText(q.error)}
      </Empty>
    );
  }
  const p = q.data.permissions;
  const labelOf = new Map((pagesQ.data ?? []).map(x => [x.id, t(x.label)]));
  return (
    <Card title="Yetkiler" count={p.scope === 'pages' ? p.pages.length : undefined}>
      <div className="flex flex-col gap-2 py-1.5 text-[13px]">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="secondary" className="font-mono">{p.role}</Badge>
          {p.customRole && <Badge variant="outline">{p.customRole}</Badge>}
        </div>
        {(p.scope === 'all' || p.scope === 'viewer') && <p className="m-0 text-(--text2)">{SCOPE_TEXT[p.scope]}</p>}
        {p.scope === 'missing' && (
          <p className="m-0 text-(--warn)">
            Özel rol <b>{p.customRole}</b> katalogda yok (silinmiş olabilir) — kullanıcı kısıtsız viewer gibi davranır.
          </p>
        )}
        {p.scope === 'pages' && (p.pages.length === 0
          ? <p className="m-0 text-(--text2)">Bu özel rol hiçbir sayfaya izin vermiyor.</p>
          : (
            <>
              <p className="m-0 text-(--text2)">Yalnız bu sayfalar, salt-okunur:</p>
              <ul className="m-0 flex list-none flex-wrap gap-1.5 p-0">
                {p.pages.map(pg => (
                  <li key={pg}><Badge variant="outline" title={pg}>{labelOf.get(pg) ?? pg}</Badge></li>
                ))}
              </ul>
            </>
          ))}
      </div>
    </Card>
  );
}

// v0.10.1734 — Bildirimler sekmesi. Kişi başına abonelik yok: bu kullanıcıya
// ulaşan e-posta kanalları + ekip yönlendirme yolları gösterilir.
function NotificationsTab({ id }: { id: string }) {
  const q = useUserAccess(id, true);
  if (q.isPending) return <Spinner label="Bildirim yolları okunuyor" />;
  if (q.isError) {
    return (
      <Empty icon="⚠" title="Bildirim yolları okunamadı"
        action={<Button variant="outline" size="sm" onClick={() => void q.refetch()}>Yeniden dene</Button>}>
        {errText(q.error)}
      </Empty>
    );
  }
  const n = q.data.notifications;
  return (
    <div className="flex flex-col gap-3.5">
      <p className="m-0 text-[13px] text-(--text2)">
        Coremetry'de kişi başına bildirim aboneliği yok. Bu kullanıcıya ulaşan yollar: alıcı listesinde
        e-postası olan kanallar ve ekip yönlendirmesi (Settings › Team routing).
      </p>
      <Card title="E-posta kanalları" count={n.channels.length}>
        {n.channels.length === 0
          ? <p className="my-1.5 text-[13px] text-(--text2)">Hiçbir e-posta kanalının alıcı listesinde değil.</p>
          : <KeyValue items={n.channels.map(c => ({
            id: c.id, k: c.name,
            v: `${c.enabled ? 'etkin' : 'kapalı'}${c.minSeverity ? ` · en az ${c.minSeverity}` : ''}`,
          }))} />}
      </Card>
      <Card title="Ekip yönlendirme" count={n.teamRoutes.length}>
        {!n.teamRoutingEnabled && (
          <p className="mt-1.5 mb-1 text-[13px] text-(--warn)">Ekip yönlendirme maili kapalı — aşağıdaki yollar şu an mail göndermez.</p>
        )}
        {n.teamRoutes.length === 0
          ? <p className="my-1.5 text-[13px] text-(--text2)">Birincil takımın ekip adresi yok ve hiçbir ekip adresinde bu e-posta geçmiyor.</p>
          : <KeyValue items={n.teamRoutes.map(r => ({
            id: r.team, k: r.team,
            v: <>{r.addresses.join(', ') || '—'} <Badge variant="outline">{r.direct ? 'adres listesinde' : 'birincil takım'}</Badge></>,
          }))} />}
      </Card>
    </div>
  );
}

const ORG_INDENT = ['pl-0', 'pl-4', 'pl-8', 'pl-12', 'pl-16', 'pl-20'];

function OrgName({ p }: { p: UserOrgPerson }) {
  return p.accountId
    ? <Link to={`/users/${encodeURIComponent(p.accountId)}`} title="Coremetry hesabını aç">{p.name}</Link>
    : <span>{p.name}</span>;
}

// v0.10.1733 — Organizasyon kartı: yönetici zinciri (≤4 üst) + doğrudan
// bağlılar (≤50). Kapalı başlar; "Göster" ile LDAP'tan okunur (10 dk sunucu
// önbelleği). Coremetry hesabı olan adlar o hesabın detayına link.
function OrgCard({ id }: { id: string }) {
  const [open, setOpen] = useState(false);
  const q = useUserOrg(id, open);
  let body: ReactNode;
  if (!open) {
    body = (
      <div className="flex flex-wrap items-center gap-2 py-1.5 text-[13px] text-(--text2)">
        Yönetici zinciri ve doğrudan bağlılar dizinden okunur.
        <Button variant="outline" size="sm" onClick={() => setOpen(true)}>Organizasyonu göster</Button>
      </div>
    );
  } else if (q.isPending) {
    body = <Spinner label="Organizasyon okunuyor" />;
  } else if (q.isError) {
    body = (
      <Empty compact icon="⚠" title="Organizasyon okunamadı"
        action={<Button variant="outline" size="sm" onClick={() => void q.refetch()}>Yeniden dene</Button>}>
        {errText(q.error)}
      </Empty>
    );
  } else {
    const rows = orgRows(q.data);
    body = (
      <>
        <ol className="m-0 flex list-none flex-col p-0 py-1 text-[13px]">
          {rows.map(r => (
            <li key={`${r.role}:${r.p.dn}`}
              className={cn('flex items-center gap-2 py-1', ORG_INDENT[Math.min(r.depth, ORG_INDENT.length - 1)], r.role === 'self' && 'font-semibold')}>
              <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', r.role === 'self' ? 'bg-(--text)' : 'bg-(--border)')} />
              <OrgName p={r.p} />
              <span className="font-normal text-(--text3)">
                {[r.p.title, r.p.employeeId].filter(Boolean).join(' · ')}
              </span>
            </li>
          ))}
        </ol>
        {q.data.reports.length === 0 && <p className="m-0 pb-1 text-xs text-(--text3)">Doğrudan bağlı yok.</p>}
        {q.data.reportsTruncated && <p className="m-0 pb-1 text-xs text-(--text3)">İlk 50 doğrudan bağlı gösteriliyor.</p>}
        {q.data.chainError && <p className="m-0 pb-1 text-xs text-(--warn)">Zincir kesildi: {q.data.chainError}</p>}
      </>
    );
  }
  return <Card title="Organizasyon" wide>{body}</Card>;
}

// v0.10.1733 — "AD ile denetle" sonucu: alan alan önce → sonra.
function CheckResult({ r, onClose }: { r: UserDirectoryCheck; onClose: () => void }) {
  return (
    <Card title="AD ile denetim sonucu" count={r.diff.length}>
      {r.diff.length === 0 ? (
        <p className="my-1.5 text-[13px] text-(--text2)">Fark yok — kayıt dizinle aynı.</p>
      ) : (
        <>
          <p className="mt-1 mb-2 text-xs text-(--text3)">Kayıt dizinden yenilendi (rol değişmez). Önce → sonra:</p>
          <KeyValue items={r.diff.map(d => ({ id: d.field, k: d.label, v: `${d.before || '—'} → ${d.after || '—'}` }))} />
        </>
      )}
      <div className="py-1.5"><Button variant="ghost" size="sm" onClick={onClose}>Kapat</Button></div>
    </Card>
  );
}

const GROUP_COLS: ColumnDef<UserLdapGroup>[] = [
  { id: 'cn', label: 'Grup', sortValue: g => g.cn, naturalDir: 'asc', width: 240 },
  { id: 'dn', label: 'DN', sortValue: g => g.dn, naturalDir: 'asc', flex: true, mono: true },
];

const AUDIT_COLS: ColumnDef<AuditEntry>[] = [
  { id: 'time', label: 'Zaman', sortValue: e => e.time, numeric: true, width: 170 },
  { id: 'action', label: 'İşlem', sortValue: e => e.action, naturalDir: 'asc', width: 190, mono: true },
  { id: 'actor', label: 'Yapan', sortValue: e => e.actorEmail, naturalDir: 'asc', width: 220 },
  { id: 'details', label: 'Ayrıntı', sortValue: e => e.details, flex: true, mono: true, tone: () => 'muted' },
];

export default function UserDetailPage() {
  const { id = '' } = useParams();
  const { user: me } = useAuth();
  const location = useLocation();
  const [searchParams, setSearchParams] = useSearchParams();
  const tab = userDetailTabFromParam(searchParams.get('tab'));
  const setTab = (t: UserDetailTab) => setSearchParams(prev => {
    const p = new URLSearchParams(prev);
    if (t === 'overview') p.delete('tab'); else p.set('tab', t);
    return p;
  }, { replace: true });

  const q = useUserDetail(id);
  const audit = useUserAudit(id, tab === 'changes');
  const rolesQ = useCustomRoles();
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [editing, setEditing] = useState(false);
  const [lockBusy, setLockBusy] = useState(false);
  const [lockErr, setLockErr] = useState<string | null>(null);
  const [check, setCheck] = useState<UserDirectoryCheck | null>(null);
  const [checkBusy, setCheckBusy] = useState(false);
  const [checkErr, setCheckErr] = useState<string | null>(null);
  const refresh = () => { void qc.invalidateQueries({ queryKey: keys.users.all }); };

  if (me && me.role !== 'admin') {
    return (
      <>
        <Topbar title="Kullanıcı detayı" />
        <PageShell>
          <Empty icon="🔒" title="Admin erişimi gerekir">Kullanıcı kayıtları yalnız yöneticilere açıktır.</Empty>
        </PageShell>
      </>
    );
  }

  const back = <Link to={navHref('/users', location.search)} className="text-xs text-(--text3) hover:underline">← Users</Link>;
  if (q.isPending) {
    return <><Topbar title="Kullanıcı detayı" /><PageShell>{back}<Spinner label="Kullanıcı yükleniyor" /></PageShell></>;
  }
  if (q.isError || !q.data) {
    const notFound = q.error instanceof Error && /\b404\b/.test(q.error.message);
    return (
      <>
        <Topbar title="Kullanıcı detayı" />
        <PageShell>
          {back}
          <Empty icon={notFound ? '∅' : '⚠'} title={notFound ? 'Kullanıcı bulunamadı' : 'Kullanıcı okunamadı'}
            action={notFound ? undefined : <Button variant="outline" size="sm" onClick={() => void q.refetch()}>Yeniden dene</Button>}>
            {notFound ? 'Hesap silinmiş ya da kimlik yanlış.' : 'Bu bir okuma hatası; hesap etkilenmedi.'}
          </Empty>
        </PageShell>
      </>
    );
  }

  const { user: u, groups, groupSyncEnabled, groupsSyncedAt, online, lastSeenAt } = q.data;
  const row: UserRow = u;
  const isMe = me?.id === u.id;
  const st = userStatus(u);
  const isLdap = u.authProvider === 'ldap';
  const dir: UserDirectory = u.directory ?? {};
  const teamCount = groups.length + (u.team ? 1 : 0);
  const teamSuggestions = Array.from(new Set(
    (qc.getQueryData<UserRow[]>(keys.users.list) ?? []).map(x => x.team).filter(Boolean))).sort();

  const unlock = async () => {
    setLockErr(null);
    if (!await confirm({
      title: 'Rol AD\'ye geri verilsin mi?',
      body: <>Kilit kalkar; <b>{u.email}</b> bir sonraki LDAP girişinde rolünü AD grup eşlemesinden alır
        (eşleşme yoksa mevcut <code>{u.role}</code> rolü kalır).</>,
      confirmLabel: 'Kilidi kaldır',
    })) return;
    setLockBusy(true);
    try {
      await api.clearUserRoleLock(u.id);
      refresh();
    } catch (e) {
      setLockErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLockBusy(false);
    }
  };

  const runCheck = async () => {
    setCheckErr(null);
    setCheckBusy(true);
    try {
      const r = await api.checkUserDirectory(u.id);
      setCheck(r);
      if (r.written) refresh();
    } catch (e) {
      setCheckErr(errText(e));
    } finally {
      setCheckBusy(false);
    }
  };

  const groupsState: DataTableStateInput<UserLdapGroup> = groupSyncEnabled
    ? { kind: 'empty', message: 'Bu kullanıcı eşitlenen hiçbir LDAP grubunda üye değil.' }
    : { kind: 'empty', message: 'LDAP grup eşitlemesi kapalı (Settings › LDAP) — grup üyelikleri bilinmiyor.' };
  const auditState: DataTableStateInput<AuditEntry> = audit.isPending ? { kind: 'loading' }
    : audit.isError ? { kind: 'error', message: 'Değişiklik kaydı okunamadı.', onRetry: () => void audit.refetch() }
    : { kind: 'empty', message: 'Son 1 yılda bu kullanıcıyı hedef alan değişiklik yok.' };

  return (
    <>
      <Topbar title="Kullanıcı detayı" />
      <PageShell>
        <div className="flex flex-col gap-3.5">
          {back}
          <header className="flex flex-wrap items-start gap-3.5">
            <Avatar u={u} />
            <div className="flex min-w-[220px] flex-1 flex-col gap-1.5">
              <h1 className="m-0 text-lg font-semibold">
                {u.fullName || u.email}{u.org ? <span className="font-normal text-(--text2)"> ({u.org})</span> : null}
              </h1>
              <div className="flex flex-wrap items-center gap-2 text-[13px] text-(--text2)">
                {u.directory?.employeeId && <>
                  <span className="font-mono font-medium text-(--text)" title="Sicil no">{u.directory.employeeId}</span>
                  <CopyButton value={u.directory.employeeId} title="Sicil no kopyala" copiedTitle="Kopyalandı" />·
                </>}
                <span>{u.email}</span><CopyButton value={u.email} title="E-postayı kopyala" copiedTitle="Kopyalandı" />
                {u.ldapUsername && <span className="font-mono text-(--text)" title="Dizin kullanıcı adı">{u.ldapUsername}</span>}
              </div>
              <div className="flex flex-wrap gap-1.5">
                <Badge variant="secondary" className="font-mono">{u.role}</Badge>
                {u.customRole && <Badge variant="outline" title="Özel rol">{u.customRole}</Badge>}
                {u.directory?.title && <Badge variant="outline" title="Ünvan (dizin)">{u.directory.title}</Badge>}
                <StatusBadge status={st.status}>{st.label}</StatusBadge>
                <Badge variant="outline">{userSourceLabel(u.authProvider)}</Badge>
                {u.roleLocked && <StatusBadge status="warn" title="Rol elle verildi; LDAP girişi AD eşlemesiyle ezmez">Rol kilitli</StatusBadge>}
                {isMe && <Badge variant="outline">sen</Badge>}
                {/* v0.10.1735 — varlık; yeşil = Users "online" rozetinin operatör istisnası (v0.10.1535). */}
                {online && lastSeenAt ? (
                  <span className="badge b-online" title={`Son kimlikli istek: ${tsLong(lastSeenAt)}`}>
                    ● Çevrimiçi · son istek {tsRel(lastSeenAt)}
                  </span>
                ) : null}
              </div>
              <div className="text-[13px] text-(--text2)">
                Son giriş: {u.lastLoginAt ? <>{tsRel(u.lastLoginAt)} · {tsLong(u.lastLoginAt)}</> : 'hiç'}
                {!online && lastSeenAt ? <> · son görülme <span title={tsLong(lastSeenAt)}>{tsRel(lastSeenAt)}</span></> : null}
              </div>
            </div>
            <div className="flex gap-2 self-start">
              {isLdap && (
                <Button variant="outline" loading={checkBusy} onClick={() => void runCheck()}
                  title="Kullanıcıyı servis hesabıyla dizinden yeniden oku, farkı göster ve kaydı yenile">
                  AD ile denetle
                </Button>
              )}
              <Button variant={editing ? 'default' : 'outline'} onClick={() => setEditing(e => !e)}
                aria-pressed={editing}>{editing ? 'Bitti' : 'Düzenle'}</Button>
            </div>
          </header>

          {checkErr && <p role="alert" className="m-0 text-[13px] text-(--err)">AD denetimi başarısız: {checkErr}</p>}
          {check && <CheckResult r={check} onClose={() => setCheck(null)} />}

          <Tabs value={tab} onValueChange={v => setTab(userDetailTabFromParam(v))}>
            <TabsList variant="line" aria-label="Kullanıcı sekmeleri" className="mb-3">
              <TabsTrigger value="overview">Genel Bakış</TabsTrigger>
              <TabsTrigger value="teams">Takımlar <Badge variant="secondary">{teamCount}</Badge></TabsTrigger>
              <TabsTrigger value="notifications">Bildirimler</TabsTrigger>
              <TabsTrigger value="permissions">Yetkiler</TabsTrigger>
              <TabsTrigger value="changes">
                Değişiklikler{audit.data ? <Badge variant="secondary">{audit.data.length}</Badge> : null}
              </TabsTrigger>
              {isLdap && <TabsTrigger value="directory">Dizin (AD)</TabsTrigger>}
            </TabsList>

            <TabsContent value="overview">
              <div className="grid grid-cols-1 gap-3.5 md:grid-cols-2">
                <Card title="Hesap ve oturum">
                  <KeyValue items={[
                    { k: 'Durum', v: st.label },
                    { k: 'Hesap kaynağı', v: userSourceLabel(u.authProvider) },
                    { k: 'Rol', v: editing ? <RoleEditor user={row} isMe={isMe} onChanged={refresh} /> : u.role, mono: !editing },
                    { k: 'Özel rol', v: editing ? <CustomRoleEditor user={row} catalog={rolesQ.data ?? []} onChanged={refresh} /> : u.customRole },
                    { k: 'Son giriş', v: when(u.lastLoginAt) },
                    { k: 'Son istek', v: when(lastSeenAt), title: 'Son kimlikli API isteği (varlık damgası, 24 sa saklanır)' },
                    { k: 'Oluşturulma', v: u.createdAt ? tsLong(u.createdAt) : null },
                    { k: 'Dizin kullanıcı adı', v: u.ldapUsername, mono: true },
                  ]} />
                </Card>
                <Card title="İletişim">
                  <KeyValue items={[
                    { k: 'E-posta', v: u.email, actions: <CopyButton value={u.email} title="E-postayı kopyala" copiedTitle="Kopyalandı" /> },
                    ...(isLdap ? [
                      { k: 'Sicil no', v: dir.employeeId, mono: true,
                        actions: dir.employeeId ? <CopyButton value={dir.employeeId} title="Sicil no kopyala" copiedTitle="Kopyalandı" /> : undefined },
                      { k: 'Telefon', v: dir.mobile, mono: true },
                      { k: 'Ofis', v: dir.office },
                    ] : []),
                  ]} />
                </Card>
                {isLdap && (
                  <Card title="Kilitler ve AD istisnaları" count={u.roleLocked ? 1 : 0}>
                    {u.roleLocked ? (
                      <div className="flex flex-col items-start gap-2 py-1.5 text-[13px]">
                        <StatusBadge status="warn">Rol kilitli</StatusBadge>
                        <p className="m-0 text-(--text2)">
                          Sistem rolü elle <b className="font-mono">{u.role}</b> yapıldı; LDAP girişinde AD grup
                          eşlemesi bunun üzerine yazmaz.
                        </p>
                        <Button variant="outline" size="sm" loading={lockBusy} onClick={() => void unlock()}>
                          Rolü AD'ye geri ver
                        </Button>
                        {lockErr && <p role="alert" className="m-0 text-(--err)">{lockErr}</p>}
                      </div>
                    ) : (
                      <p className="my-1.5 text-[13px] text-(--text2)">
                        Kilit yok — rol her LDAP girişinde AD grup eşlemesinden yenilenir. Rolü burada elle
                        değiştirmek kilitler.
                      </p>
                    )}
                  </Card>
                )}
                <Card title="Kuruluş">
                  <KeyValue items={[
                    { k: 'Birincil takım', v: editing ? <TeamEditor user={row} suggestions={teamSuggestions} onChanged={refresh} /> : u.team, mono: !editing },
                    { k: 'Şirket', v: u.org },
                    ...(isLdap ? [
                      { k: 'Ünvan', v: dir.title },
                      { k: 'Departman', v: dir.department },
                      { k: 'Müdürlük', v: dir.division },
                      ...(dir.level ? [{ k: 'Seviye', v: dir.level }] : []),
                      { k: 'Bağlı olduğu yönetici', v: dir.managerName || dir.managerDn, title: dir.managerDn },
                    ] : []),
                  ]} />
                </Card>
                {isLdap && <OrgCard id={u.id} />}
              </div>
            </TabsContent>

            <TabsContent value="teams">
              <div className="flex flex-col gap-3.5">
                <Card title="Birincil takım">
                  <KeyValue items={[{ k: 'Takım', v: u.team, mono: true, title: isLdap ? 'LDAP girişinde dizindeki bölüm alanından yenilenir' : undefined }]} />
                </Card>
                <Card title="LDAP grupları" count={groups.length}>
                  {groupsSyncedAt ? (
                    <p className="mt-0 mb-2 text-xs text-(--text3)">Son grup eşitlemesi: {tsRel(groupsSyncedAt)} · {tsLong(groupsSyncedAt)}</p>
                  ) : null}
                  <DataTable<UserLdapGroup> storageKey="user-detail-groups" columns={GROUP_COLS} rows={groups}
                    sort={{ initial: { id: 'cn', dir: 'asc' } }} getRowKey={g => g.dn} state={groupsState} />
                </Card>
              </div>
            </TabsContent>

            {isLdap && (
              <TabsContent value="directory">
                <DirectoryTab id={u.id} />
              </TabsContent>
            )}

            <TabsContent value="notifications">
              <NotificationsTab id={u.id} />
            </TabsContent>
            <TabsContent value="permissions">
              <PermissionsTab id={u.id} />
            </TabsContent>

            <TabsContent value="changes">
              <Card title="Bu kullanıcıyı hedef alan değişiklikler">
                <p className="mt-0 mb-2 text-xs text-(--text3)">Audit kaydı, son 1 yıl, en yeni önce, en çok 100.</p>
                <DataTable<AuditEntry> storageKey="user-detail-audit" columns={AUDIT_COLS} rows={audit.data ?? []}
                  sort={{ initial: { id: 'time', dir: 'desc' } }} getRowKey={e => e.id} state={auditState}
                  renderCell={(e, col) => col.id === 'time' ? <span title={tsLong(e.time)}>{tsLong(e.time)}</span>
                    : col.id === 'actor' ? (e.actorEmail || e.actorId)
                    : col.id === 'action' ? e.action : e.details} />
              </Card>
            </TabsContent>
          </Tabs>
        </div>
      </PageShell>
    </>
  );
}
