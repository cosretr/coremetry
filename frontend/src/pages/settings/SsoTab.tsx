import { useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button, Field, KeyValue, KeyValueRow, SelectField, TextareaField } from '@/components/ui';
import { useAuth } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import type { OidcSettingsSnapshot, OidcTestResult } from '@/lib/types';
import { useSettingsLoad, SettingsLoadError, ConfigStatusBanner, FlashBox, humanize } from './shared';
import {
  OIDC_ROLES, PERMISSION_ENDPOINT, PERMISSION_HEADER, PERMISSION_MAX_CIDRS, formToInput, oidcStatus,
  permissionNetWarnings, publicOidcSnapshot, snapshotToForm, type OidcForm,
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
// Admin olmayan: ayar ucu admin-only (IdP adresi / client id hassas); form
// yine BOŞ çizilmez — public /api/auth/config'ten SSO'nun açık olup
// olmadığı ve düğme etiketi gelir, alanlar salt-okunur.
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
        <div className="sso-source">
          {snap.source === 'settings'
            ? 'Kaynak: Settings.'
            : 'Kaynak: config.yaml (Helm) — Kaydet’e basınca Settings’e geçer.'}
          {snap.lastError && <> · <span className="is-err">Son hata: {snap.lastError}</span></>}
        </div>
      )}
      {!isAdmin && (
        <div className="sso-source">Ayarları yalnız yöneticiler görür ve değiştirir.</div>
      )}

      <form onSubmit={save} className="sso-form">
        <fieldset disabled={!isAdmin} className="sso-fieldset">
          <label className="sso-check">
            <input type="checkbox" checked={form.enabled}
              onChange={e => patch({ enabled: e.target.checked })} />
            <span>SSO ile girişi aç</span>
          </label>

          <div className="sso-row">
            <Field label="Issuer URL" value={form.issuerUrl} autoComplete="off"
              onChange={e => patch({ issuerUrl: e.target.value })}
              placeholder={adminOnly ?? 'https://idp.example.test/realms/coremetry'}
              hint="Kimlik sağlayıcının adresi; https olmalı." />
            <Field label="Client ID" value={form.clientId} autoComplete="off"
              onChange={e => patch({ clientId: e.target.value })}
              placeholder={adminOnly ?? 'coremetry'} />
          </div>

          <div className="sso-row">
            <Field type="password" autoComplete="new-password" value={form.clientSecret}
              label={<>Client secret{snap.clientSecretStored && <span style={{ color: 'var(--text3)' }}> · kayıtlı</span>}</>}
              onChange={e => patch({ clientSecret: e.target.value })}
              placeholder={adminOnly ?? (snap.clientSecretStored
                ? '(kayıtlı değeri korumak için boş bırakın)'
                : 'kimlik sağlayıcıdaki istemci parolası')}
              hint="Saklanır, geri gösterilmez; issuer ya da client id değişirse yeniden girin." />
            <Field label="Yönlendirme adresi (redirect URL)" value={form.redirectUrl} autoComplete="off"
              onChange={e => patch({ redirectUrl: e.target.value })}
              placeholder={adminOnly ?? (snap.defaultRedirectUrl || 'https://coremetry.example.test/api/auth/oidc/callback')}
              hint="Kimlik sağlayıcıda geri dönüş adresi olarak tanımlayın." />
          </div>

          <div className="sso-row">
            <Field label="Kapsamlar (scopes)" value={form.scopes} autoComplete="off"
              onChange={e => patch({ scopes: e.target.value })}
              placeholder={adminOnly ?? 'openid email profile'}
              hint="Boşlukla ayırın; openid zorunlu." />
            <Field label="Düğme etiketi" value={form.displayName} maxLength={40}
              onChange={e => patch({ displayName: e.target.value })}
              placeholder="SSO"
              hint="Giriş sayfasındaki düğmede görünür." />
          </div>

          <div className="sso-row">
            <SelectField label="Varsayılan rol" value={form.defaultRole}
              onChange={e => patch({ defaultRole: e.target.value })}
              hint="İlk kez SSO ile girene verilir; viewer dışı rol izinli alan adı ister.">
              {OIDC_ROLES.map(r => <option key={r} value={r}>{r}</option>)}
            </SelectField>
            <Field label="İzinli alan adları" value={form.allowedDomains} autoComplete="off"
              onChange={e => patch({ allowedDomains: e.target.value })}
              placeholder={adminOnly ?? 'example.test, corp.example.test'}
              hint="Virgülle ayırın; boş bırakılırsa herkes girebilir." />
          </div>

          <PermissionServiceSection form={form} snap={snap} patch={patch} adminOnly={adminOnly} />
        </fieldset>

        {tr && <OidcTestResultView tr={tr} />}
        {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}

        {isAdmin && (
          <div className="sso-actions">
            <Button type="button" variant="secondary" loading={busy === 'test'}
              disabled={busy !== null || !form.issuerUrl.trim()} onClick={() => void runTest()}>
              Bağlantıyı test et
            </Button>
            <Button type="submit" variant="primary" loading={busy === 'save'} disabled={busy !== null}>
              Kaydet
            </Button>
          </div>
        )}
      </form>

      <SsoPresets />
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
    <section className="sso-perm" aria-label="Yetki servisi (merkezi login)">
      <h3 className="sso-subtitle">Yetki servisi (merkezi login)</h3>
      <p className="sso-note">
        Merkezi login, kullanıcı oturumu başına bir kez bu uca sorar ve dönen yetkileri access
        token'a <code>{form.permissionsClaim || 'permissions'}</code> claim'i olarak koyar. Roller
        Coremetry'de yönetilir; servis yalnız kullanıcının rolünü bildirir. Coremetry'de tanımsız
        kullanıcıya varsayılan rol (<b>{role}</b>) bildirilir — herkes girebilir; yalnız devre dışı
        bırakılmış hesaba yetki verilmez (204).
      </p>
      <label className="sso-check">
        <input type="checkbox" checked={form.permissionServiceEnabled}
          onChange={e => patch({ permissionServiceEnabled: e.target.checked })} />
        <span>Yetki servisini aç</span>
      </label>
      <label className="sso-check sso-check--tight">
        <input type="checkbox" checked={form.permissionServiceAllowNoKey}
          onChange={e => patch({ permissionServiceAllowNoKey: e.target.checked })} />
        <span>Anahtarsız kabul et</span>
      </label>
      <p className="sso-note sso-perm__explain">
        Başlık hiç denetlenmez — uç ağdan erişen herkese açık olur; IP izin listesi kullanın.
      </p>
      <div className="sso-row">
        <Field type="password" autoComplete="new-password" value={form.permissionServiceKey}
          label={<>Paylaşılan anahtar{snap.permissionServiceKeySet && <span style={{ color: 'var(--text3)' }}> · kayıtlı</span>}</>}
          onChange={e => patch({ permissionServiceKey: e.target.value })}
          placeholder={adminOnly ?? (snap.permissionServiceKeySet
            ? '(kayıtlı değeri korumak için boş bırakın)'
            : form.permissionServiceAllowNoKey
              ? '(isteğe bağlı — anahtarsız kabul açık)'
              : 'en az 16 karakter, ör. openssl rand -hex 32')}
          hint={form.permissionServiceAllowNoKey
            ? 'Anahtarsız kabul açıkken kullanılmaz (isteğe bağlı). Saklanır, geri gösterilmez.'
            : 'Merkezi login bu değeri başlıkta gönderir. Saklanır, geri gösterilmez.'} />
        <Field type="number" min={60} max={3600} step={1} value={form.permissionTTLSeconds}
          label="Önbellek süresi (ttlSeconds)"
          onChange={e => patch({ permissionTTLSeconds: e.target.value })}
          hint="Merkezi login'in cevabı önbellekte tutma süresi; 60–3600 sn, varsayılan 300." />
      </div>
      <div className="sso-row">
        <Field label="Claim adı" value={form.permissionsClaim} autoComplete="off" maxLength={64}
          onChange={e => patch({ permissionsClaim: e.target.value })}
          placeholder="permissions"
          hint="Token'da yetkilerin bulunduğu claim; varsayılan permissions." />
      </div>
      <div className="sso-row">
        <TextareaField label="IP izin listesi (CIDR)" rows={3} value={form.permissionServiceAllowedCIDRs}
          autoComplete="off" spellCheck={false}
          onChange={e => patch({ permissionServiceAllowedCIDRs: e.target.value })}
          placeholder={adminOnly ?? '203.0.113.0/24\n2001:db8::/32'}
          hint={`Satır başına bir IP ya da CIDR (en çok ${PERMISSION_MAX_CIDRS}); boş = IP kısıtı yok. Listede olmayan çağırana 403.`} />
        <TextareaField label="Güvenilen vekiller (ingress)" rows={3} value={form.permissionServiceTrustedProxies}
          autoComplete="off" spellCheck={false}
          onChange={e => patch({ permissionServiceTrustedProxies: e.target.value })}
          placeholder={adminOnly ?? '10.0.0.0/8'}
          hint="X-Forwarded-For yalnız bu adreslerden gelen istekte okunur; boşsa doğrudan bağlanan adres kullanılır." />
      </div>
      {warnings.map(w => (
        <p key={w} className="sso-perm__warn" role="status">⚠ {w}</p>
      ))}
      <label className="sso-check sso-check--tight">
        <input type="checkbox" checked={form.roleFromClaim}
          onChange={e => patch({ roleFromClaim: e.target.checked })} />
        <span>Rolü token'daki claim'den al</span>
      </label>
      <p className="sso-note sso-perm__explain">
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
    <div className="sso-result" role="status" aria-live="polite">
      <div className="sso-result__head" style={{ color: tr.ok ? 'var(--ok)' : 'var(--err)' }}>
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
              v={<span className="is-err">IdP bu kapsamları listelemiyor: {d.unsupportedScopes.join(', ')}</span>} />
          )}
        </KeyValue>
      )}
    </div>
  );
}
