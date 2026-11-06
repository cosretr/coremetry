import { useEffect, useState, FormEvent } from 'react';
import { useAuth } from '@/components/AuthProvider';
import { TelescopeIcon } from '@/components/TelescopeIcon';
import { Wordmark } from '@/components/Wordmark';
import { Button } from '@/components/shadcn/button';
import { Separator } from '@/components/shadcn/separator';
import { Field } from '@/components/coremetry/Field';
import { api, type AuthConfigResponse } from '@/lib/api';
import { useBranding } from '@/lib/branding';
import { oidcStartHref } from '@/lib/postLoginRedirect';
import { useT } from '@/lib/i18n';
import { LoginTopBar } from './LoginTopBar';

// v0.10.1906 — Login yeniden tasarımı dilim 2.1 (audit §3, §6.1, §6.4). Dosya
// `pages/login/` altında: Tailwind yalnız index.css `@source` dizinlerini tarar
// (`pages/Login.tsx` bunlardan biri değil); `pages/Login.tsx` ince yeniden-dışa-aktarımdır.
//
// shadcn + Tailwind, üst çubuk (marka · TR/EN · tema), sabit İngilizce kalan
// metinler i18n anahtarına bağlı. Davranış envanteri (audit §3) bire bir aynı:
// auth config + demo oto-giriş, SSO, LDAP bandı, hata paneli, marka alanları,
// sürüm satırı. Başarı sonrası yönlendirme Login'de DEĞİL AuthProvider'da.

/** Hata durumu: 401 metni dile bağlı çizilir (`invalid`), ham mesaj olduğu gibi. */
interface LoginError { text: string; invalid?: boolean }

export default function LoginPage() {
  const { login } = useAuth();
  // Admin-set branding overlay (logo, login title, button label,
  // username field label, footer line). Falls back to the
  // bundled defaults for any field left empty.
  const brand = useBranding();
  const t = useT();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<LoginError | null>(null);
  const [config, setConfig] = useState<AuthConfigResponse | null>(null);

  // Pull auth config + auto-sign-in when demo mode is on. The demo
  // viewer creds are intentionally public (they're returned by an
  // unauth'd endpoint) so the user lands directly in the app
  // without seeing this form. On non-demo deployments the form
  // renders normally and the operator types their own creds.
  useEffect(() => {
    api.authConfig().then(c => {
      setConfig(c);
      if (c.demo?.enabled && c.demo.email && c.demo.password) {
        setEmail(c.demo.email);
        setPassword(c.demo.password);
        // Auto-submit. Don't block on user interaction.
        setBusy(true);
        login(c.demo.email, c.demo.password).catch((err: unknown) => {
          setError({ text: err instanceof Error ? err.message : t('login.demoFailed') });
          setBusy(false);
        });
      }
    }).catch(() => setConfig({
      local: { enabled: true }, oidc: { enabled: false },
    }));
    // t yalnız hata yedek metni için; dil değişimi config isteğini YENİDEN koşturmasın.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [login]);

  // Surface OIDC failure messages bubbled back via ?error=…
  useEffect(() => {
    if (typeof window === 'undefined') return;
    const params = new URLSearchParams(window.location.search);
    const e = params.get('error');
    if (e) setError({ text: t('login.ssoFailed').replace('{e}', e) });
    // Yalnız açılışta okunur (URL'den bir kez).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      await login(email.trim(), password);
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : t('login.failed');
      setError(msg.includes('invalid credentials') || msg.includes('401')
        ? { text: msg, invalid: true }
        : { text: msg });
    } finally {
      setBusy(false);
    }
  };

  const oidcEnabled = !!config?.oidc.enabled;
  const oidcName = config?.oidc.displayName ?? '';
  const oidcLabel = oidcName
    ? t('login.ssoLabel').replace('{name}', oidcName)
    : t('login.ssoNoName');
  const demoEnabled = !!config?.demo?.enabled;
  const ldapEnabled = !!config?.ldap?.enabled;

  // Build version is unauthenticated so we can show it before the
  // operator has a session — useful for support / matching a
  // running instance to a release tag.
  //
  // v0.9.339 — TWO independent sources, because one of them is failing in
  // prod. /api/version is authoritative at runtime (it honours
  // COREMETRY_VERSION); the bundle constant is baked at build time and is
  // available even when the API is unreachable. The seed means the line
  // renders immediately and never blanks; the fetch overwrites it when it
  // succeeds. A failure now costs freshness, not the whole affordance.
  const buildVersion = (import.meta.env.VITE_APP_VERSION as string | undefined) ?? '';
  const [version, setVersion] = useState<string>(buildVersion);
  useEffect(() => {
    api.version()
      .then(v => { if (v?.version) setVersion(v.version); })
      .catch(err => {
        // Not silent: whoever is asking "why does prod show nothing" gets the
        // actual reason here instead of an empty element.
        console.warn('[login] /api/version unreachable — showing the build-time version instead:', err);
      });
  }, []);

  const invalidMsg = t('login.invalid');
  const ldapParts = t('login.ldapNote').split('{account}');

  return (
    // v0.9.981 (D3.4/D3.5) — kaydırma kaçışı: `min-h-dvh` + `overflow-auto`
    // ortalamayı verir, taşmayı kaydırılabilir bırakır (`fixed inset-0` telefonda
    // SSO + hata + bilgi bandı birlikte görününce alt kısmı erişilemez yapıyordu;
    // `dvh` iOS araç çubuğunu sayar). Zemin `--bg0` (redhat'te `--bg` ≠ uygulama
    // zemini; giriş → uygulama geçişinde zemin sıçramasın).
    <div className="relative grid min-h-dvh place-items-center overflow-auto bg-(--bg0) px-6 pb-6 pt-16">
      <LoginTopBar />
      {/* 340px SABİTTİ: 320px'lik viewport'ta form yatay taşıyordu. Tavan aynı,
          taban akışkan. */}
      <div className="w-[min(340px,100%)] rounded-lg border border-border bg-card shadow-pop">
        <form onSubmit={onSubmit} className="p-8">
          <div className="mb-6 text-center">
            <h1 className="m-0 flex items-center justify-center gap-2 text-[length:var(--fs-xl)] font-bold tracking-tight">
              <Wordmark name={brand.appName} />
              {/* Operatör logosu üst çubukta; OpenTelemetry işareti ürün adının
                  ARKASINDA (v0.5.196 eş-marka sırası) logo yüklüyken kalır. */}
              {brand.logoDataUri && <TelescopeIcon size={22} />}
            </h1>
            <div className="mt-1 text-[length:var(--fs-xs)] text-(--text3)">
              {brand.loginTitle === `Sign in to ${brand.appName}`
                ? t('login.signInToContinue')
                : brand.loginTitle}
            </div>
            {brand.loginSubtitle && (
              <div className="mt-2 text-[length:var(--fs-sm)] leading-normal text-muted-foreground">
                {brand.loginSubtitle}
              </div>
            )}
          </div>

          {demoEnabled && (
            // v0.10.929 (K5) — bilgi notu ne geçiş ne veri: yeşil değil, nötr token.
            <div className="mb-3.5 rounded-md border border-border bg-secondary px-3 py-2 text-[length:var(--fs-sm)] leading-snug text-muted-foreground">
              <b className="text-foreground">{t('login.demoTitle')}</b> — {t('login.demoBody')}
            </div>
          )}

          {oidcEnabled && (
            <>
              <Button variant="secondary" size="lg" className="mb-3.5 w-full"
                onClick={() => { window.location.href = oidcStartHref(); }}>
                ⚿ {oidcLabel}
              </Button>
              <div className="mx-0 mb-3.5 mt-1.5 flex items-center gap-2 text-[length:var(--fs-xs)] text-(--text3)">
                <Separator className="flex-1 bg-(--divider)" />
                <span>{t('login.orLocal')}</span>
                <Separator className="flex-1 bg-(--divider)" />
              </div>
            </>
          )}

          {ldapEnabled && (
            <div className="mb-3.5 rounded-md border border-(--accent-border) bg-(--accent-bg) px-2.5 py-1.5 text-[length:var(--fs-xs)] leading-snug text-muted-foreground">
              {ldapParts[0]}
              <b className="text-foreground">{t('login.ldapAccount')}</b>
              {ldapParts[1]}
            </div>
          )}

          {/* Always text — `type="email"` triggered HTML5 @-validation
              which blocked LDAP users typing a bare sAMAccountName, and
              before /api/auth/config resolved the flag flipped briefly
              anyway. Backend owns the format check; the input type
              doesn't add safety beyond a soft hint, so we drop it. */}
          <Field id="login-username" type="text" autoComplete="username" required autoFocus
            className="mb-3.5" controlClassName="h-9"
            label={ldapEnabled
              ? (brand.usernameLabel === 'Email' ? t('login.usernameOrEmail') : brand.usernameLabel)
              : (brand.usernameLabel === 'Email' ? t('login.email') : brand.usernameLabel)}
            value={email} onChange={e => setEmail(e.target.value)} />

          <Field id="login-password" type="password" autoComplete="current-password" required
            className="mb-[18px]" controlClassName="h-9" label={t('login.password')}
            value={password} onChange={e => setPassword(e.target.value)} />

          {error && (
            <div role="alert" className="mb-3 rounded border border-status-critical-border bg-status-critical-bg px-2.5 py-1.5 text-[length:var(--fs-sm)] text-status-critical">
              <div>{error.invalid ? invalidMsg : error.text}</div>
              {error.invalid && (
                <div className="mt-1.5 text-[length:var(--fs-xs)] font-normal text-muted-foreground">
                  {t('login.invalidHint')}
                </div>
              )}
            </div>
          )}

          <Button variant="default" size="lg" type="submit" disabled={busy} className="w-full">
            {busy
              ? t('login.signingIn')
              : (brand.signInButtonLabel === 'Sign in' ? t('login.signIn') : brand.signInButtonLabel)}
          </Button>

          {brand.footerText && (
            <div className="mt-3.5 text-center text-[length:var(--fs-xs)] leading-normal text-muted-foreground">
              {brand.footerText}
            </div>
          )}

          {/* Build version — the "OpenTelemetry-native platform" tag stays even
              with a custom brand override: it positions Coremetry as native to
              the open standard, not a vendor attribution watermark (v0.5.196). */}
          {version && (
            <div className="mt-[18px] text-center font-mono text-[length:var(--fs-3xs)] tracking-[0.3px] text-(--text3)">
              {brand.appName} {version}
              <span className="ml-1.5">
                · an{' '}
                <a href="https://opentelemetry.io/ecosystem/adopters/"
                   target="_blank" rel="noopener"
                   className="text-(--accent2) no-underline">
                  OpenTelemetry
                </a>
                -native platform
              </span>
            </div>
          )}
        </form>
      </div>
    </div>
  );
}
