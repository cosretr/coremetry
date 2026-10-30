// AiChatRetentionPanel.tsx — v0.10.561 (CoSRE Faz 5c). Sohbet arşivi
// (saved_views page='ai-chat') saklama süresi; AiBudgetPanel kalıbı: kendi
// GET/PUT'u, kutu boş = varsayılan 90, 0 = süpürme kapalı. Worker saatte bir
// süpürür (leader kilidi), silinen sayı sunucu logunda.
import { useEffect, useState, type FormEvent } from 'react';
import { Button } from '@/components/shadcn/button';
import { Field } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import { FlashBox, SectionTitle, humanize } from './shared';
import { retentionSummaryTR, retentionToForm, retentionToWire } from './aiChatRetention';


// Kart (D3: bg2 + border + 8px köşe + 16px dolgu, aynı ölçüler; yalnız token utility).
const CARD = 'mt-[18px] max-w-[640px] rounded-lg border border-(--border) bg-(--bg2) p-4';
const INTRO = 'mb-2.5 text-[length:var(--fs-sm)] text-(--text2)';

export function AiChatRetentionPanel() {
  const [form, setForm] = useState('');
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    let cancelled = false;
    api.aiChatRetention()
      .then(c => { if (!cancelled) { setForm(retentionToForm(c)); setLoaded(true); } })
      .catch(e => { if (!cancelled) { setFlash({ kind: 'err', text: humanize(e) }); setLoaded(true); } });
    return () => { cancelled = true; };
  }, []);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const wire = retentionToWire(form);
    if (typeof wire === 'string') { setFlash({ kind: 'err', text: wire }); return; }
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putAIChatRetention(wire);
      setForm(retentionToForm(saved));
      setFlash({ kind: 'ok', text: retentionSummaryTR(saved) });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={save} className={CARD}>
      <SectionTitle>Sohbet arşivi</SectionTitle>
      <div className={INTRO}>
        CoSRE sohbetleri son yazımlarından bu kadar gün sonra silinir; boş = 90 (varsayılan), 0 = süpürme kapalı. Silme geri alınamaz.
      </div>
      <Field label="Saklama (gün)" small hint="0 = kapalı · en fazla 3650"
        inputMode="numeric" value={form} onChange={e => setForm(e.target.value)} disabled={!loaded || busy} placeholder="90" />
      <div className="mt-3">
        <ActionRow confirm={<Button type="submit" loading={busy} disabled={!loaded}>Kaydet</Button>} />
      </div>
      {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
    </form>
  );
}
