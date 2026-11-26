import { useState } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { SelectField } from '@/components/coremetry/Field';
import { api } from '@/lib/api';
import type { ProblemGraphMergeSetting } from '@/lib/types';
import { tsCompact } from '@/lib/utils';
import { FlashBox, SettingsSection, humanize, useSettingsLoad, SettingsLoadError } from './shared';

// ── Servisler arası Problem birleştirmesi (v0.10.2473) ──────────────────────
// GET/PUT /api/admin/problem-graph-merge. Görünüm kipi: Problems listesi bağlı
// kümeyi tek satırda katlar ve manşete köken yazar. Bildirimi DEĞİŞTİRMEZ
// (bildirim tutması ayrı kod sabitinde, kapalı) — bu yüzden bildirim
// politikasının yanında, ama ayrı kayıtla. Admin ekranı: yalnız Türkçe.

export const GRAPH_MERGE_HINT: Record<ProblemGraphMergeSetting['mode'], string> = {
  on: 'Varsayılan. Bağlı Problem’ler listede tek satırda katlanır (etkilenen servis sayısı, açılınca üyeler); manşete “Köken: …” yazılır. Her üye kendi sayfasında açık kalır.',
  shadow: 'Liste katlanmaz; bağlar ve köken yalnız ölçülür (Problem detayında gölge bölümü, satırda gölge alt satırı).',
  off: 'Küme ve köken hesaplanmaz, hiçbir yüzeyde gösterilmez.',
};

export function ProblemGraphMergeSection() {
  const [cfg, setCfg] = useState<ProblemGraphMergeSetting | null>(null);
  const [mode, setMode] = useState<ProblemGraphMergeSetting['mode']>('on');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const { loaded, error, retry } = useSettingsLoad(() => api.problemGraphMerge(), c => { setCfg(c); setMode(c.mode); });

  if (error) return <SettingsLoadError error={error} onRetry={retry} />;
  if (!loaded || !cfg) return <Spinner />;

  const save = async () => {
    setBusy(true); setMsg(null);
    try {
      const saved = await api.putProblemGraphMerge(mode);
      setCfg(saved); setMode(saved.mode);
      setMsg({ kind: 'ok', text: `Kaydedildi — kip: ${saved.mode}. Tüm pod'lara ≤ 30 sn içinde yayılır; bildirimler değişmez.` });
    } catch (e) {
      setMsg({ kind: 'err', text: humanize(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsSection title="Servisler arası Problem birleştirmesi"
      description="Entity grafında bağlı Problem’ler (çağrı zinciri, ortak bağımlılık) listede tek satırda toplanır ve kökeni seçilir. Yalnız görünüm: hiçbir Problem kapatılmaz, bildirim kararları değişmez.">
      <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-x-4 gap-y-3">
        <SelectField label="Kip" value={mode} hint={GRAPH_MERGE_HINT[mode]} data-testid="graph-merge-mode"
          onChange={e => setMode(e.target.value as ProblemGraphMergeSetting['mode'])}>
          <option value="on">on — listede katla (varsayılan)</option>
          <option value="shadow">shadow — yalnız ölç</option>
          <option value="off">off — hesaplama</option>
        </SelectField>
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-2.5">
        <Button onClick={() => { void save(); }} loading={busy} disabled={mode === cfg.mode}>Kaydet</Button>
        {cfg.updatedBy && <span className="field-hint">Son değişiklik: {cfg.updatedBy}{cfg.updatedAt ? ` · ${tsCompact(cfg.updatedAt)}` : ''}</span>}
      </div>
      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
    </SettingsSection>
  );
}
