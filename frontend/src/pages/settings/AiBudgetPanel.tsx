// AiBudgetPanel — v0.10.411 (CoSRE denetimi E8): AI maliyet/gecikme
// bütçesi. Üç tavan (günlük token, günlük $, p95 ms); 0/boş = tavan yok.
// Rozet /ai sayfasında (son 24 saat). Kutular STRING state (aiTuning
// deseni): çeviri saf modülde (aiBudget.ts), burada yalnız form.
import { useEffect, useState, type FormEvent } from 'react';
import { Button } from '@/components/shadcn/button';
import { Field } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import { budgetToForm, budgetToWire, type BudgetForm } from './aiBudget';
import { FlashBox, SectionTitle, humanize } from './shared';


// Kart (D3: bg2 + border + 8px köşe + 16px dolgu, aynı ölçüler; yalnız token utility).
const CARD = 'mt-[18px] max-w-[640px] rounded-lg border border-(--border) bg-(--bg2) p-4';
const INTRO = 'mb-2.5 text-[length:var(--fs-sm)] text-(--text2)';

export function AiBudgetPanel() {
  const [form, setForm] = useState<BudgetForm>({ dailyTokens: '', dailyCostUsd: '', p95Ms: '' });
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    let cancelled = false;
    api.aiBudget()
      .then(st => { if (!cancelled) { setForm(budgetToForm(st.budget)); setLoaded(true); } })
      .catch(e => { if (!cancelled) { setFlash({ kind: 'err', text: humanize(e) }); setLoaded(true); } });
    return () => { cancelled = true; };
  }, []);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const wire = budgetToWire(form);
    if (typeof wire === 'string') { setFlash({ kind: 'err', text: wire }); return; }
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putAIBudget(wire);
      setForm(budgetToForm(saved));
      setFlash({ kind: 'ok', text: 'Bütçe kaydedildi — rozet /ai sayfasında (son 24 saat).' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const set = (k: keyof BudgetForm) => (e: { target: { value: string } }) => setForm(f => ({ ...f, [k]: e.target.value }));

  return (
    <form onSubmit={save} className={CARD}>
      <SectionTitle>Bütçe</SectionTitle>
      <div className={INTRO}>
        Son 24 saatin kullanımı bu tavanlarla kıyaslanır; boş = tavan yok. %80 uyarı, %100 aşım.
      </div>
      <div className="flex flex-wrap items-start gap-3">
        <Field label="Günlük token" small hint="girdi + çıktı toplamı"
          inputMode="numeric" value={form.dailyTokens} onChange={set('dailyTokens')} disabled={!loaded || busy} />
        <Field label="Günlük $" small hint="fiyatı bilinmeyen model varsa yargılanmaz"
          inputMode="decimal" value={form.dailyCostUsd} onChange={set('dailyCostUsd')} disabled={!loaded || busy} />
        <Field label="p95 gecikme (ms)" small
          inputMode="numeric" value={form.p95Ms} onChange={set('p95Ms')} disabled={!loaded || busy} />
      </div>
      <div className="mt-3">
        <ActionRow confirm={<Button type="submit" loading={busy} disabled={!loaded}>Kaydet</Button>} />
      </div>
      {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
    </form>
  );
}
