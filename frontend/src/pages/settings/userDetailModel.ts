// userDetailModel — v0.10.1731: kullanıcı detay sayfasının saf yardımcıları
// (bileşen dosyası yalnız bileşen dışa aktarsın — react-refresh).
import type { UserDetail, UserDetailUser, UserDirectory, UserDirectoryCheck, UserManager } from '@/lib/types';

/** v0.10.1741 — "AD ile denetle" cevabı detay önbelleğine: `after` sunucunun
 *  YAZDIĞI satır (yeniden okuma), kartlar refetch beklemeden onu gösterir. */
export function withDirectoryCheck(old: UserDetail | undefined, r: UserDirectoryCheck): UserDetail | undefined {
  if (!old || !r.written || old.user.id !== r.after.id) return old;
  return { ...old, user: r.after };
}

/** v0.10.1741 — Kuruluş kartının yöneticisi: çözülmüş görünen ad (+ sicil),
 *  yoksa saklı ad (DN'nin CN'i). Ham DN ASLA (yalnız Dizin sekmesinde). */
export function managerLabel(manager: UserManager | undefined, dir: UserDirectory): string | undefined {
  const name = manager?.name || dir.managerName;
  if (!name) return undefined;
  return manager?.employeeId ? `${name} · ${manager.employeeId}` : name;
}

export type UserDetailTab = 'overview' | 'teams' | 'notifications' | 'permissions' | 'changes' | 'directory';

const TABS: readonly UserDetailTab[] = ['overview', 'teams', 'notifications', 'permissions', 'changes', 'directory'];

/** URL ?tab= → sekme; bilinmeyen/boş değer Genel Bakış. */
export function userDetailTabFromParam(v: string | null): UserDetailTab {
  return (TABS as readonly string[]).includes(v ?? '') ? v as UserDetailTab : 'overview';
}

/** Dizin sekmesi: öznitelikler ada göre (büyük/küçük harf duyarsız), çok
 *  değerli öznitelik satır satır. */
export function directoryAttributeRows(attrs: Record<string, string[]>): { k: string; v: string }[] {
  return Object.keys(attrs)
    .sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()))
    .map(k => ({ k, v: (attrs[k] ?? []).join('\n') }));
}

/** Organizasyon: üstten alta satırlar — yöneticiler (en üst önce), kullanıcı,
 *  doğrudan bağlılar; `depth` girinti kademesi. */
export function orgRows<P>(chart: { self: P; managers: P[]; reports: P[] }): { p: P; depth: number; role: 'manager' | 'self' | 'report' }[] {
  const top = [...chart.managers].reverse();
  const rows: { p: P; depth: number; role: 'manager' | 'self' | 'report' }[] =
    top.map((p, i) => ({ p, depth: i, role: 'manager' }));
  rows.push({ p: chart.self, depth: top.length, role: 'self' });
  for (const p of chart.reports) rows.push({ p, depth: top.length + 1, role: 'report' });
  return rows;
}

/** Hesap kaynağı rozeti. */
export function userSourceLabel(provider: string | undefined): string {
  switch (provider) {
    case 'ldap': return 'AD (LDAP)';
    case 'oidc': return 'SSO (OIDC)';
    case 'trusted': return 'Güvenilen başlık';
    default: return 'Yerel';
  }
}

/** Durum rozeti: engelli > pasif (silinmiş) > aktif. */
export function userStatus(u: Pick<UserDetailUser, 'disabled' | 'blocked'>): { label: string; status: 'ok' | 'warn' | 'critical' } {
  if (u.blocked) return { label: 'Engelli', status: 'critical' };
  if (u.disabled) return { label: 'Pasif', status: 'warn' };
  return { label: 'Aktif', status: 'ok' };
}
