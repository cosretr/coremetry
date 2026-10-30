import { useEffect, useState, type FormEvent, type ChangeEvent } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, SelectField, TextareaField, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { useConfirm } from '@/components/coremetry/Confirm';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { DEFAULT_BRANDING, ORACLE_GROUP_LABEL_MAX, invalidateBranding, type BrandingSettings } from '@/lib/branding';
import { SettingsLoadError, useSettingsLoad, FlashBox } from './shared';

// Faz 2 dilim 1 R14 — inline stil → Tailwind utility; shared Field/Row +
// ui/ atomları → coremetry Field ailesi + shadcn. Alan sırası, metinler,
// kaydetme gövdesi aynı. Row düzeni: flex-wrap, alan flex 1 / min 200.
const H2 = 'mb-1.5 text-[length:var(--fs-lg)] font-semibold';
const ROW = 'flex flex-wrap items-start gap-3';
const WIDE = 'min-w-[200px] flex-1';

// BrandingTab — white-label / customisation form. Admin paints the
// login page (logo + title + button label + footer) and the
// browser tab title. Everything is optional; an empty value
// reverts to the bundled Coremetry default. Saved overlay is
// applied immediately via invalidateBranding() so the operator
// doesn't have to reload to see the result.
//
// Logo upload reads the local file as a data URI and caps the
// raw size at 200 KB — big enough for a 200 px PNG, small
// enough that the system_settings row stays cheap to fetch on
// every login page render.
export function BrandingTab() {
  const confirm = useConfirm();
  const [b, setB] = useState<BrandingSettings>({});
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const { loaded, error: loadErr, retry } = useSettingsLoad(
    () => api.getBranding(),
    v => {
      setB(v ?? {});
    },
  );

  if (loadErr) return <SettingsLoadError error={loadErr} onRetry={retry} />;
  if (!loaded) return <Spinner />;

  const set = (k: keyof BrandingSettings, v: string) =>
    setB(prev => ({ ...prev, [k]: v }));

  const onLogo = (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    if (!f) return;
    if (f.size > 200 * 1024) {
      setMsg({ kind: 'err', text: `Logo file too large (${(f.size / 1024).toFixed(0)} KB) — keep it under 200 KB.` });
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      const result = reader.result;
      if (typeof result === 'string') {
        set('logoDataUri', result);
        setMsg(null);
      }
    };
    reader.onerror = () => setMsg({ kind: 'err', text: 'Failed to read file.' });
    reader.readAsDataURL(f);
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      await api.putBranding(b);
      await invalidateBranding();
      setMsg({ kind: 'ok', text: 'Saved — branding applied immediately.' });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Save failed' });
    } finally {
      setBusy(false);
    }
  };

  const resetAll = async () => {
    if (!await confirm({
      title: 'Marka ayarları sıfırlansın mı?',
      body: <>Yüklenmiş logo ve tüm özel metinler (giriş başlığı, buton
        etiketi, alt bilgi) SİLİNECEK ve Coremetry varsayılanlarına
        dönülecek. Geri alınamaz.</>,
      confirmLabel: 'Varsayılanlara dön',
      danger: true,
    })) return;
    setBusy(true); setMsg(null);
    try {
      await api.putBranding({});
      setB({});
      await invalidateBranding();
      setMsg({ kind: 'ok', text: 'Reset — Coremetry defaults restored.' });
    } catch (err) {
      setMsg({ kind: 'err', text: err instanceof Error ? err.message : 'Reset failed' });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="max-w-[720px]">
      <h2 className={H2}>Branding</h2>
      <p className="mb-4 text-[length:var(--fs-md)] text-(--text2)">
        White-label the login page + browser tab title. Empty fields fall back to the
        Coremetry defaults shown as placeholders. Changes apply immediately — no
        restart, no reload needed.
      </p>

      <form onSubmit={save} className="flex flex-col gap-3 rounded-lg border border-(--border) bg-(--bg2) p-4">
        <div className={ROW}>
          <Field className={WIDE} label="App name" value={b.appName ?? ''}
            onChange={e => set('appName', e.target.value)}
            placeholder={DEFAULT_BRANDING.appName} />
          <Field className={WIDE} label="Browser tab title" value={b.browserTitle ?? ''}
            onChange={e => set('browserTitle', e.target.value)}
            placeholder={DEFAULT_BRANDING.browserTitle} />
        </div>

        <Field label="Login page title" value={b.loginTitle ?? ''}
          onChange={e => set('loginTitle', e.target.value)}
          placeholder="Sign in to Coremetry" />
        <TextareaField label="Login subtitle (optional — shown under the title)"
          value={b.loginSubtitle ?? ''} onChange={e => set('loginSubtitle', e.target.value)}
          placeholder='e.g. "Acme Bank observability. Access requires VPN."'
          rows={2} controlClassName="resize-y" />

        <div className={ROW}>
          <Field className={WIDE} label="Sign-in button label" value={b.signInButtonLabel ?? ''}
            onChange={e => set('signInButtonLabel', e.target.value)}
            placeholder={DEFAULT_BRANDING.signInButtonLabel} />
          <Field className={WIDE} label="Username field label" value={b.usernameLabel ?? ''}
            onChange={e => set('usernameLabel', e.target.value)}
            placeholder='e.g. "Corporate ID" or "Domain user"' />
        </div>

        <Field label="Footer text (small line at the bottom of the login card)"
          value={b.footerText ?? ''} onChange={e => set('footerText', e.target.value)}
          placeholder='e.g. "© Acme Bank · Internal use only"' />

        <SelectField label="UI language" className="max-w-[260px]"
          value={b.language ?? 'en'}
          onChange={e => set('language', e.target.value)}
          hint={<>Drives sidebar labels, login strings, common buttons, page titles.
            Applies to every operator hitting this Coremetry instance.</>}>
          <option value="en">English (default)</option>
          <option value="tr">Türkçe</option>
        </SelectField>

        {/* v0.10.1334 — rehberli tur: yeni kullanıcıya (hesap ≤ 7 gün) ilk
            girişte kendiliğinden açılsın mı. Kapalıyken menüdeki "Turu yeniden
            başlat" çalışmaya devam eder; anahtar yalnız oto-başlatmayı keser. */}
        <SelectField label="Rehberli tur (yeni kullanıcı)"
          value={b.onboardingTour === 'off' ? 'off' : 'on'}
          onChange={e => set('onboardingTour', e.target.value)}
          hint="Açık: ilk 7 gün içindeki kullanıcıya tur ilk girişte bir kez açılır. Kullanıcı menüsündeki “Turu yeniden başlat” her durumda çalışır.">
          <option value="on">Açık (varsayılan)</option>
          <option value="off">Kapalı</option>
        </SelectField>

        {/* v0.10.1108 (operatör: "exceptionsta Oracle yazıyor onun yerine … Teknik
            Hata gibi") — `ora:` gruplarının görünen adı; boş = "Teknik hata".
            Sunucu kırpar + 40 rune'a keser; maxLength aynı tavan. */}
        <Field label="Oracle hata grubu etiketi"
          value={b.oracleGroupLabel ?? ''} onChange={e => set('oracleGroupLabel', e.target.value)}
          placeholder={DEFAULT_BRANDING.oracleGroupLabel} maxLength={ORACLE_GROUP_LABEL_MAX}
          hint='Exceptions/Problems satırlarında, rozetinde, çipinde ve AI satırında "Oracle" kelimesinin yerine görünür.' />

        <div className="flex items-end gap-2">
          <Field className="flex-1" label="Primary color (CSS — e.g. #4f46e5, rgb(79,70,229))"
            value={b.primaryColor ?? ''} onChange={e => set('primaryColor', e.target.value)}
            placeholder="leave empty for the bundled accent" />
          {/* Faz 2 dilim 1 R14 — hesaplanan renk inline stil değil: ref
              `--swatch` CSS değişkenini yazar, sınıf okur. */}
          {b.primaryColor && (
            <span aria-hidden="true" data-swatch=""
              ref={el => { el?.style.setProperty('--swatch', b.primaryColor ?? ''); }}
              className="h-8 w-8 shrink-0 rounded border border-(--border) bg-(--swatch)" />
          )}
        </div>

        <div className="flex min-w-0 flex-col gap-1">
          <span className="text-[length:var(--fs-sm)] leading-tight text-(--text2)">Logo (PNG / SVG / JPG, ≤ 200 KB)</span>
          <div className="flex items-center gap-3">
            {/* Yerel dosya girdisi bilinçli olarak native (plan Q3). */}
            <input type="file" accept="image/png,image/svg+xml,image/jpeg,image/webp"
              aria-label="Logo (PNG / SVG / JPG, ≤ 200 KB)"
              className="text-[length:var(--fs-sm)]"
              onChange={onLogo} />
            {b.logoDataUri && (
              <>
                <img src={b.logoDataUri} alt="logo preview"
                  className="max-h-12 max-w-[140px] rounded border border-(--border) bg-(--bg) object-contain p-1" />
                <Button type="button" variant="destructive" size="sm"
                  onClick={() => set('logoDataUri', '')}>
                  Remove
                </Button>
              </>
            )}
          </div>
        </div>

        {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}

        {/* v0.9.1006 (M4/O6) — dolu Save'in yanında dolu kırmızı ikinci
            bir ağırlık vardı. `ghost-danger` kırmızı DİLİ taşır, ikinci
            dolgu değil (R14: shadcn'deki coremetry varyantı); ActionRow
            yıkıcıyı EN SOLA, Save'i EN SAĞA koyar (D4). */}
        <ActionRow
          destructive={
            <Button type="button" variant="ghost-danger"
              onClick={() => { void resetAll(); }} disabled={busy}>
              Reset to defaults
            </Button>
          }
          confirm={<Button type="submit" loading={busy}>Save</Button>} />
      </form>

      <AnnouncementSection />
    </div>
  );
}

// AnnouncementSection — v0.8.486 (operatör isteği): sayfa-üstü duyuru
// şeridi. Metin + opsiyonel link + ton; kaydet revizyon damgasını
// yeniler, kapatmış kullanıcılara şerit yeniden görünür.
function AnnouncementSection() {
  const [a, setA] = useState<import('@/lib/announcement').AnnouncementView | null | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  useEffect(() => {
    api.getAnnouncement().then(setA).catch(() => setA(null));
  }, []);

  if (a === undefined) return <div className="mt-6"><Spinner /></div>;
  if (a === null) return null;

  const save = async () => {
    setBusy(true); setMsg(null);
    try {
      const saved = await api.putAnnouncement(a);
      setA(saved);
      setMsg('Kaydedildi — kapatmış kullanıcılara da yeniden görünür.');
    } catch (e) {
      setMsg(e instanceof Error ? e.message : 'Kaydedilemedi');
    } finally { setBusy(false); }
  };

  return (
    <div className="mt-7 border-t border-(--border) pt-[18px]">
      <h2 className={cn(H2, 'flex items-center gap-2')}>
        Duyuru şeridi
        <StatusBadge status="unknown">{a.enabled && a.text ? 'aktif' : 'kapalı'}</StatusBadge>
      </h2>
      <p className="mb-3 text-[length:var(--fs-md)] text-(--text2)">
        Tüm kullanıcılara sayfa üstünde gösterilen genel duyuru — ör. destek
        e-postası, wiki adresi, planlı bakım notu. Kullanıcı kapatabilir;
        metni güncellediğinde herkese yeniden görünür.
      </p>
      <div className="flex flex-col gap-3">
        <Field label="Duyuru metni" value={a.text ?? ''} maxLength={500}
          placeholder="Coremetry ile ilgili sorularınız için: apm-destek@…"
          onChange={e => setA({ ...a, text: e.target.value })} />
        <div className={ROW}>
          <Field className={WIDE} label="Link (opsiyonel)" value={a.linkUrl ?? ''}
            placeholder="http://wiki.local/coremetry"
            onChange={e => setA({ ...a, linkUrl: e.target.value })} />
          <Field className={WIDE} label="Link etiketi" value={a.linkLabel ?? ''} maxLength={80}
            placeholder="Wiki"
            onChange={e => setA({ ...a, linkLabel: e.target.value })} />
          <SelectField small label="Ton" value={a.tone ?? 'info'}
            onChange={e => setA({ ...a, tone: e.target.value as 'info' | 'warn' })}>
            <option value="info">Bilgi</option>
            <option value="warn">Önemli (amber)</option>
          </SelectField>
        </div>
      </div>
      <div className="mt-2.5 flex items-center gap-2.5">
        <CheckboxField label="Şerit aktif" checked={a.enabled}
          onCheckedChange={v => setA({ ...a, enabled: v })} />
        <Button onClick={() => { void save(); }} loading={busy}>
          Kaydet
        </Button>
        {msg && <span role="status" className="text-[length:var(--fs-sm)] text-(--text2)">{msg}</span>}
      </div>
    </div>
  );
}
