import { useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import type { KibanaSettings } from '@/lib/types';
import { SettingsLoadError, useSettingsLoad, ConfigStatusBanner } from './shared';

// Faz 2 dilim 1 R8 — inline stil yerine Tailwind utility (token değişkenleri).
const H2 = 'mb-1.5 text-[length:var(--fs-lg)] font-semibold';
const LEAD = 'mb-4 text-[length:var(--fs-md)] text-(--text2)';
const CARD = 'mt-[18px] flex flex-col gap-3 rounded-lg border border-(--border) bg-(--bg2) p-4';

// KibanaTab — external Kibana deep-link config (v0.5.236).
// Operator pastes the base URL of their Kibana install; the
// Logs page then renders an "Open in Kibana Discover" link
// per row + a global one in the topbar. Empty / disabled =
// no link rendered.
//
// OpenShift's "Discover in Kibana" pattern: pass a KQL clause
// + time bounds via the _g / _a state params so the Kibana
// landing surface matches the row's context.
export function KibanaTab() {
  const [enabled, setEnabled] = useState(false);
  const [baseUrl, setBaseUrl] = useState('');
  const [dataView, setDataView] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.getKibanaSettings(),
    s => {
      setEnabled(!!s.enabled);
      setBaseUrl(s.baseUrl || '');
      setDataView(s.dataView || '');
    },
  );

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      const next: KibanaSettings = { enabled, baseUrl, dataView: dataView || undefined };
      const r = await api.putKibanaSettings(next);
      setMsg({ kind: 'ok', text: r.enabled
        ? 'Saved — Kibana link is live on /logs.'
        : 'Saved — Kibana link disabled.' });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Save failed' });
    } finally {
      setBusy(false);
    }
  };

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  const ready = enabled && baseUrl.trim() !== '';

  return (
    <div className="max-w-[640px]">
      <h2 className={H2}>Kibana deep-link</h2>
      <p className={LEAD}>
        Operators who use Kibana alongside Coremetry can jump out
        to Kibana Discover with the current Logs filter pre-applied
        — same pattern as OpenShift's "Discover in Kibana" affordance.
        Coremetry never proxies Kibana; only mints the deep-link.
      </p>

      {/* v0.10.929 (K5) — kurulu/kurulmamış bir ayar durumu: nötr bant (eskiden yeşil/amber). */}
      <ConfigStatusBanner label={ready ? 'ENABLED' : 'NOT CONFIGURED'}>
        {ready
          ? `Logs page will render a Kibana link pointing at ${baseUrl}.`
          : 'Disabled — no Kibana link rendered on the Logs page.'}
      </ConfigStatusBanner>

      {/* Faz 2 dilim 1 R8: SettingRow + ham input → coremetry/Field (D1), kart aynı (D3). */}
      <form onSubmit={save} className={CARD}>
        <CheckboxField label={'Show "Open in Kibana" link on Logs'} checked={enabled} onCheckedChange={setEnabled} />

        <Field label="Kibana base URL" value={baseUrl}
          onChange={e => setBaseUrl(e.target.value)}
          placeholder="https://kibana.example.com  (no trailing /app/...)"
          hint={<>
            Just the host (or host + path prefix if Kibana lives under one,
            e.g. <code>https://openshift-console.example.com/monitoring/kibana</code>).
            Coremetry appends <code>/app/discover#/?…</code>.
          </>} />

        <div>
          <Field label={<>Data view ID <span className="text-(--text3)">(optional)</span></>} value={dataView}
            onChange={e => setDataView(e.target.value)}
            placeholder="e.g. 8b9b3260-019d-11ec-a24c-8d8ee9abad6f"
            hint={<>
              The Kibana data view <b>ID (UUID)</b> — find it in Kibana →
              Stack Management → Data Views (the id in the URL), or in the
              fallback toast Kibana shows. An index <i>pattern</i> like{' '}
              <code>app-*</code> is NOT accepted by Kibana 8 here: Discover
              then warns &quot;not a configured data view ID&quot; on every
              open and falls back to the default view. Empty = Kibana picks
              the default (fine for single-pattern installs).
            </>} />
          {/* v0.9.1093 (operatör foto): desen girilmişse toast'ın SEBEBİNİ
              kaynağında söyle — kaydetmeyi engelleme (Kibana 7 kurulumları
              deseni tolere edebiliyor), yalnız uyar. */}
          {dataView.includes('*') && (
            <div className="mt-1.5 text-[length:var(--fs-xs)] text-(--warn)">
              ⚠ Bu bir index deseni görünüyor — Kibana 8 burada data view
              ID (UUID) bekler; Discover her açılışta &quot;not a configured
              data view ID&quot; uyarısı basıp varsayılana düşer.
            </div>
          )}
        </div>

        {msg && (
          <div className={cn('text-[length:var(--fs-sm)]', msg.kind === 'ok' ? 'text-(--ok)' : 'text-(--err)')}>
            {msg.text}
          </div>
        )}

        {/* D4: eylem satırı sağa yaslı. */}
        <ActionRow confirm={<Button type="submit" loading={busy}>Save</Button>} />
      </form>
    </div>
  );
}
