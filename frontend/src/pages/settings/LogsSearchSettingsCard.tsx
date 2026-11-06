// LogsSearchSettingsCard — v0.10.1831 (Log Search Faz 1 dilim 1.8).
//
// Settings › Logs arka ucu (ElasticTab) altında "Log arama" kartı: /logs
// örnek boyunun ÜST SINIRI (system_settings `logs_search.maxSample`, 10 …
// 5000, varsayılan 2000). İki arka uç için de geçerli (CH + ES), bu yüzden
// ES alanlarından ayrı kart. Settings yalnız admin'e açık (Settings.tsx kapısı);
// PUT sunucuda da admin-only + audit `logs_search.settings`. Diğer roller
// etkin sınırı /logs Görünüm ▾ › Örnek boyutu etiketinde salt-okunur görür.
//
// v0.10.1853 (Log Search Faz 2 dilim 2.2) — "İş alanları": /logs alan kenar
// çubuğunun en üstündeki bölüm (etiket → ES alan adayları, ilk eşleşen aday).
// PUT TAM blobu yazar: maxSample + businessFields birlikte gider (tam-satır).
//
// v0.10.1896 (Log Search Faz 4 dilim 4.2) — "Desen eğilimi ve ilk görülme"
// anahtarı (`logs_search.patternInsights`, varsayılan KAPALI): Desenler
// sekmesine Elasticsearch'e iki ek sorgu ekler (1877 olayı: ölçülmeden açılmaz).
import { useId, useState, type FormEvent } from 'react';
import { Checkbox } from '@/components/shadcn/checkbox';
import { Label } from '@/components/shadcn/label';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import type { LogsSearchSettings, LogsBusinessField } from '@/lib/types';
import { useSettingsLoad, SettingsLoadError } from './shared';

const CARD = 'mt-[18px] grid gap-3 rounded-lg border bg-(--bg2) p-4';
const NOTE = 'mt-1.5 text-[length:var(--fs-xs)] text-(--text3)';
const BIZ_MAX = 20;

type BizDraft = { label: string; fields: string };
const toDraft = (b: LogsBusinessField[] | undefined): BizDraft[] => (b ?? []).map(x => ({ label: x.label, fields: x.fields.join(', ') }));
// fromDraft — tamamen boş satır düşer; yarım satır sunucuya gider (400 + gerekçe).
function bizFromDraft(rows: BizDraft[]): LogsBusinessField[] {
  return rows
    .map(r => ({ label: r.label.trim(), fields: r.fields.split(',').map(f => f.trim()).filter(Boolean) }))
    .filter(r => r.label !== '' || r.fields.length > 0);
}

export function LogsSearchSettingsCard() {
  const [snap, setSnap] = useState<LogsSearchSettings | null>(null);
  const [maxSample, setMaxSample] = useState('');
  const [biz, setBiz] = useState<BizDraft[]>([]);
  const [insights, setInsights] = useState(false);
  const insightsId = useId();
  const setBizAt = (i: number, patch: Partial<BizDraft>) => setBiz(rows => rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const { loaded, error, retry } = useSettingsLoad(() => api.getLogsSearchSettings(), s => {
    setSnap(s);
    setMaxSample(String(s.maxSample));
    setBiz(toDraft(s.businessFields));
    setInsights(!!s.patternInsights);
  });

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      const n = Number(maxSample);
      const s = await api.putLogsSearchSettings({ maxSample: Number.isFinite(n) ? Math.round(n) : 0, businessFields: bizFromDraft(biz), patternInsights: insights });
      setSnap(s); setMaxSample(String(s.maxSample)); setBiz(toDraft(s.businessFields)); setInsights(!!s.patternInsights);
      setMsg({ ok: true, text: 'Kaydedildi — /logs örnek boyu üst sınırı, iş alanları ve desen ayarı güncellendi.' });
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
      <fieldset className="m-0 grid gap-2 border-0 p-0" aria-label="İş alanları">
        <legend className="mb-1 p-0 text-[length:var(--fs-sm)] font-semibold">İş alanları</legend>
        <p className="m-0 text-[length:var(--fs-xs)] text-(--text3)">
          /logs alan kenar çubuğunun en üstünde sabit durur. Etiket başına Elasticsearch alan adayları (virgülle);
          eşlemede bulunan ilk aday kullanılır. Boşken bölüm gizli. Yalnız Elasticsearch log arka ucunda.
        </p>
        {biz.map((r, i) => (
          <div key={i} className="grid grid-cols-[180px_minmax(0,1fr)_auto] items-end gap-2" data-testid="biz-row">
            <Field label={`Etiket ${i + 1}`} value={r.label} maxLength={64}
              onChange={e => setBizAt(i, { label: e.target.value })} />
            <Field label={`Alan adayları ${i + 1}`} value={r.fields} controlClassName="font-mono"
              onChange={e => setBizAt(i, { fields: e.target.value })} />
            <Button type="button" variant="ghost-danger" size="sm" aria-label={`İş alanını kaldır: ${r.label || i + 1}`}
              onClick={() => setBiz(rows => rows.filter((_, j) => j !== i))}>Kaldır</Button>
          </div>
        ))}
        <div>
          <Button type="button" variant="outline" size="sm" disabled={biz.length >= BIZ_MAX}
            title={biz.length >= BIZ_MAX ? `En çok ${BIZ_MAX} iş alanı` : undefined}
            onClick={() => setBiz(rows => [...rows, { label: '', fields: '' }])}>+ İş alanı ekle</Button>
        </div>
      </fieldset>
      <div className="grid gap-1" data-testid="pattern-insights-setting">
        <div className="flex items-center gap-2">
          <Checkbox id={insightsId} checked={insights} onCheckedChange={v => setInsights(v === true)}
            aria-describedby={`${insightsId}-note`} />
          <Label htmlFor={insightsId}>Desenler sekmesinde eğilim ve "ilk kez görüldü"</Label>
        </div>
        <p id={`${insightsId}-note`} className="m-0 text-[length:var(--fs-xs)] text-(--text3)">
          Desenler sekmesinde eğilim çizgisi ve "ilk kez görüldü" etiketi; Elasticsearch'e ek sorgu atar — önce test
          ortamında Admin → Elasticsearch sayacıyla ölçün. Kapalıyken bu sorgular hiç gitmez. Açıkken yalnız Desenler
          sekmesi açılınca, ilk 20 desen için iki toplu sorgu (her biri en çok 2 sn). Yalnız Elasticsearch log arka ucunda.
        </p>
      </div>
      <ActionRow confirm={<Button type="submit" loading={busy}>Kaydet</Button>} />
      {msg && <p role={msg.ok ? 'status' : 'alert'} className={msg.ok ? NOTE : 'text-[length:var(--fs-sm)] text-status-critical'}>{msg.text}</p>}
    </form>
  );
}
