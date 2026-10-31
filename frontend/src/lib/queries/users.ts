import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';

// Admin user management (/users). Mutations on that page are inline
// api.* calls behind useConfirm dialogs; they refresh by invalidating
// keys.users.all, which covers both queries below.

export function useUsers() {
  return useQuery({
    queryKey: keys.users.list,
    queryFn: async () => (await api.listUsers()) ?? [],
  });
}

// v0.10.1731 — /users/:id detay sayfası.
export function useUserDetail(id: string) {
  return useQuery({
    queryKey: keys.users.detail(id),
    queryFn: () => api.getUser(id),
    enabled: !!id,
  });
}

// v0.10.1731 — Değişiklikler sekmesi: bu kullanıcıyı HEDEF alan audit
// kayıtları, sunucuda süzülür (target=user&targetId=id), en yeni önce, ≤100.
// Yalnız sekme açıkken çekilir.
export function useUserAudit(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.users.audit(id),
    queryFn: async () => (await api.auditLog('8760h', { target: 'user', targetId: id, limit: 100 })) ?? [],
    enabled: enabled && !!id,
  });
}

// v0.10.1733 — Dizin sekmesi / Organizasyon kartı: LDAP okuması, yalnız
// açılınca; staleTime sunucu önbelleği kadar (aynı oturumda yeniden açmak
// dizine yeniden gitmez).
export function useUserDirectory(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.users.directory(id),
    queryFn: () => api.getUserDirectory(id),
    enabled: enabled && !!id,
    staleTime: 60_000,
    retry: false,
  });
}

export function useUserOrg(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.users.org(id),
    queryFn: () => api.getUserOrg(id),
    enabled: enabled && !!id,
    staleTime: 10 * 60_000,
    retry: false,
  });
}

// v0.10.1734 — Yetkiler / Bildirimler sekmeleri (yalnız sekme açıkken).
export function useUserAccess(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.users.access(id),
    queryFn: () => api.getUserAccess(id),
    enabled: enabled && !!id,
  });
}

// v0.10.1734 — sayfa kataloğu (Yetkiler sekmesinde rota → etiket).
export function useAvailablePages(enabled: boolean) {
  return useQuery({
    queryKey: keys.users.pages,
    queryFn: async () => (await api.listAvailablePages()).pages ?? [],
    enabled,
    staleTime: 10 * 60_000,
  });
}

// Custom-role catalog — drives the per-row picker on /users. Fetched
// alongside the user list, refreshed on every change so a role added
// in Settings → Roles appears here without a hard reload.
export function useCustomRoles() {
  return useQuery({
    queryKey: keys.users.customRoles,
    queryFn: async () => (await api.listCustomRoles()).roles ?? [],
  });
}
