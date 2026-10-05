// oidcForm — Settings > SSO formunun SAF yardımcıları (v0.10.1067).
//
// Form metin kutularıyla çalışır (kapsamlar / alan adları tek satır),
// sunucu dizi bekler; dönüşüm burada, test oidcForm.test.ts'te. Secret
// sözleşmesi: form secret'ı sunucudan HİÇ almaz (snapshot'ta yok) ve boş
// bırakılırsa gövdeye koymaz — sunucu kayıtlıyı korur.
import type { AuthConfigResponse } from '@/lib/api';
import type { OidcSettingsInput, OidcSettingsSnapshot } from '@/lib/types';

export interface OidcForm {
  enabled: boolean;
  issuerUrl: string;
  clientId: string;
  clientSecret: string;
  redirectUrl: string;
  scopes: string;
  displayName: string;
  defaultRole: string;
  allowedDomains: string;
  // v0.10.1110 — merkezi login yetki servisi + claim'den rol.
  permissionServiceEnabled: boolean;
  permissionServiceKey: string;
  permissionTTLSeconds: string;
  permissionsClaim: string;
  roleFromClaim: boolean;
}

/** Yetki servisi ucunun sabitleri (sunucu: internal/api/auth_permissions.go). */
export const PERMISSION_ENDPOINT = '/api/auth/permissions';
export const PERMISSION_HEADER = 'X-Coremetry-Auth-Key';
export const PERMISSION_TTL_DEFAULT = 300;

/** TTL kutusu → sayı; boş/geçersiz → varsayılan, [60, 3600] kıskacı (sunucuyla aynı). */
export function parsePermissionTTL(v: string): number {
  const n = Math.round(Number(v.trim()));
  if (!v.trim() || !Number.isFinite(n) || n <= 0) return PERMISSION_TTL_DEFAULT;
  return Math.min(3600, Math.max(60, n));
}

export const OIDC_ROLES = ['viewer', 'editor', 'admin'] as const;

/** Virgül / boşluk / satır sonu ayraçlı listeyi böler; boşları ve tekrarları atar. */
export function splitList(v: string): string[] {
  const out: string[] = [];
  for (const p of v.split(/[\s,;]+/)) {
    const t = p.trim();
    if (t && !out.includes(t)) out.push(t);
  }
  return out;
}

export function snapshotToForm(s: OidcSettingsSnapshot): OidcForm {
  return {
    enabled: s.enabled,
    issuerUrl: s.issuerUrl ?? '',
    clientId: s.clientId ?? '',
    clientSecret: '', // asla sunucudan gelmez
    redirectUrl: s.redirectUrl ?? '',
    scopes: (s.scopes ?? []).join(' '),
    displayName: s.displayName ?? '',
    defaultRole: s.defaultRole || 'viewer',
    allowedDomains: (s.allowedDomains ?? []).join(', '),
    permissionServiceEnabled: !!s.permissionServiceEnabled,
    permissionServiceKey: '', // asla sunucudan gelmez
    permissionTTLSeconds: String(s.permissionTTLSeconds || PERMISSION_TTL_DEFAULT),
    permissionsClaim: s.permissionsClaim || 'permissions',
    roleFromClaim: !!s.roleFromClaim,
  };
}

export function formToInput(f: OidcForm): OidcSettingsInput {
  const secret = f.clientSecret.trim();
  const permKey = f.permissionServiceKey.trim();
  return {
    enabled: f.enabled,
    issuerUrl: f.issuerUrl.trim(),
    clientId: f.clientId.trim(),
    ...(secret ? { clientSecret: secret } : {}),
    redirectUrl: f.redirectUrl.trim(),
    scopes: splitList(f.scopes),
    displayName: f.displayName.trim(),
    defaultRole: f.defaultRole,
    allowedDomains: splitList(f.allowedDomains).map(d => d.toLowerCase()),
    permissionServiceEnabled: f.permissionServiceEnabled,
    ...(permKey ? { permissionServiceKey: permKey } : {}),
    permissionTTLSeconds: parsePermissionTTL(f.permissionTTLSeconds),
    permissionsClaim: f.permissionsClaim.trim() || 'permissions',
    roleFromClaim: f.roleFromClaim,
  };
}

/**
 * Admin olmayan görünüm: ayar ucu admin-only, ama SSO'nun açık olup
 * olmadığı ve düğme etiketi zaten public /api/auth/config'te. Form boş
 * çizilmez — bilinen iki alan dolu, gerisi "yalnız yönetici görür".
 */
export function publicOidcSnapshot(c: AuthConfigResponse): OidcSettingsSnapshot {
  const on = !!c.oidc?.enabled;
  return {
    enabled: on,
    issuerUrl: '',
    clientId: '',
    clientSecretStored: false,
    redirectUrl: '',
    scopes: [],
    displayName: c.oidc?.displayName ?? '',
    defaultRole: 'viewer',
    allowedDomains: [],
    permissionServiceEnabled: false,
    permissionServiceKeySet: false,
    permissionTTLSeconds: PERMISSION_TTL_DEFAULT,
    permissionsClaim: 'permissions',
    roleFromClaim: false,
    source: 'config',
    active: on,
  };
}

/** Üst durum bandının etiketi + cümlesi. */
export function oidcStatus(s: OidcSettingsSnapshot): { label: string; text: string } {
  if (s.active) {
    return { label: 'AÇIK', text: `Giriş sayfasında “${s.displayName || 'SSO'}” düğmesi görünüyor.` };
  }
  if (s.enabled) {
    return { label: 'BAĞLANAMADI', text: 'Açık ama kimlik sağlayıcıya bağlanılamadı — giriş düğmesi gizli.' };
  }
  return { label: 'KAPALI', text: 'Yalnız yerel kullanıcı/parola girişi açık.' };
}
