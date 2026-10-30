import { useState, type FormEvent, type ReactNode } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField, TextareaField, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { KeyValue, KeyValueRow } from '@/components/coremetry/KeyValue';
import { useAuth } from '@/components/AuthProvider';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { useT } from '@/lib/i18n';
import type { OidcSettingsSnapshot, OidcTestResult } from '@/lib/types';
import { useSettingsLoad, SettingsLoadError, ConfigStatusBanner, FlashBox, humanize } from './shared';
import {
  OIDC_ROLES, PERMISSION_ENDPOINT, PERMISSION_HEADER, PERMISSION_MAX_CIDRS, TLS_SKIP_VERIFY_WARNING, caPemInfo,
  formToInput, ldapLinkInput, oidcStatus, permissionNetWarnings, publicOidcSnapshot, snapshotToForm, trustUnverifiedEmailAllowed,
  USERNAME_CLAIM_DEFAULT, type OidcForm,
} from './oidcForm';
import { SsoPresets } from './SsoPresets';

// SSOTab — v0.10.1067 (operatör: "Settings'ten yönetebilsem Helm'e göre
// daha iyi olur"). OIDC girişi artık burada yönetilir: kaydedince restart
// gerekmez, giriş sayfasındaki düğme aynı anda gelir/gider. Kayıtlı ayar
// config.yaml / Helm'deki auth.oidc'nin önüne geçer; kayıt yoksa form
// etkin config.yaml değerleriyle dolu gelir.
//
// Secret sözleşmesi (Tempo/Oracle emsali): sunucu secret'ı hiç döndürmez,
// "· kayıtlı" işareti clientSecretStored'dan; boş bırakılan kutu kayıtlıyı
// korur. "Bağlantıyı test et" yalnız keşif koşar, hiçbir şey kaydetmez.
//
// v0.10.1110 — "Yetki servisi (merkezi login)" alt bölümü: müşterinin merkezi
// login'i oturum başına POST /api/auth/permissions'ı çağırır ve dönen
// permissions dizisini access token'a gömer. Anahtar clientSecret gibi
// (kayıtlı işareti, boş = koru). "Rolü token'daki claim'den al" varsayılan
// KAPALI (operatör kararı 2026-10-05): kapalıyken giriş rolü bugünkü gibi
// varsayılan rol (viewer) + Kullanıcılar sayfası.
//
// v0.10.1111 (operatör: "Anahtarsız olmaz mı") — "Anahtarsız kabul et"
// (başlık hiç denetlenmez) + IP izin listesi + güvenilen vekiller. Anahtarsız
// ve izin listesi boşsa görünür uyarı satırı (engel değil).
//
// v0.10.1112 — Issuer altında IdP TLS güveni: özel CA (PEM) + "TLS sertifika
// doğrulamasını kapat (önerilmez)" ve açıkken kırmızı uyarı satırı.
//
// v0.10.1120 — TLS altında "Doğrulanmamış e-postaya güven (önerilmez)":
// izinli alan adları boşken pasif; açıklama i18n (sso.trustEmail.*).
//
// Admin olmayan: ayar ucu admin-only (IdP adresi / client id hassas); form
// yine BOŞ çizilmez — public /api/auth/config'ten SSO'nun açık olup
// olmadığı ve düğme etiketi gelir, alanlar salt-okunur.
//
// Faz 2 dilim 1 R10 — shadcn/coremetry: ui Field ailesi → coremetry Field
// (D1), yerel onay kutuları → CheckboxField (D13, pasifken etiket sönük),
// Kaydet satırı ActionRow (D4). `.sso-*` sayfa sınıfları utility oldu;
// yalnız AI SRE ile paylaşılan dört sınıf (settings-pane / sso-title /
// sso-note / sso-fieldset) kalır (R12'de birlikte gider). Alan sırası,
// metinler, kaydetme gövdesi aynı (secret sözleşmesi: SsoTab.secret.test).

// Eski `.sso-row` (sarar, 10px aralık) + `.sso-row > .field` (1 1 260px).
const ROW = 'mb-2.5 flex flex-wrap gap-2.5';
const GROW = 'min-w-0 flex-[1_1_260px]';
// Eski `.sso-check` (12px alt boşluk) / `--tight` (6px).
const CHECK = 'mb-3';
const CHECK_TIGHT = 'mb-1.5';
// Kırmızı / amber uyarı satırları (eski .sso-tls__warn / .sso-perm__warn).
const WARN_ERR = 'mb-2 text-[length:var(--fs-sm)] text-(--err)';
const WARN_AMBER = 'mb-2 text-[length:var(--fs-sm)] text-(--warn)';
const MONO_XS = 'font-mono text-[length:var(--fs-xs)] md:text-[length:var(--fs-xs)]';

/** "· kayıtlı" — etiket satırında, --text3 (K5: saklı hâl nötr). */
function StoredMark({ on }: { on: boolean }): ReactNode {
  return on ? <span className="text-[length:var(--fs-sm)] text-(--text3)">{' · kayıtlı'}</span> : null;
}

export function SSOTab() {
  const { user } = useAuth();
  const isAdmin = user?.role === 'admin';
  // Rol değişirse (oturum geç yüklendi) okuma yolu da değişir: key ile yeniden kur.
  return <SSOTabBody key={isAdmin ? 'admin' : 'limited'} isAdmin={isAdmin} />;
}

function SSOTabBody({ isAdmin }: { isAdmin: boolean }) {
  const [snap, setSnap] = useState<OidcSettingsSnapshot | null>(null);
  const [form, setForm] = useState<OidcForm | null>(null);
  const [busy, setBusy] = useState<'save' | 'test' | null>(null);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [tr, setTr] = useState<OidcTestResult | null>(null);

  const { loaded, error: loadErr, retry } = useSettingsLoad<OidcSettingsSnapshot>(
    () => (isAdmin ? api.getOidcSettings() : api.authConfig().then(publicOidcSnapshot)),
    s => { setSnap(s); setForm(snapshotToForm(s)); },
  );

  const patch = (p: Partial<OidcForm>) => setForm(f => (f ? { ...f, ...p } : f));

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!form || !isAdmin) return;
    setBusy('save'); setMsg(null);
    try {
      const next = await api.putOidcSettings(formToInput(form));
      setSnap(next);
      setForm(snapshotToForm(next));
      setMsg({ kind: 'ok', text: next.enabled
        ? 'Kaydedildi — SSO düğmesi giriş sayfasında, restart gerekmez.'
        : 'Kaydedildi — SSO kapalı, yalnız yerel giriş açık.' });
    } catch (err) {
      setMsg({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(null);
    }
  };

  const runTest = async () => {
    if (!form) return;
    setBusy('test'); setTr(null);
    try {
      setTr(await api.testOidcSettings(formToInput(form)));
    } catch (err) {
      setTr({ ok: false, error: humanize(err) });
    } finally {
      setBusy(null);
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded || !form || !snap) return <Spinner />;

  const status = oidcStatus(snap);
  const adminOnly = isAdmin ? undefined : 'Yalnız yönetici görür';

  return (
    <div className="settings-pane">
      <h2 className="sso-title">SSO (OIDC)</h2>
      <p className="sso-note">
        Burada kaydedilen ayar config.yaml / Helm'deki <code>auth.oidc</code>'nin önüne geçer ve
        restart'sız uygulanır; yerel kullanıcı/parola girişi her zaman açık kalır.
      </p>

      <ConfigStatusBanner label={status.label}>{status.text}</ConfigStatusBanner>
      {isAdmin && (
        <div className="mt-2 text-[length:var(--fs-xs)] text-(--text3)">
          {snap.source === 'settings'
            ? 'Kaynak: Settings.'
            : 'Kaynak: config.yaml (Helm) — Kaydet’e basınca Settings’e geçer.'}
          {snap.lastError && <> · <span className="text-(--err)">Son hata: {snap.lastError}</span></>}
        </div>
      )}
      {!isAdmin && (
        <div className="mt-2 text-[length:var(--fs-xs)] text-(--text3)">Ayarları yalnız yöneticiler görür ve değiştirir.</div>
      )}

      <form onSubmit={save} className="mt-3 rounded-lg border border-(--border) bg-(--bg2) p-4">
        <fieldset disabled={!isAdmin} className="sso-fieldset">
          <CheckboxField className={CHECK} checked={form.enabled}
            onCheckedChange={v => patch({ enabled: v })} label="SSO ile girişi aç" />

          <div className={ROW}>
            <Field className={GROW} label="Issuer URL" value={form.issuerUrl} autoComplete="off"
              onChange={e => patch({ issuerUrl: e.target.value })}
              placeholder={adminOnly ?? 'https://idp.example.test/realms/coremetry'}
              hint="Kimlik sağlayıcının adresi; https olmalı." />
            <Field className={GROW} label="Client ID" value={form.clientId} autoComplete="off"
              onChange={e => patch({ clientId: e.target.value })}
              placeholder={adminOnly ?? 'coremetry'} />
          </div>

          <OidcTlsFields form={form} patch={patch} adminOnly={adminOnly} />
          <OidcTrustEmailField form={form} patch={patch} />
          <OidcUsernameFallbackField form={form} patch={patch} />

          <div className={ROW}>
            <Field className={GROW} type="password" autoComplete="new-password" value={form.clientSecret}
              label="Client secret" labelAside={<StoredMark on={snap.clientSecretStored} />}
              onChange={e => patch({ clientSecret: e.target.value })}
              placeholder={adminOnly ?? (snap.clientSecretStored
                ? '(kayıtlı değeri korumak için boş bırakın)'
                : 'kimlik sağlayıcıdaki istemci parolası')}
              hint="Saklanır, geri gösterilmez; issuer ya da client id değişirse yeniden girin." />
            <Field className={GROW} label="Yönlendirme adresi (redirect URL)" value={form.redirectUrl} autoComplete="off"
              onChange={e => patch({ redirectUrl: e.target.value })}
              placeholder={adminOnly ?? (snap.defaultRedirectUrl || 'https://coremetry.example.test/api/auth/oidc/callback')}
              hint="Kimlik sağlayıcıda geri dönüş adresi olarak tanımlayın." />
          </div>

          <div className={ROW}>
            <Field className={GROW} label="Kapsamlar (scopes)" value={form.scopes} autoComplete="off"
              onChange={e => patch({ scopes: e.target.value })}
              placeholder={adminOnly ?? 'openid email profile'}
              hint="Boşlukla ayırın; openid zorunlu." />
            <Field className={GROW} label="Düğme etiketi" value={form.displayName} maxLength={40}
              onChange={e => patch({ displayName: e.target.value })}
              placeholder="SSO"
              hint="Giriş sayfasındaki düğmede görünür." />
          </div>

          <div className={ROW}>
            <SelectField className={GROW} label="Varsayılan rol" value={form.defaultRole}
              onChange={e => patch({ defaultRole: e.target.value })}
              hint="İlk kez SSO ile girene verilir; viewer dışı rol izinli alan adı ister.">
              {OIDC_ROLES.map(r => <option key={r} value={r}>{r}</option>)}
            </SelectField>
            <Field className={GROW} label="İzinli alan adları" value={form.allowedDomains} autoComplete="off"
              onChange={e => patch({ allowedDomains: e.target.value })}
              placeholder={adminOnly ?? 'example.test, corp.example.test'}
              hint="Virgülle ayırın; boş bırakılırsa herkes girebilir." />
          </div>

          <PermissionServiceSection form={form} snap={snap} patch={patch} adminOnly={adminOnly} />
        </fieldset>

        {tr && <OidcTestResultView tr={tr} />}
        {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}

        {isAdmin && (
          <div className="mt-3">
            <ActionRow
              secondary={<Button type="button" variant="secondary" size="sm" loading={busy === 'test'}
                disabled={busy !== null || !form.issuerUrl.trim()} onClick={() => void runTest()}>
                Bağlantıyı test et
              </Button>}
              confirm={<Button type="submit" size="sm" loading={busy === 'save'} disabled={busy !== null}>
                Kaydet
              </Button>} />
          </div>
        )}
      </form>

      <SsoPresets />
    </div>
  );
}

// IdP TLS güveni — v0.10.1112 (operatör: "oidc bağlantısının tls kontrolünü
// kapatma seçeneği de olsun sertifikaya takılıyor."). Issuer'ın hemen altında:
// kurum içi CA ile imzalı IdP için önce CA (tercih — sistem köklerine eklenir),
// son çare doğrulamayı kapat. https kuralı ve iç-ağ (loopback/metadata)
// koruması iki durumda da sürer. "Bağlantıyı test et" bu alanların
// KAYDEDİLMEMİŞ hâliyle koşar (formToInput).
function OidcTlsFields({ form, patch, adminOnly }: {
  form: OidcForm;
  patch: (p: Partial<OidcForm>) => void;
  adminOnly: string | undefined;
}) {
  const ca = caPemInfo(form.tlsCACertPEM);
  return (
    <div className="mb-2.5">
      <div className={ROW}>
        <TextareaField className={GROW} label="Özel CA sertifikası (PEM)" rows={4} value={form.tlsCACertPEM}
          controlClassName={MONO_XS} autoComplete="off" spellCheck={false}
          onChange={e => patch({ tlsCACertPEM: e.target.value })}
          placeholder={adminOnly ?? '-----BEGIN CERTIFICATE-----\n…\n-----END CERTIFICATE-----'}
          error={ca.error}
          hint={`Kurum içi CA ile imzalı IdP için; birden çok sertifika eklenebilir.${ca.certs > 0 ? ` (${ca.certs} sertifika)` : ''}`} />
      </div>
      <CheckboxField className={CHECK_TIGHT} checked={form.tlsInsecureSkipVerify}
        onCheckedChange={v => patch({ tlsInsecureSkipVerify: v })}
        label="TLS sertifika doğrulamasını kapat (önerilmez)" />
      {form.tlsInsecureSkipVerify && (
        <p className={WARN_ERR} role="alert">⚠ {TLS_SKIP_VERIFY_WARNING}</p>
      )}
    </div>
  );
}

// Doğrulanmamış e-postaya güven — v0.10.1120 (prod: "[oidc] callback failed:
// class=email_unverified"; AD/LDAP federasyonlu kurumsal IdP
// email_verified=false gönderiyor). TLS kutusunun altında; izinli alan adları
// boşken pasif + ipucu (sunucu 400 döner, gövdeye zaten false gider —
// formToInput). Kırmızı açıklama hep görünür: karar vermeden ÖNCE okunmalı.
function OidcTrustEmailField({ form, patch }: {
  form: OidcForm;
  patch: (p: Partial<OidcForm>) => void;
}) {
  const t = useT();
  const allowed = trustUnverifiedEmailAllowed(form);
  const on = allowed && form.trustUnverifiedEmail;
  return (
    <div className="mb-2.5">
      <CheckboxField className={CHECK_TIGHT} checked={on} disabled={!allowed}
        onCheckedChange={v => patch({ trustUnverifiedEmail: v })}
        label={t('sso.trustEmail.label')} />
      {!allowed && <p className="mb-1.5 text-[length:var(--fs-xs)] text-(--text3)">{t('sso.trustEmail.needsDomains')}</p>}
      <p className={WARN_ERR} role={on ? 'alert' : undefined}>⚠ {t('sso.trustEmail.warning')}</p>
      <OidcLdapLinkField form={form} patch={patch} trustOn={on} />
    </div>
  );
}

// Mevcut LDAP hesabını kullanıcı adıyla SSO'ya bağla — v0.10.1403 (operatör:
// "Bu hesap doğrulanmamış e-posta ile SSO'dan açılamaz" → "Coremetry tarafını
// yap" + "LDAP'ı bir süre sonra hiç kullanmayacağım, sadece merkezi login
// Keycloak üzerinden olacak"). Güven kutusunun içinde, ona bağlı: güven
// kapalıyken pasif (gövdeye false gider — ldapLinkInput). Admin alt seçeneği
// yalnız bağlama açıkken. Kırmızı ön koşul uyarısı bağlama açıkken görünür.
function OidcLdapLinkField({ form, patch, trustOn }: {
  form: OidcForm;
  patch: (p: Partial<OidcForm>) => void;
  trustOn: boolean;
}) {
  const t = useT();
  const { linkLdapByUsername: on, linkLdapByUsernameAllowAdmin: adminOn } = ldapLinkInput(form);
  return (
    <div className="mt-1.5 ml-2.5" data-testid="sso-ldap-link">
      <CheckboxField className={CHECK_TIGHT} checked={on} disabled={!trustOn}
        onCheckedChange={v => patch({ linkLdapByUsername: v })}
        label={t('sso.ldapLink.label')} />
      <p className="sso-note">{t('sso.ldapLink.note')}</p>
      {on && <p className={WARN_ERR} role="alert">⚠ {t('sso.ldapLink.warning')}</p>}
      <CheckboxField className={CHECK_TIGHT} checked={adminOn} disabled={!on}
        onCheckedChange={v => patch({ linkLdapByUsernameAllowAdmin: v })}
        label={t('sso.ldapLinkAdmin.label')} />
      {adminOn && <p className={WARN_ERR} role="alert">⚠ {t('sso.ldapLinkAdmin.warning')}</p>}
    </div>
  );
}

// Kullanıcı adıyla eşleştirme — v0.10.1121 (prod: "[oidc] callback failed:
// class=email_missing"; id_token'da email yok, preferred_username = AD sicili).
// Kutunun altındaki açıklama DURUMA göre değişir: kapalıyken ne olacağı
// (email_missing + IdP'de tercih edilen düzeltme), açıkken zincirin tamamı —
// operatör hangi yolun etkin olduğunu formda görür.
function OidcUsernameFallbackField({ form, patch }: {
  form: OidcForm;
  patch: (p: Partial<OidcForm>) => void;
}) {
  const t = useT();
  const on = form.usernameFallback;
  // v0.10.1403 — claim adı LDAP bağlamasında da okunur.
  const claimUsed = on || ldapLinkInput(form).linkLdapByUsername;
  return (
    <div className="mb-2.5">
      <CheckboxField className={CHECK_TIGHT} checked={on}
        onCheckedChange={v => patch({ usernameFallback: v })}
        label={t('sso.usernameFallback.label')} />
      <p className="sso-note" role="status">{t(on ? 'sso.usernameFallback.on' : 'sso.usernameFallback.off')}</p>
      {on && <p className={WARN_ERR}>⚠ {t('sso.usernameFallback.risk')}</p>}
      <CheckboxField className={CHECK_TIGHT} checked={on && form.usernameFallbackAllowAdmin} disabled={!on}
        onCheckedChange={v => patch({ usernameFallbackAllowAdmin: v })}
        label={t('sso.usernameAdmin.label')} />
      {on && form.usernameFallbackAllowAdmin && (
        <p className={WARN_ERR} role="alert">⚠ {t('sso.usernameAdmin.warning')}</p>
      )}
      <div className={ROW}>
        <Field className={GROW} label={t('sso.usernameClaim.label')} value={form.usernameClaim} autoComplete="off" maxLength={64}
          spellCheck={false} disabled={!claimUsed}
          onChange={e => patch({ usernameClaim: e.target.value })}
          placeholder={USERNAME_CLAIM_DEFAULT}
          hint={t('sso.usernameClaim.hint')} />
      </div>
    </div>
  );
}

// Yetki servisi (merkezi login) — v0.10.1110. Ayrı bileşen: SSO alanlarıyla
// aynı form/fieldset (tek Kaydet, tek PUT), ama okuyucu bölümü ayrı görür.
function PermissionServiceSection({ form, snap, patch, adminOnly }: {
  form: OidcForm;
  snap: OidcSettingsSnapshot;
  patch: (p: Partial<OidcForm>) => void;
  adminOnly: string | undefined;
}) {
  const role = form.defaultRole || 'viewer';
  const warnings = permissionNetWarnings(form);
  return (
    <section className="mt-4 border-t border-(--border) pt-3" aria-label="Yetki servisi (merkezi login)">
      <h3 className="mb-1.5 text-[length:var(--fs-sm)] font-semibold">Yetki servisi (merkezi login)</h3>
      <p className="sso-note">
        Merkezi login, kullanıcı oturumu başına bir kez bu uca sorar ve dönen yetkileri access
        token'a <code>{form.permissionsClaim || 'permissions'}</code> claim'i olarak koyar. Roller
        Coremetry'de yönetilir; servis yalnız kullanıcının rolünü bildirir. Coremetry'de tanımsız
        kullanıcıya varsayılan rol (<b>{role}</b>) bildirilir — herkes girebilir; yalnız devre dışı
        bırakılmış hesaba yetki verilmez (204).
      </p>
      <CheckboxField className={CHECK} checked={form.permissionServiceEnabled}
        onCheckedChange={v => patch({ permissionServiceEnabled: v })} label="Yetki servisini aç" />
      <CheckboxField className={CHECK_TIGHT} checked={form.permissionServiceAllowNoKey}
        onCheckedChange={v => patch({ permissionServiceAllowNoKey: v })} label="Anahtarsız kabul et" />
      <p className="sso-note mb-2.5">
        Başlık hiç denetlenmez — uç ağdan erişen herkese açık olur; IP izin listesi kullanın.
      </p>
      <div className={ROW}>
        <Field className={GROW} type="password" autoComplete="new-password" value={form.permissionServiceKey}
          label="Paylaşılan anahtar" labelAside={<StoredMark on={snap.permissionServiceKeySet} />}
          onChange={e => patch({ permissionServiceKey: e.target.value })}
          placeholder={adminOnly ?? (snap.permissionServiceKeySet
            ? '(kayıtlı değeri korumak için boş bırakın)'
            : form.permissionServiceAllowNoKey
              ? '(isteğe bağlı — anahtarsız kabul açık)'
              : 'en az 16 karakter, ör. openssl rand -hex 32')}
          hint={form.permissionServiceAllowNoKey
            ? 'Anahtarsız kabul açıkken kullanılmaz (isteğe bağlı). Saklanır, geri gösterilmez.'
            : 'Merkezi login bu değeri başlıkta gönderir. Saklanır, geri gösterilmez.'} />
        <Field className={GROW} type="number" min={60} max={3600} step={1} value={form.permissionTTLSeconds}
          label="Önbellek süresi (ttlSeconds)"
          onChange={e => patch({ permissionTTLSeconds: e.target.value })}
          hint="Merkezi login'in cevabı önbellekte tutma süresi; 60–3600 sn, varsayılan 300." />
      </div>
      <div className={ROW}>
        <Field className={GROW} label="Claim adı" value={form.permissionsClaim} autoComplete="off" maxLength={64}
          onChange={e => patch({ permissionsClaim: e.target.value })}
          placeholder="permissions"
          hint="Token'da yetkilerin bulunduğu claim; varsayılan permissions." />
      </div>
      <div className={ROW}>
        <TextareaField className={GROW} label="IP izin listesi (CIDR)" rows={3} value={form.permissionServiceAllowedCIDRs}
          autoComplete="off" spellCheck={false}
          onChange={e => patch({ permissionServiceAllowedCIDRs: e.target.value })}
          placeholder={adminOnly ?? '203.0.113.0/24\n2001:db8::/32'}
          hint={`Satır başına bir IP ya da CIDR (en çok ${PERMISSION_MAX_CIDRS}); boş = IP kısıtı yok. Listede olmayan çağırana 403.`} />
        <TextareaField className={GROW} label="Güvenilen vekiller (ingress)" rows={3} value={form.permissionServiceTrustedProxies}
          autoComplete="off" spellCheck={false}
          onChange={e => patch({ permissionServiceTrustedProxies: e.target.value })}
          placeholder={adminOnly ?? '10.0.0.0/8'}
          hint="X-Forwarded-For yalnız bu adreslerden gelen istekte okunur; boşsa doğrudan bağlanan adres kullanılır." />
      </div>
      {warnings.map(w => (
        <p key={w} className={WARN_AMBER} role="status" data-testid="sso-perm-warn">⚠ {w}</p>
      ))}
      <CheckboxField className={CHECK_TIGHT} checked={form.roleFromClaim}
        onCheckedChange={v => patch({ roleFromClaim: v })} label="Rolü token'daki claim'den al" />
      <p className="sso-note mb-2.5">
        {form.roleFromClaim
          ? <>Açık: claim (dizi) <code>COREMETRY_ADMIN</code> / <code>COREMETRY_EDITOR</code> / <code>COREMETRY_VIEWER</code>
            taşıyor ve kayıtlı rolden DÜŞÜKSE kullanıcının rolü SSO girişinde ona düşürülür; claim'den rol yalnız
            düşürür, yükseltme Kullanıcılar sayfasından. Claim yoksa rol değişmez; ilk kez giren kullanıcı yine
            varsayılan rolle (<b>{role}</b>) açılır.</>
          : <>Kapalı (varsayılan): SSO ile ilk kez giren kullanıcı varsayılan rolle (<b>{role}</b>) açılır; rol
            yalnız Coremetry'nin Kullanıcılar sayfasından değişir, token'daki claim yok sayılır.</>}
      </p>
      <KeyValue>
        <KeyValueRow k="Uç" v={`POST ${PERMISSION_ENDPOINT}`} mono />
        <KeyValueRow k="Başlık" v={form.permissionServiceAllowNoKey ? `${PERMISSION_HEADER} (denetlenmiyor)` : PERMISSION_HEADER} mono />
      </KeyValue>
    </section>
  );
}

// Test sonucu — yalnız keşif alanları (sunucu ham gövde döndürmez).
function OidcTestResultView({ tr }: { tr: OidcTestResult }) {
  const d = tr.discovery;
  return (
    <div className="mt-2.5 rounded-sm border border-(--border) bg-(--bg1) px-3 py-2.5 text-[length:var(--fs-sm)]"
      role="status" aria-live="polite" data-testid="sso-result">
      <div className={cn('mb-1.5 font-semibold', tr.ok ? 'text-(--ok)' : 'text-(--err)')}>
        {tr.ok ? '✓ Kimlik sağlayıcı bulundu' : `✗ Bağlantı başarısız: ${tr.error ?? 'bilinmeyen hata'}`}
      </div>
      {tr.ok && d && (
        <KeyValue>
          <KeyValueRow k="Issuer" v={d.issuer} mono />
          <KeyValueRow k="Yetkilendirme" v={d.authorizationEndpoint} mono />
          <KeyValueRow k="Token" v={d.tokenEndpoint} mono />
          {d.userinfoEndpoint && <KeyValueRow k="Userinfo" v={d.userinfoEndpoint} mono />}
          <KeyValueRow k="JWKS" v={d.jwksUri} mono />
          {d.unsupportedScopes && d.unsupportedScopes.length > 0 && (
            <KeyValueRow k="Uyarı"
              v={<span className="text-(--err)">IdP bu kapsamları listelemiyor: {d.unsupportedScopes.join(', ')}</span>} />
          )}
        </KeyValue>
      )}
    </div>
  );
}
