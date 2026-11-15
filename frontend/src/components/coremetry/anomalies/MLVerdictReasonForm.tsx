// MLVerdictReasonForm — v0.10.2256 (anomali kartı mockup §5, operatör onayı):
// yeni kartta "Sorun değil" bir neden sorar. Dört hazır seçenek (kapalı küme,
// sunucu chstore.NormalizeVerdictReason ile aynı) + isteğe bağlı not ("Diğer"
// için zorunlu, ≤ 500 karakter — sunucu da keser). Neden yalnız karne
// etiketidir: eşik, tespit, bildirim ya da Problem kararını değiştirmez.
import { useState, type FormEvent } from 'react';
import { Button } from '@/components/shadcn/button';
import { Textarea } from '@/components/shadcn/textarea';
import { useT } from '@/lib/i18n';
import { VERDICT_NOTE_MAX, VERDICT_REASONS, reasonFormError, type VerdictReason } from './mlVerdictReason';

export function MLVerdictReasonForm({ busy, onSave, onCancel }: {
  busy: boolean; onSave: (reason: VerdictReason, note: string) => void; onCancel: () => void;
}) {
  const t = useT();
  const [reason, setReason] = useState<VerdictReason | ''>('');
  const [note, setNote] = useState('');
  const [err, setErr] = useState<string | null>(null);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    const k = reasonFormError(reason, note);
    if (k || !reason) { setErr(k ?? 'mlRecord.reason.pick'); return; }
    onSave(reason, note.trim());
  };
  return (
    <form data-testid="ml-verdict-reason" onSubmit={submit} noValidate className="mt-2.5">
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="mb-1 text-xs font-semibold">{t('mlRecord.reason.legend')}</legend>
        {VERDICT_REASONS.map(r => (
          <label key={r} className="flex cursor-pointer items-start gap-2 text-sm">
            <input type="radio" name="ml-verdict-reason" value={r} checked={reason === r} className="mt-1"
              onChange={() => { setReason(r); setErr(null); }} />
            <span><b className="font-medium">{t(`mlRecord.reason.${r}`)}</b> <span className="text-xs text-muted-foreground">{t(`mlRecord.reason.${r}.hint`)}</span></span>
          </label>
        ))}
        <label htmlFor="ml-verdict-note" className="mt-1 text-xs text-muted-foreground">
          {t(reason === 'other' ? 'mlRecord.reason.noteRequired' : 'mlRecord.reason.noteOptional')}
        </label>
        <Textarea id="ml-verdict-note" className="bg-field dark:bg-field" value={note} maxLength={VERDICT_NOTE_MAX} rows={2}
          placeholder={t('mlRecord.reason.placeholder')} onChange={e => { setNote(e.target.value); setErr(null); }} />
        <div className="mt-1 flex flex-wrap items-center gap-2">
          <Button size="sm" type="submit" disabled={busy}>{t('mlRecord.reason.save')}</Button>
          <Button size="sm" type="button" variant="ghost" disabled={busy} onClick={onCancel}>{t('mlRecord.reason.cancel')}</Button>
          {err && <span role="status" className="text-xs text-[color:var(--err)]">{t(err)}</span>}
        </div>
      </fieldset>
    </form>
  );
}
