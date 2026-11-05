import { useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import { useSetIncidentsFeature } from '@/components/AuthProvider';
import { FlashBox, humanize } from './shared';

// v0.10.1826 — eski Incidents akışının TEK ana anahtarı (system_settings
// `incidents`, varsayılan KAPALI; operatör kararı 2026-11-05). Durum
// AnomalyPromotionTab'da tutulur (aile kutuları aynı değeri okur); burada
// yalnız taslak + kaydet. Kayıt kabuk bayrağını da günceller (menü /
// Problems türleri yeniden yüklemesiz).
export function IncidentsLegacySection({ saved, onSaved, loadError }: {
  saved: boolean | null;
  onSaved: (on: boolean) => void;
  loadError: string | null;
}) {
  const [draft, setDraft] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const setShellFlag = useSetIncidentsFeature();
  const value = draft ?? saved;

  const save = async () => {
    if (value === null) return;
    setBusy(true); setFlash(null);
    try {
      const res = await api.putIncidentsSettings({ enabled: value });
      onSaved(res.enabled);
      setShellFlag(res.enabled);
      setDraft(null);
      setFlash({ kind: 'ok', text: 'Kaydedildi.' });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mb-[18px] rounded-[4px] border border-(--border) px-2.5 py-2">
      {loadError && <FlashBox kind="err">{loadError}</FlashBox>}
      <CheckboxField aria-label="Eski Incidents akışı"
        checked={value === true}
        disabled={value === null}
        onCheckedChange={v => setDraft(v)}
        label={<>Eski Incidents akışı (emekli)</>}
        hint={<>
          Kapalıyken otomatik incident oluşmaz, incident bildirimi gitmez; yerine
          Anomalies kullanılır. Veri silinmez.
        </>} />
      <div className="mt-2">
        <ActionRow confirm={
          <Button onClick={save} disabled={value === null || draft === null || draft === saved} loading={busy}>
            Kaydet
          </Button>
        } />
        {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
      </div>
    </div>
  );
}
