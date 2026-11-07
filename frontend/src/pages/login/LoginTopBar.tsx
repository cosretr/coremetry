// LoginTopBar — v0.10.1906 (Login yeniden tasarımı, audit §6.1). Sol: marka
// (operatör logosu varsa o, yoksa "✦ Wordmark"); sağ: TR · EN seçici + tema.
// Üst çubuk akışta değil kartın üstünde `absolute` durur (yapışkan/yüzen şerit
// YOK — feedback-no-floating-strips): sayfa kayarsa birlikte kayar.
// Dil seçici `coremetry/SegmentedControl`: mevcut `LangToggle` tek düğmedir ve
// ÖBÜR dili gösterir; burada iki dil de görünür, seçili olan dolu çizilir.
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { ThemeToggle } from '@/components/ThemeToggle';
import { Wordmark } from '@/components/Wordmark';
import { useBranding } from '@/lib/branding';
import { setUserLang, useLang, useT, type Lang } from '@/lib/i18n';

const LANGS = [
  { value: 'tr', label: 'TR' },
  { value: 'en', label: 'EN' },
] as const;

export function LoginTopBar() {
  const brand = useBranding();
  const t = useT();
  const lang = useLang();
  return (
    // v0.10.1917 — şeridin boş alanı arka plandaki topolojiye (sürükleme) geçer;
    // yalnız marka ve araçlar olay alır.
    <div className="pointer-events-none absolute inset-x-0 top-0 flex items-center justify-between gap-3 px-6 py-4">
      <div className="pointer-events-auto flex min-w-0 items-center text-[length:var(--fs-xl)] font-bold tracking-tight">
        {brand.logoDataUri ? (
          <img src={brand.logoDataUri} alt={brand.appName}
            className="max-h-8 max-w-[160px] object-contain" />
        ) : (
          <>
            <span aria-hidden="true" className="mr-2 text-(--brand)">✦</span>
            <span className="truncate"><Wordmark name={brand.appName} /></span>
          </>
        )}
      </div>
      <div className="pointer-events-auto flex shrink-0 items-center gap-2">
        <SegmentedControl<Lang> aria-label={t('login.language')} value={lang}
          onChange={setUserLang} options={LANGS} />
        <ThemeToggle />
      </div>
    </div>
  );
}
