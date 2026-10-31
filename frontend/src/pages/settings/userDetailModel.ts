// userDetailModel — v0.10.1731: kullanıcı detay sayfasının saf yardımcıları
// (bileşen dosyası yalnız bileşen dışa aktarsın — react-refresh).
import type { UserDetailUser } from '@/lib/types';

export type UserDetailTab = 'overview' | 'teams' | 'changes';

const TABS: readonly UserDetailTab[] = ['overview', 'teams', 'changes'];

/** URL ?tab= → sekme; bilinmeyen/boş değer Genel Bakış. */
export function userDetailTabFromParam(v: string | null): UserDetailTab {
  return (TABS as readonly string[]).includes(v ?? '') ? v as UserDetailTab : 'overview';
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
