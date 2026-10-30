// ContextPackPanel — CoSRE Context Pack ayarı (2026-10-24; uç v0.10.1494).
// system_settings["sre_agent.contextpack"] — sre_agent formundan AYRI anahtar ve
// AYRI uç (GET/PUT /api/settings/sre/contextpack, admin, PUT audit'li), bu yüzden
// AI SRE formunun altında kendi formu ve kendi Kaydet'i var: tek düğme iki ucu
// yazsaydı biri düştüğünde diğeri yarım kalırdı.
//
// canEdit=false (admin olmayan) → alanlar salt okunur, Kaydet yok. Settings
// bugün admin kapılı; kapı gevşerse bölüm yazma açmaz. Okuma 403/hata →
// SettingsLoadError (boş formu kaydetmek saklı ayarın üstüne yazardı).
import { useId, useMemo, useState, type FormEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import { agoTR, tsLong } from '@/lib/utils';
import type { SREContextPackSettings } from '@/lib/types';
import {
  PACK_BASELINES, PACK_MAX_TOKENS, PACK_TOP_ANOMALIES, packDefaultForm, packFormToWire,
  packToForm, toggleBaseline, validatePackForm, type PackForm,
} from './contextPackForm';
import { FlashBox, SectionTitle, SettingsLoadError, SettingsSection, humanize, useSettingsLoad } from './shared';

// Faz 2 dilim 1 R12 — .sre-* / .sso-* sınıfları yerine Tailwind utility'leri
// (AiSreTab ile aynı ölçüler: D1, D4, D13).
const HINT = 'text-[length:var(--fs-xs)] leading-snug text-(--text3)';
const ERR = 'text-[length:var(--fs-xs)] leading-snug text-status-critical';

export function ContextPackPanel({ canEdit }: { canEdit: boolean }) {
  const [form, setForm] = useState<PackForm>(packDefaultForm);
  const [meta, setMeta] = useState<Pick<SREContextPackSettings, 'updatedBy' | 'updatedAt'>>({});
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const blHintId = useId();

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.sreContextPackSettings(),
    s => { setForm(packToForm(s)); setMeta({ updatedBy: s?.updatedBy, updatedAt: s?.updatedAt }); },
  );
  const errors = useMemo(() => validatePackForm(form), [form]);
  const ro = !canEdit || busy;

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!canEdit) return;
    const r = packFormToWire(form);
    if (!r.ok) {
      setFlash({ kind: 'err', text: `${Object.keys(r.errors).length} alan geçersiz — kaydedilmedi.` });
      return;
    }
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putSREContextPackSettings(r.wire);
      setForm(packToForm(saved));
      setMeta({ updatedBy: saved.updatedBy, updatedAt: saved.updatedAt });
      setFlash({ kind: 'ok', text: `Kaydedildi — Context Pack ${saved.enabled ? 'AÇIK' : 'KAPALI'} · ${saved.maxTokens} token · ${saved.baselines.length} referans · ilk ${saved.topAnomalies} anomali. Tüm pod'larda ≤30 sn.` });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const body = loadErr ? <SettingsLoadError error={loadErr} onRetry={retry} />
    : !loaded ? <Spinner label="Context Pack ayarı okunuyor…" />
    : (
      <form onSubmit={save} noValidate data-context-pack>
        <SettingsSection>
          <CheckboxField className="mb-3" label="Context Pack'i etkinleştir" checked={form.enabled} disabled={ro}
            hint="Varsayılan: kapalı. Kapalıyken anlatım girdisi bugünkü 4 KB kesimidir."
            onCheckedChange={v => setForm(f => ({ ...f, enabled: v }))} />

          <div className="grid-2 mb-3 gap-3">
            <Field label="Token tavanı" inputMode="numeric" value={form.maxTokens} disabled={ro}
              placeholder={String(PACK_MAX_TOKENS.def)} error={errors.maxTokens}
              onChange={e => setForm(f => ({ ...f, maxTokens: e.target.value }))}
              hint={`öncelikli kırpmanın tavanı (≈ 4 bayt/token) · varsayılan ${PACK_MAX_TOKENS.def} · ${PACK_MAX_TOKENS.min}–${PACK_MAX_TOKENS.max}`} />
            <Field label="En çok anomali" inputMode="numeric" value={form.topAnomalies} disabled={ro}
              placeholder={String(PACK_TOP_ANOMALIES.def)} error={errors.topAnomalies}
              onChange={e => setForm(f => ({ ...f, topAnomalies: e.target.value }))}
              hint={`öznenin skor sıralı ilk N anomalisi · varsayılan ${PACK_TOP_ANOMALIES.def} · ${PACK_TOP_ANOMALIES.min}–${PACK_TOP_ANOMALIES.max}`} />
          </div>

          <fieldset className="m-0 min-w-0 border-0 p-0" aria-describedby={blHintId} aria-invalid={errors.baselines ? 'true' : undefined}>
            <legend className="mb-1.5 text-[length:var(--fs-sm)] text-(--text2)">Taban referansları</legend>
            <div className="mb-2 flex flex-wrap items-center gap-x-4 gap-y-2">
              {PACK_BASELINES.map(b => (
                <CheckboxField key={b.value} checked={form.baselines.includes(b.value)} disabled={ro}
                  label={<>{b.label} <span className={HINT}>({b.hint})</span></>}
                  onCheckedChange={() => setForm(f => ({ ...f, baselines: toggleBaseline(f.baselines, b.value) }))} />
              ))}
            </div>
            <div id={blHintId} className={errors.baselines ? ERR : HINT}>
              {errors.baselines ?? 'Varsayılan: önceki pencere + geçen hafta. Her referans RED karşılaştırma satırı ekler.'}
            </div>
          </fieldset>

          <ActionRow
            secondary={<>
              {meta.updatedAt ? (
                <span className={HINT} title={tsLong(meta.updatedAt)}>
                  Son değişiklik{meta.updatedBy ? ` ${meta.updatedBy}` : ''} · {agoTR(meta.updatedAt)}
                </span>
              ) : (
                <span className={HINT}>Henüz kaydedilmedi — varsayılanlar geçerli (kapalı).</span>
              )}
              {!canEdit && <span className={HINT}>Salt okunur — yalnız admin değiştirebilir.</span>}
            </>}
            confirm={canEdit ? <Button type="submit" loading={busy}>Kaydet</Button> : undefined} />
          {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
        </SettingsSection>
      </form>
    );

  return (
    <section aria-label="Context Pack">
      <SectionTitle>Context Pack</SectionTitle>
      <p className="mb-3 text-[length:var(--fs-md)] text-(--text2)">
        Araştırma anlatımına giden deterministik bağlamı zenginleştirir: seçili taban
        referanslarıyla RED karşılaştırması, öznenin en yüksek skorlu anomalileri, hata özeti
        ve deploy etkisi — token tavanına öncelik sırasıyla kırpılarak. Yalnız modelin okuduğu
        metni değiştirir; hipotez skoru bu paketi hiç okumaz (skor aynı kalır).
      </p>
      {body}
    </section>
  );
}
