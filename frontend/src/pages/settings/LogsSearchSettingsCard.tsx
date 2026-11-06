// LogsSearchSettingsCard — v0.10.1831 (Log Search Faz 1 dilim 1.8).
//
// Settings › Logs arka ucu (ElasticTab) altında "Log arama" kartı: /logs
// örnek boyunun ÜST SINIRI (system_settings `logs_search.maxSample`, 10 …
// 5000, varsayılan 2000). İki arka uç için de geçerli (CH + ES), bu yüzden
// ES alanlarından ayrı kart. Settings yalnız admin'e açık (Settings.tsx kapısı);
// PUT sunucuda da admin-only + audit `logs_search.settings`. Diğer roller
// etkin sınırı /logs Görünüm ▾ › Örnek boyutu etiketinde salt-okunur görür.
import { useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import type { LogsSearchSettings } from '@/lib/types';
import { useSettingsLoad, SettingsLoadError } from './shared';

const CARD = 'mt-[18px] grid gap-3 rounded-lg border bg-(--bg2) p-4';
const NOTE = 'mt-1.5 text-[length:var(--fs-xs)] text-(--text3)';

export function LogsSearchSettingsCard() {
  const [snap, setSnap] = useState<LogsSearchSettings | null>(null);
  const [maxSample, setMaxSample] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const { loaded, error, retry } = useSettingsLoad(() => api.getLogsSearchSettings(), s => {
    setSnap(s);
    setMaxSample(String(s.maxSample));
  });

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      const n = Number(maxSample);
      const s = await api.putLogsSearchSettings({ maxSample: Number.isFinite(n) ? Math.round(n) : 0 });
      setSnap(s); setMaxSample(String(s.maxSample));
      setMsg({ ok: true, text: 'Kaydedildi — /logs örnek boyu üst sınırı güncellendi.' });
    } catch (err) {
      setMsg({ ok: false, text: err instanceof Error ? err.message : String(err) });
    } finally {
      setBusy(false);
    }
  };

  if (error) return <SettingsLoadError error={error} onRetry={retry} />;
  if (!loaded || !snap) return <div className={CARD}><Spinner /></div>;
  return (
    <form onSubmit={save} className={CARD} aria-label="Log arama">
      <h3 className="m-0 text-[length:var(--fs-md)] font-semibold">Log arama</h3>
      <Field label={`Örnek boyutu üst sınırı (${snap.min} – ${snap.hardCap}; varsayılan ${snap.defaults.maxSample})`}
        type="number" inputMode="numeric" min={snap.min} max={snap.hardCap}
        value={maxSample} onChange={e => setMaxSample(e.target.value)} />
      <p className={NOTE}>
        /logs listesi her istekte {snap.sampleDefault} satırlık örnek çeker (Görünüm ▾ › Örnek boyutu ile
        {` ${snap.min}`} ile bu sınır arasında değişir). Tavan {snap.hardCap}: Elasticsearch
        {' '}<code>index.max_result_window</code> ve yanıt boyu. ClickHouse ve Elasticsearch için aynı.
      </p>
      <ActionRow confirm={<Button type="submit" loading={busy}>Kaydet</Button>} />
      {msg && <p role={msg.ok ? 'status' : 'alert'} className={msg.ok ? NOTE : 'text-[length:var(--fs-sm)] text-status-critical'}>{msg.text}</p>}
    </form>
  );
}
