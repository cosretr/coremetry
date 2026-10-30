import { useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { useSettingsLoad, SettingsLoadError, ConfigStatusBanner } from './shared';
import type { ESLogstoreInput, ESLogstoreSnapshot } from '@/lib/types';

// Faz 2 dilim 1 R8 — inline stil yerine Tailwind utility (token değişkenleri).
const H2 = 'mb-1.5 text-[length:var(--fs-lg)] font-semibold';
const LEAD = 'mb-4 text-[length:var(--fs-md)] text-(--text2)';
const CARD = 'mt-[18px] grid gap-3 rounded-lg border border-(--border) bg-(--bg2) p-4';
const ASIDE = 'ml-1.5 text-[length:var(--fs-xs)] italic text-(--text3)';
const NOTE = 'mt-1.5 text-[length:var(--fs-xs)] text-(--text3)';

// ElasticTab — UI-managed logs read backend (v0.8.232,
// operator-requested: configure ES from the UI and SEE the error when
// it's wrong). Follows the TempoTab template. Save is apply-first on
// the server: a config that can't connect comes back with the real ES
// error and nothing changes; Test pings a candidate without touching
// the live backend. UI-saved config overrides env/YAML (which stays
// the bootstrap default) and swaps the backend live — no restart.
export function ElasticTab() {
  const [snap, setSnap] = useState<ESLogstoreSnapshot | null>(null);

  const [backend, setBackend] = useState<'clickhouse' | 'elasticsearch'>('clickhouse');
  const [addresses, setAddresses] = useState('');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [insecure, setInsecure] = useState(false);
  const [index, setIndex] = useState('');
  const [indexTemplate, setIndexTemplate] = useState('');
  const [fTimestamp, setFTimestamp] = useState('');
  const [fTraceId, setFTraceId] = useState('');
  const [fSpanId, setFSpanId] = useState('');
  const [fService, setFService] = useState('');
  const [fBody, setFBody] = useState('');
  const [fSevTx, setFSevTx] = useState('');
  const [fSevNo, setFSevNo] = useState('');
  // v0.8.400 — deployment-environment field for the ?env= filter;
  // empty = the backend self-discovers via field_caps.
  const [fEnv, setFEnv] = useState('');
  // v0.10.948 (CoSRE Faz B) — çapraz-kaynak eşleme alanları (Faz A backend'i:
  // logstore_es_handlers.go esFieldMapInput). Boş = aday listeleri + field_caps
  // keşfi; CoSRE'nin log araçları hangi alanı (yapılandırılmış / keşfedilmiş)
  // kullandığını sonucunda raporlar.
  const [fCluster, setFCluster] = useState('');
  const [fNamespace, setFNamespace] = useState('');
  const [fPod, setFPod] = useState('');
  const [fVersion, setFVersion] = useState('');
  // v0.10.1473 — takım etiketi alan(lar)ı: CoSRE takım cevabının son-log
  // kontrolü. Boş = kapalı (ES'e hiç gidilmez).
  const [fTeam, setFTeam] = useState('');

  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.getLogstoreSettings(),
    s => {
      setSnap(s);
      setBackend(s.backend);
      setAddresses((s.addresses || []).join(', '));
      setUsername(s.username || '');
      setInsecure(!!s.insecureSkipVerify);
      setIndex(s.index || '');
      setIndexTemplate(s.indexTemplate || '');
      setFTimestamp(s.fields?.timestamp || '');
      setFTraceId(s.fields?.traceId || '');
      setFSpanId(s.fields?.spanId || '');
      setFService(s.fields?.service || '');
      setFBody(s.fields?.body || '');
      setFSevTx(s.fields?.severityTx || '');
      setFSevNo(s.fields?.severityNo || '');
      setFEnv(s.fields?.env || '');
      setFCluster(s.fields?.cluster || '');
      setFNamespace(s.fields?.namespace || '');
      setFPod(s.fields?.pod || '');
      setFVersion(s.fields?.version || '');
      setFTeam(s.fields?.team || '');
    },
  );

  const buildInput = (): ESLogstoreInput => ({
    backend,
    addresses: addresses.split(',').map(a => a.trim()).filter(Boolean),
    username, password, apiKey,
    insecureSkipVerify: insecure,
    index, indexTemplate,
    fields: {
      timestamp: fTimestamp, traceId: fTraceId, spanId: fSpanId,
      service: fService, body: fBody, severityTx: fSevTx, severityNo: fSevNo,
      env: fEnv,
      // v0.10.948 — dördü de HER kayıtta gider (form saklı değeri okudu): boş
      // dize "temizle → keşif", sunucunun işaretçi sözleşmesi. Anahtarı hiç
      // göndermemek (eski form) saklı değeri korur — bu form artık onu bilir.
      cluster: fCluster, namespace: fNamespace, pod: fPod, version: fVersion,
      team: fTeam, // v0.10.1473 — aynı işaretçi sözleşmesi ("" = kapat)
    },
  });

  const test = async () => {
    setBusy(true); setMsg(null);
    try {
      const r = await api.testLogstoreSettings(buildInput());
      setMsg(r.ok
        ? { kind: 'ok', text: backend === 'elasticsearch' ? 'Connected — cluster answered the ping.' : 'ClickHouse backend reachable.' }
        : { kind: 'err', text: r.error || 'Connection failed' });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Test failed' });
    } finally {
      setBusy(false);
    }
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      const next = await api.putLogstoreSettings(buildInput());
      setSnap(next);
      setPassword(''); setApiKey('');
      setMsg({ kind: 'ok',
        text: next.backend === 'elasticsearch'
          ? 'Saved — logs now read from Elasticsearch (applied live, all pods converge <30s).'
          : 'Saved — logs now read from ClickHouse.' });
    } catch (err) {
      // The server rejects an unconnectable config with the REAL ES
      // error (apply-first) — surface it verbatim.
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Save failed' });
    } finally {
      setBusy(false);
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  const es = backend === 'elasticsearch';
  const fieldRow = (label: string, val: string, set: (v: string) => void, ph: string) => (
    <Field label={label} value={val} onChange={e => set(e.target.value)} placeholder={ph} />
  );

  return (
    <div className="max-w-[640px]">
      <h2 className={H2}>Logs read backend</h2>
      <p className={LEAD}>
        Ingest always writes to ClickHouse; this selects where /logs
        <em> reads</em> from. Point it at an external Elasticsearch cluster to
        query the indices your existing pipeline ships to. Saved config
        overrides <code>COREMETRY_ES_*</code> env (env stays the bootstrap
        default) and applies live — no restart. Failed queries surface on
        Admin → Elasticsearch → Recent query errors.
      </p>

      {snap && (
        // v0.10.929 (K5) — seçili log backend'i bir ayar durumu: nötr bant (ES yeşil / CH amber değil).
        <ConfigStatusBanner label={snap.backend.toUpperCase()}
          aside={
            <span className="ml-auto text-[length:var(--fs-xs)] text-(--text3)">
              source: {snap.source === 'ui' ? 'UI override' : 'env / YAML'}
            </span>
          }>
          {snap.backend === 'elasticsearch'
            ? `Reading ${snap.index || 'app-*'} on ${snap.addresses.join(', ') || '—'}`
            : 'Reading the built-in ClickHouse logs table.'}
        </ConfigStatusBanner>
      )}

      {/* Faz 2 dilim 1 R8: elle yazılmış <label><div/><input/></label> alanları
          coremetry/Field ailesine (D1), kart aynı (D3). */}
      <form onSubmit={save} className={CARD}>
        <SelectField label="Backend" value={backend}
          onChange={e => setBackend(e.target.value as 'clickhouse' | 'elasticsearch')}>
          <option value="clickhouse">ClickHouse (built-in logs table)</option>
          <option value="elasticsearch">Elasticsearch (external cluster, read-only)</option>
        </SelectField>

        {es && (
          <>
            <Field label="Addresses" value={addresses} onChange={e => setAddresses(e.target.value)}
              placeholder="https://es-0:9200, https://es-1:9200" hint="Comma-separated." />

            <Field label="API key" type="password" value={apiKey} onChange={e => setApiKey(e.target.value)}
              labelAside={snap?.hasApiKey ? <span className="text-[length:var(--fs-sm)] text-(--text3)">· stored</span> : undefined}
              placeholder={snap?.hasApiKey ? '(leave empty to keep stored value)' : 'base64 id:api_key — takes precedence over basic auth'} />

            <div className="grid-2 gap-3">
              <Field label="Username (basic auth)" value={username} onChange={e => setUsername(e.target.value)}
                placeholder="unused when an API key is set" />
              <Field label="Password" type="password" value={password} onChange={e => setPassword(e.target.value)}
                labelAside={snap?.hasPassword ? <span className="text-[length:var(--fs-sm)] text-(--text3)">· stored</span> : undefined}
                placeholder={snap?.hasPassword ? '(keep stored)' : ''} />
            </div>

            <div className="grid-2 gap-3">
              <Field label="Index pattern" value={index} onChange={e => setIndex(e.target.value)}
                placeholder="app-*" />
              <Field label="Index template (per-service)" value={indexTemplate} onChange={e => setIndexTemplate(e.target.value)}
                placeholder="app-{service}.{namespace}"
                hint={<>
                  Service-filtered queries hit the resolved index instead of the
                  pattern. <code>{'{namespace}'}</code> comes from span resource
                  attributes; unresolved → <code>*</code>.
                </>} />
            </div>

            <details>
              <summary className="cursor-pointer text-[length:var(--fs-sm)] text-(--text2)">
                Document field map (leave empty for OTel/ECS defaults)
              </summary>
              <div className="grid-2 mt-2.5 gap-3">
                {fieldRow('Timestamp', fTimestamp, setFTimestamp, '@timestamp')}
                {fieldRow('Message body', fBody, setFBody, 'message')}
                {fieldRow('Trace ID', fTraceId, setFTraceId, 'trace.id')}
                {fieldRow('Span ID', fSpanId, setFSpanId, 'span.id')}
                {fieldRow('Service', fService, setFService, 'service.name')}
                {fieldRow('Severity (text)', fSevTx, setFSevTx, 'log.level')}
                {fieldRow('Severity (number)', fSevNo, setFSevNo, 'empty = skip')}
                {fieldRow('Environment', fEnv, setFEnv, 'empty = self-discover')}
                {fieldRow('Cluster', fCluster, setFCluster, 'empty = self-discover')}
                {fieldRow('Namespace', fNamespace, setFNamespace, 'empty = self-discover')}
                {fieldRow('Pod', fPod, setFPod, 'empty = self-discover')}
                {fieldRow('Version', fVersion, setFVersion, 'empty = self-discover')}
                {fieldRow('Team field', fTeam, setFTeam, 'empty = off')}
              </div>
              <div className={NOTE}>
                Environment backs the global env picker on /logs. Left empty,
                Coremetry discovers the field from the index mapping
                (deployment.environment[.name] and friends) and /logs shows an
                honest chip when none resolves.
              </div>
              <div className={NOTE}>
                Cluster / namespace / pod / version back CoSRE’s cross-source
                matching (trace ↔ logs filters). Left empty, Coremetry tries its
                candidate fields (k8s.cluster.name, kubernetes.namespace.name,
                kubernetes.pod_name, service.version and friends) via field_caps;
                a filter it cannot map is reported as partial, never silently
                dropped. Set one only when your pipeline uses another path.
              </div>
              <div className={NOTE}>
                Team field turns on CoSRE’s recent-log check in team answers
                (“how are my team’s services?”): the document path that carries
                the team label, e.g. a Kubernetes pod label such
                as <code>kubernetes.labels.team</code> (comma-separate up to 3
                paths). One bounded search per answer — last 15 min, error
                level, 3 s timeout, cached 60 s. Team values come from the
                service catalog, so no value mapping is needed. Empty = off.
              </div>
            </details>

            <CheckboxField checked={insecure} onCheckedChange={setInsecure}
              label={<>Skip TLS verification<span className={ASIDE}>(self-signed certs / POC only)</span></>} />
          </>
        )}

        {msg && (
          <div className={cn('whitespace-pre-wrap break-words text-[length:var(--fs-sm)]', msg.kind === 'ok' ? 'text-(--ok)' : 'text-(--err)')}>
            {msg.text}
          </div>
        )}

        {/* D4: eylem satırı sağa yaslı, birincil en sağda. */}
        <ActionRow
          secondary={<Button variant="secondary" loading={busy} onClick={test}>Test connection</Button>}
          confirm={<Button type="submit" loading={busy}>Save &amp; apply</Button>} />
      </form>
    </div>
  );
}
