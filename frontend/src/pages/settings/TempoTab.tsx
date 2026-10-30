import { useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { useConfirm } from '@/components/coremetry/Confirm';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { useSettingsLoad, SettingsLoadError, ConfigStatusBanner } from './shared';
import type { TempoAuthType } from '@/lib/types';

// Faz 2 dilim 1 R8 — inline stil yerine Tailwind utility (token değişkenleri).
const H2 = 'mb-1.5 text-[length:var(--fs-lg)] font-semibold';
const LEAD = 'mb-4 text-[length:var(--fs-md)] text-(--text2)';
const CARD = 'mt-[18px] flex flex-col gap-3 rounded-lg border border-(--border) bg-(--bg2) p-4';
const CODE = 'mx-1 rounded-sm bg-(--bg0) px-[5px] py-px font-mono text-[length:var(--fs-sm)]';
const ASIDE = 'ml-1.5 text-[length:var(--fs-xs)] italic text-(--text3)';

// TempoTab — external Grafana Tempo backend (v0.5.208). When
// configured, /api/traces/{id} falls back to Tempo on a CH miss,
// so operators running Coremetry at low sampling + Tempo at 100%
// retention can still resolve long-tail trace IDs in the same
// /trace URL the rest of the UI links to. Admin-only — the saved
// token reads every trace in the operator's Tempo cluster.
export function TempoTab() {
  const confirm = useConfirm();
  const [enabled, setEnabled] = useState(false);
  const [baseUrl, setBaseUrl] = useState('');
  const [authType, setAuthType] = useState<TempoAuthType>('none');
  const [username, setUsername] = useState('');
  const [orgId, setOrgId] = useState('');
  const [token, setToken] = useState('');
  const [hasToken, setHasToken] = useState(false);
  // v0.10.271 — token referansı (Influx sözleşmesi): görünür, secret değil.
  const [tokenRef, setTokenRef] = useState('');
  const [tokenResolved, setTokenResolved] = useState(false);
  const [tokenError, setTokenError] = useState('');
  const [insecureSkipVerify, setInsecureSkipVerify] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.getTempoSettings(),
    s => {
      setEnabled(s.enabled);
      setBaseUrl(s.baseUrl || '');
      setAuthType((s.authType || 'none') as TempoAuthType);
      setUsername(s.username || '');
      setOrgId(s.orgId || '');
      setHasToken(s.hasToken);
      setTokenRef(s.tokenRef ?? '');
      setTokenResolved(!!s.tokenResolved);
      setTokenError(s.tokenError ?? '');
      setInsecureSkipVerify(!!s.insecureSkipVerify);
    },
  );

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      const next = await api.putTempoSettings({
        enabled, baseUrl, authType,
        token, // empty preserved on the server side
        username, orgId, insecureSkipVerify,
        tokenRef: tokenRef.trim() || undefined,
      });
      setHasToken(next.hasToken);
      setTokenResolved(!!next.tokenResolved);
      setTokenError(next.tokenError ?? '');
      setToken('');
      setMsg({ kind: 'ok',
        text: next.enabled
          ? 'Saved — Tempo fallback live for trace-by-id lookups.'
          : 'Saved — Tempo disabled.' });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Save failed' });
    } finally {
      setBusy(false);
    }
  };

  const clearToken = async () => {
    if (!await confirm({
      title: 'Kayıtlı Tempo token’ı silinsin mi?',
      body: <>Token kaldırılacak; Tempo üzerinden trace aramaları yeni bir
        token girilene kadar <b>401</b> ile başarısız olacak.</>,
      confirmLabel: 'Token’ı sil',
      danger: true,
    })) return;
    setBusy(true); setMsg(null);
    try {
      // Server contract: empty token = preserve. To explicitly
      // CLEAR we send a sentinel and the server compares cur vs
      // payload. Without a sentinel, the simplest path is to
      // flip authType to "none" — drops the Authorization
      // header even if the token is still stored. That's enough
      // for "stop using my creds".
      const next = await api.putTempoSettings({
        enabled, baseUrl, authType: 'none',
        username, orgId, insecureSkipVerify,
      });
      setAuthType('none');
      setHasToken(next.hasToken);
      setMsg({ kind: 'ok', text: 'Auth disabled — lookups now go anonymously.' });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Clear failed' });
    } finally {
      setBusy(false);
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  const ready = enabled && baseUrl.trim().length > 0;

  return (
    <div className="max-w-[640px]">
      <h2 className={H2}>External Tempo backend</h2>
      <p className={LEAD}>
        Use case: Coremetry at low sampling (e.g. 5%) for fast hot-path
        observability + an external Grafana Tempo cluster at 100% retention
        for forensics. When a trace ID isn't in Coremetry's store,
        <code className={CODE}>/trace?id=…</code>
        silently falls back to Tempo. The trace renders in the same
        waterfall with a small banner so it's clear where the data came from.
        Trace-by-id only — search / aggregations / topology still hit Coremetry.
      </p>

      {/* v0.10.929 (K5) — kurulu/kurulmamış bir ayar durumu: nötr bant (eskiden yeşil/amber). */}
      <ConfigStatusBanner label={ready ? 'ENABLED' : 'NOT CONFIGURED'}>
        {ready
          ? `Pointing at ${baseUrl}${orgId ? ` (orgId=${orgId})` : ''}.`
          : 'Disabled — CH misses return empty without trying Tempo.'}
      </ConfigStatusBanner>

      {/* Faz 2 dilim 1 R8: alanlar coremetry/Field ailesi (D1), kart aynı (D3). */}
      <form onSubmit={save} className={CARD}>
        <CheckboxField label="Enable Tempo fallback" checked={enabled} onCheckedChange={setEnabled} />

        <Field label="Base URL" value={baseUrl}
          onChange={e => setBaseUrl(e.target.value)}
          placeholder="https://tempo.example.com  ·  Grafana Cloud: https://tempo-prod-XX.grafana.net/tempo"
          hint={<>
            Trailing slash optional. We call <code>{`{baseUrl}/api/traces/{id}`}</code> with
            <code className="ml-1">Accept: application/json</code>.
          </>} />

        <SelectField label="Auth" value={authType}
          onChange={e => setAuthType(e.target.value as TempoAuthType)}>
          <option value="none">None (open Tempo behind VPN / mTLS)</option>
          <option value="bearer">Bearer token (Grafana Cloud API key)</option>
          <option value="basic">Basic auth (self-hosted + nginx htpasswd)</option>
        </SelectField>

        {authType === 'basic' && (
          <Field label="Username" value={username}
            onChange={e => setUsername(e.target.value)} />
        )}

        {(authType === 'bearer' || authType === 'basic') && (
          <Field label={authType === 'bearer' ? 'Bearer token' : 'Password'} type="password" value={token}
            labelAside={hasToken ? <span className="text-[length:var(--fs-sm)] text-(--text3)">· stored</span> : undefined}
            onChange={e => setToken(e.target.value)}
            placeholder={hasToken ? '(leave empty to keep stored value)' : 'paste token…'} />
        )}

        {(authType === 'bearer' || authType === 'basic') && (
          /* v0.10.271 — InfluxTab deseni: referans doluysa saklı token yerine kullanılır. */
          <Field label="…ya da token referansı" value={tokenRef}
            onChange={e => setTokenRef(e.target.value)}
            placeholder="env:COREMETRY_TEMPO_TOKEN  ·  file:/var/run/secrets/tempo/token"
            autoComplete="off"
            error={tokenError || undefined}
            hint={tokenResolved
              ? 'Çözüldü ✓ — saklı token yerine bu kullanılıyor'
              : 'Doluysa saklı token yerine bu kullanılır. env: Helm extraEnv + existingSecret (pod restart); file: mount edilmiş Secret (≤30 s). Kaydedince çözüm durumu burada görünür.'} />
        )}

        <Field label="X-Scope-OrgID (multi-tenant Tempo / Grafana Cloud)" value={orgId}
          onChange={e => setOrgId(e.target.value)}
          placeholder="leave empty for single-tenant" />

        <CheckboxField checked={insecureSkipVerify} onCheckedChange={setInsecureSkipVerify}
          label={<>Skip TLS verification<span className={ASIDE}>(self-signed certs / POC only)</span></>} />

        {msg && (
          <div className={cn('text-[length:var(--fs-sm)]', msg.kind === 'ok' ? 'text-(--ok)' : 'text-(--err)')}>
            {msg.text}
          </div>
        )}

        {/* D4: eylem satırı sağa yaslı, birincil en sağda, yıkıcı en solda. */}
        <ActionRow
          destructive={hasToken && authType !== 'none' && (
            <Button variant="ghost-danger" disabled={busy} onClick={() => void clearToken()}>
              Disable auth
            </Button>
          )}
          confirm={<Button type="submit" loading={busy}>Save</Button>} />
      </form>
    </div>
  );
}
