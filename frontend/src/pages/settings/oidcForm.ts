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
  // v0.10.1111 — anahtarsız kip + IP/CIDR izin listesi + güvenilen vekiller
  // (metin kutuları: satır başına bir giriş ya da virgül ayraçlı).
  permissionServiceAllowNoKey: boolean;
  permissionServiceAllowedCIDRs: string;
  permissionServiceTrustedProxies: string;
  // v0.10.1112 — IdP TLS güveni (kurum içi CA PEM / doğrulamayı kapat).
  tlsCACertPEM: string;
  tlsInsecureSkipVerify: boolean;
  // v0.10.1120 — doğrulanmamış e-postaya güven (yalnız izinli alan adlarıyla).
  trustUnverifiedEmail: boolean;
}

/** Yetki servisi ucunun sabitleri (sunucu: internal/api/auth_permissions.go). */
export const PERMISSION_ENDPOINT = '/api/auth/permissions';
export const PERMISSION_HEADER = 'X-Coremetry-Auth-Key';
export const PERMISSION_TTL_DEFAULT = 300;
/** Liste başına en çok giriş (sunucu: auth.PermissionMaxCIDRs). */
export const PERMISSION_MAX_CIDRS = 32;

/**
 * CIDR kutusu → dizi: satır / virgül / noktalı virgül / boşluk ayraçlı, boşlar
 * ve tekrarlar atılır. Doğrulama + kanonikleştirme sunucuda (net/netip).
 */
export function parseCidrList(v: string): string[] {
  return splitList(v);
}

/**
 * Yetki servisi ağ ayarının uyarı satırları (engel değil — sunucu yine kaydeder).
 * Anahtarsız + boş izin listesi = uç ona ağdan erişebilen herkese açık.
 */
export function permissionNetWarnings(f: OidcForm): string[] {
  const cidrs = parseCidrList(f.permissionServiceAllowedCIDRs);
  const proxies = parseCidrList(f.permissionServiceTrustedProxies);
  const out: string[] = [];
  if (f.permissionServiceAllowNoKey && cidrs.length === 0) {
    out.push('Anahtarsız kabul açık ve IP izin listesi boş: uç, ona ağdan erişebilen herkese açık.');
  }
  if (cidrs.length > 0 && proxies.length === 0) {
    out.push('Güvenilen vekil listesi boş: X-Forwarded-For okunmaz — Coremetry bir ingress arkasındaysa çağıran, ingress\'in IP\'si olarak görünür.');
  }
  if (cidrs.length > PERMISSION_MAX_CIDRS || proxies.length > PERMISSION_MAX_CIDRS) {
    out.push(`Liste başına en çok ${PERMISSION_MAX_CIDRS} giriş — kayıt reddedilir.`);
  }
  return out;
}

/** Özel CA alanının tavanı (sunucu: auth.OIDCCACertPEMMax). */
export const OIDC_CA_PEM_MAX = 64 * 1024;

/** "TLS doğrulamasını kapat" açıkken görünen uyarı satırı (v0.10.1112). */
export const TLS_SKIP_VERIFY_WARNING = 'Ortadaki-adam saldırısına açık; mümkünse CA sertifikası ekleyin.';

/**
 * v0.10.1120 — "Doğrulanmamış e-postaya güven" yalnız izinli alan adı listesi
 * doluyken seçilebilir ve gönderilir (sunucu boş listeyle 400 döner, çalışma
 * anında da kapalı sayar). Liste boşaltılırsa bayrak gövdeye false gider.
 */
export function trustUnverifiedEmailAllowed(f: Pick<OidcForm, 'allowedDomains'>): boolean {
  // Sunucu (NormalizeOIDCSettings) baştaki "@"'ı atar; yalnız "@" girişi boş sayılır.
  return splitList(f.allowedDomains).some(d => d.replace(/^@/, '').trim() !== '');
}

/**
 * CA kutusunun anlık özeti — yalnız ipucu/erken uyarı; asıl doğrulama
 * sunucuda (x509, yalnız CERTIFICATE blokları). Boş = özel CA yok.
 */
export function caPemInfo(v: string): { certs: number; error?: string } {
  const t = v.replace(/\r\n/g, '\n').trim();
  if (!t) return { certs: 0 };
  if (t.includes('PRIVATE KEY-----')) {
    return { certs: 0, error: 'Özel anahtar yapıştırmayın — yalnız CA sertifikası (BEGIN CERTIFICATE).' };
  }
  if (new TextEncoder().encode(t).length > OIDC_CA_PEM_MAX) {
    return { certs: 0, error: 'En çok 64 KB — kayıt reddedilir.' };
  }
  const certs = (t.match(/-----BEGIN CERTIFICATE-----/g) ?? []).length;
  if (certs === 0) return { certs: 0, error: 'PEM sertifikası bulunamadı (-----BEGIN CERTIFICATE----- …).' };
  return { certs };
}

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
    permissionServiceAllowNoKey: !!s.permissionServiceAllowNoKey,
    permissionServiceAllowedCIDRs: (s.permissionServiceAllowedCIDRs ?? []).join('\n'),
    permissionServiceTrustedProxies: (s.permissionServiceTrustedProxies ?? []).join('\n'),
    tlsCACertPEM: s.tlsCACertPEM ?? '',
    tlsInsecureSkipVerify: !!s.tlsInsecureSkipVerify,
    trustUnverifiedEmail: !!s.trustUnverifiedEmail,
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
    permissionServiceAllowNoKey: f.permissionServiceAllowNoKey,
    permissionServiceAllowedCIDRs: parseCidrList(f.permissionServiceAllowedCIDRs),
    permissionServiceTrustedProxies: parseCidrList(f.permissionServiceTrustedProxies),
    tlsCACertPEM: f.tlsCACertPEM.trim(),
    tlsInsecureSkipVerify: f.tlsInsecureSkipVerify,
    trustUnverifiedEmail: f.trustUnverifiedEmail && trustUnverifiedEmailAllowed(f),
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
    permissionServiceAllowNoKey: false,
    permissionServiceAllowedCIDRs: [],
    permissionServiceTrustedProxies: [],
    tlsCACertPEM: '',
    tlsInsecureSkipVerify: false,
    trustUnverifiedEmail: false,
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
