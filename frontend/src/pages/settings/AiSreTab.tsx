// AiSreTab — v0.10.1286 (AI SRE ayar ekranı). system_settings["sre_agent"]
// (v0.10.1282) için admin formu + canlı durum. Settings alanı yalnız admin'e
// açık (Settings.tsx kapısı); GET/PUT /api/settings/sre de admin, PUT
// audit'li (settings.sre_agent.update).
//
// Form ↔ tel çevirisi ve Go NormalizeSettings sınırlarının aynası saf
// modülde (sreSettingsForm.ts): aralık dışı değer alanın altında hata olarak
// görünür ve PUT gitmez — sunucu sessizce tavana kırpardı.
import { useId, useMemo, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries';
import { agoTR, tsLong } from '@/lib/utils';
import type { SRESettings } from '@/lib/types';
import { AiSreStatusPanel } from './AiSreStatusPanel';
import { ContextPackPanel } from './ContextPackPanel'; // 2026-10-24
import { useAuthRole } from '@/components/AuthProvider';
import {
  SRE_NUM_BOUNDS, SRE_PRIORITIES, sreDefaultForm, sreFormToWire, sreSavedSummaryTR,
  sreToForm, togglePriority, validateSREForm, type SREForm, type SRENumKey,
} from './sreSettingsForm';
import { ConfigStatusBanner, FlashBox, SectionTitle, SettingsLoadError, SettingsSection, humanize, useSettingsLoad } from './shared';

// Faz 2 dilim 1 R12 — .sre-* / .sso-* sınıfları yerine Tailwind utility'leri
// (D1 alan anatomisi, D4 eylem satırı sağa yaslı, D13 Radix onay kutuları).
const HINT = 'text-[length:var(--fs-xs)] leading-snug text-(--text3)';
const FIELDSET = 'm-0 min-w-0 border-0 p-0';
const LEGEND = 'mb-1.5 text-[length:var(--fs-sm)] text-(--text2)';

const PRIORITY_HINT: Record<string, string> = {
  P1: 'şimdi',
  P2: 'bugün',
  P3: 'uygun zamanda',
};

export function AiSreTab() {
  const [form, setForm] = useState<SREForm>(sreDefaultForm);
  const [meta, setMeta] = useState<Pick<SRESettings, 'updatedBy' | 'updatedAt'>>({});
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const qc = useQueryClient();
  const prioHintId = useId();
  const role = useAuthRole();

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.sreSettings(),
    s => { setForm(sreToForm(s)); setMeta({ updatedBy: s.updatedBy, updatedAt: s.updatedAt }); },
  );
  const errors = useMemo(() => validateSREForm(form), [form]);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const r = sreFormToWire(form);
    if (!r.ok) {
      setFlash({ kind: 'err', text: `${Object.keys(r.errors).length} alan geçersiz — kaydedilmedi.` });
      return;
    }
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putSRESettings(r.wire);
      setForm(sreToForm(saved));
      setMeta({ updatedBy: saved.updatedBy, updatedAt: saved.updatedAt });
      setFlash({ kind: 'ok', text: sreSavedSummaryTR(saved) });
      await qc.invalidateQueries({ queryKey: keys.sre.all });
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const setNum = (k: SRENumKey) => (e: { target: { value: string } }) =>
    setForm(f => ({ ...f, nums: { ...f.nums, [k]: e.target.value } }));

  const header = (
    <>
      <h2 className="mb-1.5 text-[length:var(--fs-lg)] font-semibold">AI SRE</h2>
      <p className="mb-3 text-[length:var(--fs-md)] text-(--text2)">
        Açık problemleri deterministik okumalarla araştıran ajan. Yalnız öneri üretir —
        problem, öncelik ya da kök neden tablolarına yazmaz. Kuyruğu
        COREMETRY_MODE=sre (ya da all) rolü tüketir.
      </p>
    </>
  );

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner label="Ayar okunuyor…" />;

  return (
    <div className="max-w-[820px]">
      {header}
      <AiSreStatusPanel />
      <ConfigStatusBanner label={form.enabled ? 'AÇIK' : 'KAPALI'}>
        {!form.enabled
          ? 'Kapalı — üretici kuyruğa yazmaz, drain yeni iş almaz, "Araştır" düğmesi gizlenir.'
          : form.priorities.length === 0
            ? 'Açık — otomatik kuyruk kapalı; yalnız elle "Araştır".'
            : `Açık — ${form.priorities.join(', ')} problemleri otomatik araştırılır.`}
      </ConfigStatusBanner>
      <form onSubmit={save} noValidate>
        <SettingsSection>
          <CheckboxField className="mb-3" label="AI SRE ajanını etkinleştir" checked={form.enabled} disabled={busy}
            hint="Varsayılan: açık. Kapatmak mevcut kayıtları silmez."
            onCheckedChange={v => setForm(f => ({ ...f, enabled: v }))} />

          <SectionTitle>Otomatik kuyruk</SectionTitle>
          <fieldset className={FIELDSET} aria-describedby={prioHintId}>
            <legend className={LEGEND}>Otomatik araştırılan öncelikler</legend>
            <div className="mb-2 flex flex-wrap items-center gap-x-4 gap-y-2">
              {SRE_PRIORITIES.map(p => (
                <CheckboxField key={p} checked={form.priorities.includes(p)} disabled={busy}
                  label={<>{p} <span className={HINT}>({PRIORITY_HINT[p]})</span></>}
                  onCheckedChange={() => setForm(f => ({ ...f, priorities: togglePriority(f.priorities, p) }))} />
              ))}
            </div>
            <div id={prioHintId} className={HINT}>
              Varsayılan: P1, P2. Hiçbiri seçili değilse otomatik kuyruk kapalı — elle "Araştır" yine çalışır.
            </div>
          </fieldset>

          <SectionTitle>Tavanlar</SectionTitle>
          <div className="grid-2 gap-3">
            {SRE_NUM_BOUNDS.map(b => (
              <Field key={b.key} label={b.label} inputMode="numeric" value={form.nums[b.key]}
                onChange={setNum(b.key)} disabled={busy} placeholder={String(b.def)}
                error={errors[b.key]}
                hint={`${b.hint} · varsayılan ${b.def} · ${b.min}–${b.max}`} />
            ))}
          </div>

          <ActionRow
            secondary={meta.updatedAt ? (
              <span className={HINT} title={tsLong(meta.updatedAt)}>
                Son değişiklik{meta.updatedBy ? ` ${meta.updatedBy}` : ''} · {agoTR(meta.updatedAt)}
              </span>
            ) : (
              <span className={HINT}>Henüz kaydedilmedi — varsayılanlar geçerli.</span>
            )}
            confirm={<Button type="submit" loading={busy}>Kaydet</Button>} />
          {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
        </SettingsSection>
      </form>
      {/* Ayrı anahtar + ayrı uç (sre_agent.contextpack) — kendi formu. Rol
          bilinmiyorsa (AuthProvider dışı) Settings'in admin kapısı geçerli. */}
      <ContextPackPanel canEdit={role === undefined || role === 'admin'} />
    </div>
  );
}
