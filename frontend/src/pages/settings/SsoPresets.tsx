import { useState } from 'react';
import { ChevronRight } from 'lucide-react';
import { Button } from '@/components/shadcn/button';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/shadcn/tabs';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/shadcn/collapsible';
import { copyToClipboard } from '@/lib/clipboard';

// SsoPresets — sağlayıcı başına örnek ayar (OIDC + trusted-header proxy
// kipi). v0.10.1067'e dek SSO sekmesinin TEK içeriğiydi (yapılandırma
// yalnız config.yaml / env'den); artık OIDC Settings formundan yönetiliyor
// (SsoTab.tsx) ve bu blok formun altında, kapalı gelen bir başvuru:
// issuer URL kalıbı, önerilen kapsamlar, sağlayıcıya özgü notlar (Azure
// tenant yer tutucusu, Keycloak realm parçası, oauth2-proxy CIDR'ı).
// Trusted-header kipi hâlâ yalnız config.yaml'dan kurulur.
// Faz 2 dilim 1 R10 — DisclosureButton → shadcn collapsible (tetikleyici
// aynı `section` anatomisi: tam genişlik, 14px iç boşluk, açıkken alt
// ayırıcı ARIA/state'ten; ▸/▾ glifi → lucide chevron, D12), TabStrip →
// shadcn tabs (`line`). Kapalı gelir; gövde yalnız açıkken bağlanır.
export function SsoPresets() {
  const [open, setOpen] = useState(false);
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="mt-6 rounded-lg border border-(--border)">
      <CollapsibleTrigger className="group/dsc flex w-full cursor-pointer items-center gap-3 border-b border-transparent bg-transparent p-3.5 text-left text-[length:var(--fs-sm)] font-semibold text-(--text2) transition-colors hover:text-(--text) data-[state=open]:border-(--divider)">
        <ChevronRight aria-hidden="true" className="size-3.5 shrink-0 transition-transform group-data-[state=open]/dsc:rotate-90" />
        Örnek IdP ayarları
      </CollapsibleTrigger>
      <CollapsibleContent>
        <SsoPresetsBody />
      </CollapsibleContent>
    </Collapsible>
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
    <div className="px-4 py-3">
      <p className="sso-note">
        Sağlayıcıya göre örnek değerler. OIDC alanlarını yukarıdaki forma girmeniz yeterli;
        YAML yalnız <code>config.yaml</code> / Helm ile kurmak isteyenler ve Settings'ten
        yönetilmeyen trusted-header kipi için.
      </p>
      <Tabs value={activeKey} onValueChange={setActiveKey}>
        <TabsList variant="line" aria-label="SSO sağlayıcı ön ayarları" className="mb-2.5 max-w-full justify-start overflow-x-auto">
          {presets.map(p => <TabsTrigger key={p.key} value={p.key} className="flex-none">{p.label}</TabsTrigger>)}
        </TabsList>
        {presets.map(p => (
          <TabsContent key={p.key} value={p.key}>
            <div className="mb-2.5 text-[length:var(--fs-sm)] leading-relaxed text-(--text2)">
              {p.description}
            </div>
            <div className="relative">
              {/* v0.10.928 — <pre> üstünde yüzer: secondary dolgulu (opak), kod
                  satırı etiketin altından akmaz. */}
              <Button variant="secondary" size="sm" onClick={() => { void copy(); }} className="absolute top-2 right-2">
                {copied ? '✓ copied' : 'Copy'}
              </Button>
              <pre className="m-0 overflow-x-auto rounded-md border border-(--border) bg-(--bg) p-3.5 leading-relaxed">
                <code>{p.yaml}</code>
              </pre>
            </div>
          </TabsContent>
        ))}
      </Tabs>
      <div className="mt-3.5 rounded-md border border-(--border) bg-(--bg2) px-3 py-2.5 text-[length:var(--fs-sm)] leading-relaxed text-(--text2)">
        <b>Notlar:</b>
        <ul className="mt-1.5 mb-0 pl-[18px]">
          <li>Settings'te kaydedilen OIDC ayarı <code>config.yaml</code>'daki <code>auth.oidc</code>'nin önüne geçer; config.yaml'daki değişiklik yalnız Settings'te kayıt yokken ve restart'la uygulanır.</li>
          <li>Local username/password login stays available alongside OIDC so admins always have a fallback.</li>
          <li>Trusted-header mode <b>requires</b> <code>trusted_proxies</code> — empty list = boot refused. Source-IP gate prevents header spoofing from any caller outside the proxy mesh.</li>
          <li>First-sight OIDC / trusted-header users land with <code>default_role</code> (viewer). Admins promote via <code>/users</code>.</li>
        </ul>
      </div>
    </div>
  );
}
