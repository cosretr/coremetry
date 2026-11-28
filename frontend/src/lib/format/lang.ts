import type { Lang } from '../i18n';

// lib/format/lang — v0.10.2484 (entity tutarlılık denetimi dilim 1).
//
// Biçimleyiciler saf: her işlev `lang` alır. Hook kullanamayan eski
// `lib/utils` fmt* sarmalayıcıları dili buradan okur. Kaynak `lib/i18n`
// modül yüklenirken `setLangSource(currentLang)` ile bağlanır — biçim
// modülü i18n'i İÇE AKTARMAZ (i18n'i sahteleyen testler fmt*'yi kırmasın,
// döngü olmasın). Bağlanmamışsa İngilizce.
export type { Lang };

let source: () => Lang = () => 'en';

export function setLangSource(fn: () => Lang): void {
  source = fn;
}

export function activeLang(): Lang {
  return source();
}

/** Verinin olmadığı yerde basılan tek işaret (uygulamanın yerleşik glifi). */
export const NO_DATA = '—';

export function isNum(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v);
}
