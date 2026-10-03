import { useState } from 'react';
import { TabStrip } from '@/components/ui/TabStrip'; // v0.10.456 (D5)
import { Button } from '@/components/ui/Button';
import { DisclosureButton } from '@/components/ui';
import { copyToClipboard } from '@/lib/clipboard';

// SsoPresets — sağlayıcı başına örnek ayar (OIDC + trusted-header proxy
// kipi). v0.10.1067'e dek SSO sekmesinin TEK içeriğiydi (yapılandırma
// yalnız config.yaml / env'den); artık OIDC Settings formundan yönetiliyor
// (SsoTab.tsx) ve bu blok formun altında, kapalı gelen bir başvuru:
// issuer URL kalıbı, önerilen kapsamlar, sağlayıcıya özgü notlar (Azure
// tenant yer tutucusu, Keycloak realm parçası, oauth2-proxy CIDR'ı).
// Trusted-header kipi hâlâ yalnız config.yaml'dan kurulur.
export function SsoPresets() {
  const [open, setOpen] = useState(false);
  return (
    <div className="sso-presets">
      <DisclosureButton anatomy="section" expanded={open} onClick={() => setOpen(o => !o)}>
        Örnek IdP ayarları
      </DisclosureButton>
      {open && <SsoPresetsBody />}
    </div>
  );
}

function SsoPresetsBody() {
  type Preset = { key: string; label: string; description: string; yaml: string };
  const presets: Preset[] = [
    {
      key: 'keycloak',
      label: 'Keycloak',
      description: 'Most common self-hosted identity provider for banks. Replace <realm> with your realm name; Coremetry discovers the rest via /.well-known/openid-configuration.',
      yaml:
`auth:
  oidc:
    enabled: true
    issuer_url: "https://keycloak.example.com/realms/<realm>"
    client_id: "coremetry"
    client_secret: "<from-keycloak-client-credentials>"
    redirect_url: "https://coremetry.example.com/api/auth/oidc/callback"
    scopes: ["openid", "email", "profile"]
    display_name: "Keycloak"
    default_role: "viewer"
    allowed_domains: []   # optional ["example.test"]`,
    },
    {
      key: 'dex',
      label: 'Dex',
      description: 'CoreOS Dex — popular OIDC bridge in front of LDAP/SAML/GitHub for k8s shops. Issuer URL is the public host:port the SPA can reach.',
      yaml:
`auth:
  oidc:
    enabled: true
    issuer_url: "https://dex.example.com"
    client_id: "coremetry"
    client_secret: "<dex-static-client-secret>"
    redirect_url: "https://coremetry.example.com/api/auth/oidc/callback"
    scopes: ["openid", "email", "profile", "groups"]
    display_name: "Dex"
    default_role: "viewer"`,
    },
    {
      key: 'google',
      label: 'Google Workspace',
      description: 'Hosted Google. Restrict to a single GSuite domain via allowed_domains so anyone with a personal gmail.com can\'t sign in.',
      yaml:
`auth:
  oidc:
    enabled: true
    issuer_url: "https://accounts.google.com"
    client_id: "<google-cloud-oauth-client-id>"
    client_secret: "<google-cloud-oauth-secret>"
    redirect_url: "https://coremetry.example.com/api/auth/oidc/callback"
    scopes: ["openid", "email", "profile"]
    display_name: "Google"
    default_role: "viewer"
    allowed_domains: ["yourcompany.com"]`,
    },
    {
      key: 'azure-ad',
      label: 'Azure AD (Entra)',
      description: 'Microsoft Entra ID (formerly Azure AD). Replace <tenant-id> with your tenant GUID; the v2.0 endpoint is the one to use.',
      yaml:
`auth:
  oidc:
    enabled: true
    issuer_url: "https://login.microsoftonline.com/<tenant-id>/v2.0"
    client_id: "<app-registration-client-id>"
    client_secret: "<app-registration-client-secret>"
    redirect_url: "https://coremetry.example.com/api/auth/oidc/callback"
    scopes: ["openid", "email", "profile"]
    display_name: "Microsoft"
    default_role: "viewer"`,
    },
    {
      key: 'okta',
      label: 'Okta',
      description: 'Okta-as-a-service. Replace <your-okta-domain> with the host Okta assigned you (e.g. acme.okta.com).',
      yaml:
`auth:
  oidc:
    enabled: true
    issuer_url: "https://<your-okta-domain>"
    client_id: "<okta-app-client-id>"
    client_secret: "<okta-app-client-secret>"
    redirect_url: "https://coremetry.example.com/api/auth/oidc/callback"
    scopes: ["openid", "email", "profile"]
    display_name: "Okta"
    default_role: "viewer"`,
    },
    {
      key: 'auth0',
      label: 'Auth0',
      description: 'Hosted Auth0. Issuer URL includes the tenant slug.',
      yaml:
`auth:
  oidc:
    enabled: true
    issuer_url: "https://<your-tenant>.auth0.com/"
    client_id: "<auth0-application-client-id>"
    client_secret: "<auth0-application-client-secret>"
    redirect_url: "https://coremetry.example.com/api/auth/oidc/callback"
    scopes: ["openid", "email", "profile"]
    display_name: "Auth0"
    default_role: "viewer"`,
    },
    {
      key: 'oauth2-proxy',
      label: 'oauth2-proxy / IAP (trusted headers)',
      description: 'Banks running oauth2-proxy / Google IAP / Cloudflare Access in front of every internal app — Coremetry trusts the upstream identity headers without re-doing OIDC itself. trusted_proxies CIDR is REQUIRED so an attacker bypassing the proxy can\'t spoof X-Auth-Request-Email.',
      yaml:
`auth:
  trusted_header:
    enabled: true
    email_header: "X-Auth-Request-Email"
    user_header: "X-Auth-Request-User"
    groups_header: "X-Auth-Request-Groups"
    auto_provision: true        # first-sight email lands as DefaultRole
    default_role: "viewer"
    trusted_proxies:            # ← REQUIRED — your oauth2-proxy node CIDRs
      - "10.0.0.0/8"
      - "172.16.0.0/12"`,
    },
  ];
  const [activeKey, setActiveKey] = useState(presets[0].key);
  const active = presets.find(p => p.key === activeKey) ?? presets[0];
  const [copied, setCopied] = useState(false);
  // v0.8.550 — was `navigator.clipboard.writeText(...)` with a .catch().
  // The catch never fired on the case it described: with no secure context
  // `navigator.clipboard` is undefined, so reading `.writeText` throws
  // SYNCHRONOUSLY and no promise is ever created for .catch to attach to.
  // The shared helper handles the missing API, a rejection, AND supplies
  // the textarea fallback this surface never had — the operator can now
  // actually copy the YAML on a plain-HTTP install instead of falling back
  // to selecting it by hand.
  const copy = async () => {
    if (await copyToClipboard(active.yaml)) {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    }
  };
  return (
    <div className="sso-presets__body">
      <p className="sso-note">
        Sağlayıcıya göre örnek değerler. OIDC alanlarını yukarıdaki forma girmeniz yeterli;
        YAML yalnız <code>config.yaml</code> / Helm ile kurmak isteyenler ve Settings'ten
        yönetilmeyen trusted-header kipi için.
      </p>
      <TabStrip ariaLabel="SSO sağlayıcı ön ayarları" value={activeKey} onChange={setActiveKey}
        tabs={presets.map(p => ({ key: p.key, label: p.label }))} />
      <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 10, lineHeight: 1.6 }}>
        {active.description}
      </div>
      <div style={{ position: 'relative' }}>
        {/* v0.10.928 — <pre> üstünde yüzer: dolgusuz secondary'de kod satırı
            etiketin altından akmasın diye opak (is-overlay). */}
        <Button variant="secondary" size="sm" onClick={copy} className="is-overlay"
          style={{ position: 'absolute', top: 8, right: 8 }}>
          {copied ? '✓ copied' : 'Copy'}
        </Button>
        <pre style={{
          margin: 0, padding: 14, background: 'var(--bg)',
          border: '1px solid var(--border)', borderRadius: 6,
          lineHeight: 1.6, overflowX: 'auto',
        }}>
          <code>{active.yaml}</code>
        </pre>
      </div>
      <div style={{
        marginTop: 14, padding: '10px 12px', borderRadius: 6,
        background: 'var(--bg2)', border: '1px solid var(--border)',
        fontSize: 12, color: 'var(--text2)', lineHeight: 1.6,
      }}>
        <b>Notlar:</b>
        <ul style={{ paddingLeft: 18, margin: '6px 0 0' }}>
          <li>Settings'te kaydedilen OIDC ayarı <code>config.yaml</code>'daki <code>auth.oidc</code>'nin önüne geçer; config.yaml'daki değişiklik yalnız Settings'te kayıt yokken ve restart'la uygulanır.</li>
          <li>Local username/password login stays available alongside OIDC so admins always have a fallback.</li>
          <li>Trusted-header mode <b>requires</b> <code>trusted_proxies</code> — empty list = boot refused. Source-IP gate prevents header spoofing from any caller outside the proxy mesh.</li>
          <li>First-sight OIDC / trusted-header users land with <code>default_role</code> (viewer). Admins promote via <code>/users</code>.</li>
        </ul>
      </div>
    </div>
  );
}
