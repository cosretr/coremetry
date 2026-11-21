import { useEffect, useState, useSyncExternalStore } from 'react';
import { DEFAULT_BRANDING, useBranding } from './branding';
import { getRaw, setRaw, removeRaw, getSessionRaw, setSessionRaw } from './storage';

// Tiny in-repo i18n. No external dependency — the SPA's
// branding settings carry a `language` field that picks one of
// the catalogs below. English is the source of truth + the
// default; Turkish covers the high-traffic surfaces (sidebar,
// login, common buttons, page titles, empty/error states).
//
// Catalog covers ~80% of what an operator sees during morning
// triage. Strings that aren't in the catalog fall back to the
// English key verbatim so a missing translation never renders
// blank.
export type Lang = 'en' | 'tr';

type Catalog = Record<string, string>;

// v0.10.2400 — İKİ katalog da tembel (ilk yükte yok): i18n.tr.ts ve
// i18n.en.ts yalnız loadCatalog()'un dinamik import()'u ile gelir; ikisi de
// i18n.common.ts'in EN_EAGER yedeğini (TR'de karşılığı olmayan anahtarlar)
// yeniden dışa verir. main.tsx ilk çizimden ÖNCE loadInitialCatalog()'u
// bekler: kayıtlı kullanıcı seçimi → son görülen marka dili (brandLang
// ipucu) → Türkçe — ham anahtar ya da yanlış dil flaşı yok. Çalışırken dil
// değişince hedef katalog yüklenene dek ELDEKİ yüklü dil çizilir, sonra
// abonelere (useSyncExternalStore) haber verilir ve her useLang tüketicisi
// yeniden çizilir. Arama: TR[key] ?? EN_EAGER[key] ?? key;
// EN[key] ?? EN_EAGER[key] ?? key (EN yüklü değilse TR'ye düşer).
const catalogs: Record<Lang, Catalog | null> = { tr: null, en: null };
let EN_EAGER: Catalog = {};
const pending: Partial<Record<Lang, Promise<void>>> = {};
let catalogVersion = 0;
const catalogListeners = new Set<() => void>();

function subscribeCatalogs(cb: () => void): () => void {
  catalogListeners.add(cb);
  return () => { catalogListeners.delete(cb); };
}
const catalogSnapshot = (): number => catalogVersion;

// effectiveLang — istenen dilin kataloğu yüklüyse o; değilse yüklü olan
// diğeri (ham anahtar çizilmez); hiçbiri yoksa 'tr' (yalnız açılış hatası).
function effectiveLang(wanted: Lang): Lang {
  if (catalogs[wanted]) return wanted;
  return catalogs.en ? 'en' : 'tr';
}

function registerCatalog(lang: Lang, cat: Catalog | null, eager?: Catalog): void {
  catalogs[lang] = cat;
  if (eager) EN_EAGER = eager;
  delete pending[lang];
  catalogVersion++;
  for (const l of [...catalogListeners]) l();
}

// vitest kurulumu (src/test/i18nEnSetup.ts) iki kataloğu da modül
// değerlendirmesinden ÖNCE globalThis'e bırakır (modül düzeyi t() sabitleri
// için). Üretim derlemesinde MODE sabit → blok ölü kod, atılır.
if (import.meta.env.MODE === 'test') {
  const pre = (globalThis as { __cmI18nPreload?: { tr: Catalog; en: Catalog; eager: Catalog } }).__cmI18nPreload;
  if (pre) { catalogs.tr = pre.tr; catalogs.en = pre.en; EN_EAGER = pre.eager; }
}

// registerEnCatalog — kataloğu kaydeder + abonelere haber verir. Üretimde
// yalnız loadCatalog çağırır; testler (i18n.lazyEn.test) boşaltıp geri koyar.
export function registerEnCatalog(cat: Catalog | null, eager?: Catalog): void {
  registerCatalog('en', cat, eager);
}

// loadCatalog — dil başına tek uçuş (çift fetch yok); asla reddetmez: hata =
// eldeki dil kalır, bir sonraki istek yeniden dener.
export function loadCatalog(lang: Lang): Promise<void> {
  if (catalogs[lang]) return Promise.resolve();
  let p = pending[lang];
  if (!p) {
    const mod = lang === 'en'
      ? import('./i18n.en').then((m) => ({ cat: m.EN, eager: m.EN_EAGER }))
      : import('./i18n.tr').then((m) => ({ cat: m.TR, eager: m.EN_EAGER }));
    p = mod
      .then(({ cat, eager }) => { registerCatalog(lang, cat, eager); retryCount = 0; })
      .catch((err: unknown) => {
        delete pending[lang];
        // v0.10.2416 — operator-reported (prod): both catalog chunks failed to
        // load, the app mounted with raw keys and never tried again. Log the
        // cause (chunk 404 / blocked by a proxy / MIME) and retry while no
        // catalog is loaded at all.
        console.error(`[i18n] catalog '${lang}' could not be loaded`, err);
        scheduleRetry(lang);
      });
    pending[lang] = p;
  }
  return p;
}

// Retry with backoff while NO catalog is loaded (one language loaded is
// enough to draw real text). After the last attempt, reload the page once per
// tab session: a tab opened before an upgrade asks for chunk names the new
// build no longer serves, and only a fresh index.html fixes that.
const RETRY_DELAYS_MS = [1000, 3000, 8000, 20000, 45000];
const RELOAD_GUARD_KEY = 'coremetry.i18nReloaded';
let retryCount = 0;
let retryTimer: ReturnType<typeof setTimeout> | null = null;

function scheduleRetry(lang: Lang): void {
  if (catalogs.tr || catalogs.en || retryTimer || typeof window === 'undefined') return;
  if (retryCount >= RETRY_DELAYS_MS.length) {
    // getSessionRaw/setSessionRaw swallow storage errors; if the guard
    // can't be written, don't reload (no loop).
    if (!getSessionRaw(RELOAD_GUARD_KEY)) {
      setSessionRaw(RELOAD_GUARD_KEY, '1');
      if (getSessionRaw(RELOAD_GUARD_KEY)) window.location.reload();
    }
    return;
  }
  const delay = RETRY_DELAYS_MS[retryCount++];
  retryTimer = setTimeout(() => {
    retryTimer = null;
    if (!catalogs.tr && !catalogs.en) void loadCatalog(lang);
  }, delay);
}
export const loadEnCatalog = (): Promise<void> => loadCatalog('en');

// loadInitialCatalog — main.tsx ilk çizimden önce bekler. İstenen dil:
// kullanıcı seçimi → son görülen marka dili → 'tr'. Yüklenemezse diğeri
// denenir. Asla reddetmez.
export async function loadInitialCatalog(): Promise<void> {
  const wanted = readUserLang() ?? readBrandLangHint() ?? 'tr';
  await loadCatalog(wanted);
  if (!catalogs[wanted]) await loadCatalog(wanted === 'en' ? 'tr' : 'en');
}

function lookup(key: string, lang: Lang): string {
  // Modül düzeyinde t() (ilk yükte, katalog gelmeden) ham anahtar döndürür:
  // sabiti fonksiyona çevir. Yalnız geliştirmede uyarır.
  if (import.meta.env.DEV && !catalogs.tr && !catalogs.en) console.warn(`[i18n] '${key}' katalog yüklenmeden okundu`);
  if (lang === 'tr') {
    const tr = catalogs.tr;
    if (tr) return tr[key] ?? EN_EAGER[key] ?? key;
    // v0.10.2421 — operator-reported (prod, English UI): CoSRE text is
    // always Turkish (tCosre → 'tr'), but since v0.10.2403 an English
    // session only loads the EN catalog, so every CoSRE key rendered raw.
    // Start the TR load (idempotent, deduped) and show the English text
    // meanwhile instead of a raw key; subscribers re-render when TR arrives.
    void loadCatalog('tr');
    return catalogs.en?.[key] ?? EN_EAGER[key] ?? key;
  }
  const en = catalogs.en;
  return en?.[key] ?? EN_EAGER[key] ?? (en ? key : catalogs.tr?.[key] ?? key);
}

// User-level language override. Stored in localStorage so it
// survives reloads without a server round trip and so unauthed
// pages (the public-trace viewer, the login screen) can also
// honour the picked language. When unset, the branding-level
// language wins — operators who never touch the picker get the
// org default they always had.
const USER_LANG_KEY = 'coremetry.lang';
const USER_LANG_EVENT = 'coremetry:lang-change';
// v0.10.2400 — son çözülen marka dili (ipucu, lib/branding.ts fetch'i
// yazar): kullanıcı seçimi yokken açılış kataloğu bununla seçilir;
// İngilizce markalı kurulum ikinci açılıştan itibaren Türkçe flaşı görmez.
const BRAND_LANG_KEY = 'coremetry.brandLang'; // = STORAGE_KEYS.brandLang (mock'lu storage'da sabit yok)

function readUserLang(): Lang | null {
  if (typeof window === 'undefined') return null;
  const v = getRaw(USER_LANG_KEY);
  if (v === 'tr' || v === 'en') return v;
  return null;
}

function readBrandLangHint(): Lang | null {
  if (typeof window === 'undefined') return null;
  const v = getRaw(BRAND_LANG_KEY);
  return v === 'tr' || v === 'en' ? v : null;
}

// useUserLang subscribes to localStorage + the same-tab
// USER_LANG_EVENT. The storage listener fires on OTHER tabs;
// the custom event fires within the current tab where the
// picker was clicked, so both paths flow back to every
// useT consumer immediately.
export function useUserLang(): Lang | null {
  const [lang, setLang] = useState<Lang | null>(() => readUserLang());
  useEffect(() => {
    const handler = () => setLang(readUserLang());
    window.addEventListener(USER_LANG_EVENT, handler);
    window.addEventListener('storage', handler);
    return () => {
      window.removeEventListener(USER_LANG_EVENT, handler);
      window.removeEventListener('storage', handler);
    };
  }, []);
  return lang;
}

// setUserLang persists the choice + broadcasts the same-tab
// event so every useT consumer re-renders without a manual
// reload. Passing null clears the override (falls back to
// branding). A language also starts its catalog import (v0.10.2400).
export function setUserLang(lang: Lang | null): void {
  if (typeof window === 'undefined') return;
  if (lang) setRaw(USER_LANG_KEY, lang);
  else removeRaw(USER_LANG_KEY);
  if (lang) void loadCatalog(lang);
  window.dispatchEvent(new Event(USER_LANG_EVENT));
}

// lastResolvedLang — v0.10.948: useLang'in en son çözdüğü İSTENEN dil (marka
// varsayılanı dahil); hiçbir bileşen henüz çizilmediyse null.
let lastResolvedLang: Lang | null = null;

// useLang resolves the EFFECTIVE language: user-picked
// (localStorage) → branding default → English, and — v0.10.2307 —
// 'en' only once the English catalog is loaded ('tr' until then).
// The branding default asks for English only after /api/branding
// resolved (DEFAULT_BRANDING's pre-fetch 'en' is not a request), so a
// Turkish install never downloads the English chunk.
// Exported for components that need locale-aware formatting beyond
// catalog lookups (e.g. TimeRangePicker's month/weekday tables).
export function useLang(): Lang {
  const brand = useBranding();
  const userLang = useUserLang();
  useSyncExternalStore(subscribeCatalogs, catalogSnapshot, catalogSnapshot);
  const wanted: Lang = userLang ?? (brand.language === 'tr' ? 'tr' : 'en');
  // DEFAULT_BRANDING (fetch öncesi 'en') bir istek DEĞİL: branding'i
  // export'suz mock'layan testler (kataloglar kurulumda yüklü) ona hiç
  // dokunmaz — koşul yalnız katalog yüklü değilken okunur.
  // v0.10.2416 — with NO catalog loaded (boot import failed) always ask for
  // one, even before branding resolves; loadCatalog dedups in-flight loads.
  const request = catalogs[wanted] === null &&
    (userLang !== null || brand !== DEFAULT_BRANDING || (!catalogs.tr && !catalogs.en));
  useEffect(() => {
    if (request) void loadCatalog(wanted);
  }, [request, wanted]);
  // v0.10.948 — hook DIŞI metin üreticileri (aiSubjectTitle) için son
  // çözülen dil. İdempotent önbellek: aynı girdi
  // aynı değeri yazar, render saflığını bozmaz.
  lastResolvedLang = wanted;
  return effectiveLang(wanted);
}

// currentLang — v0.10.948: hook kullanamayan saf yardımcıların etkin dili.
// Öncelik useT ile aynı: kullanıcı seçimi (localStorage) → son çözülen
// (marka varsayılanı) → İngilizce; v0.10.2307: katalog yüklü değilse 'tr'.
export function currentLang(): Lang {
  return effectiveLang(readUserLang() ?? lastResolvedLang ?? 'en');
}

// useT returns a translator scoped to the effective language.
// Priority: user-picked (localStorage) → branding default →
// English fallback. Hook so any of those layers changing
// (admin saves new branding → invalidateBranding fires; user
// clicks the picker → USER_LANG_EVENT fires; English catalog
// loads) flows through every consumer without a manual reload.
// useCatalogs — v0.10.2421: re-render on ANY catalog load. Surfaces whose
// text is not in the UI language (CoSRE is fixed Turkish via tCosre) call
// the module-level t() and would otherwise keep the fallback text forever.
export function useCatalogs(): number {
  return useSyncExternalStore(subscribeCatalogs, catalogSnapshot, catalogSnapshot);
}

export function useT(): (key: string) => string {
  const lang = useLang();
  return (key: string) => lookup(key, lang);
}

// Direct catalog access for non-component code paths (e.g. an
// imperative toast). Mirrors useT but reads the cached branding
// synchronously.
export function t(key: string, lang: Lang = 'en'): string {
  return lookup(key, lang);
}

// tf — v0.10.2189: {ad} yer tutuculu çeviri. vars.n "1" ise ve dilin
// kataloğunda `${key}_one` varsa o kullanılır (EN tekil; TR'de yok → aynı kalıp).
export function tf(key: string, lang: Lang, vars: Record<string, string | number> = {}): string {
  const one = `${key}_one`;
  const k = vars.n != null && String(vars.n) === '1' && (lang === 'tr' ? catalogs.tr?.[one] : catalogs.en?.[one] ?? EN_EAGER[one]) != null ? one : key;
  return t(k, lang).replace(/\{(\w+)\}/g, (m, name: string) => (name in vars ? String(vars[name]) : m));
}

// fmtPctLang — v0.10.2186/2189: yüzde biçimi dile göre, TR "%46", EN "46%".
export function fmtPctLang(n: number, lang: Lang): string {
  return lang === 'tr' ? `%${n}` : `${n}%`;
}

// agoLang — v0.10.2285: utils.agoTR'nin dile göre eşi (TR çıktısı birebir aynı:
// "az önce" / "N dk önce" / "N sa önce" / "N gün önce").
export function agoLang(unixNs: number, lang: Lang, nowMs = Date.now()): string {
  const s = Math.max(0, (nowMs - unixNs / 1e6) / 1000);
  if (s < 60) return t('ago.now', lang);
  if (s < 3600) return tf('ago.m', lang, { n: Math.round(s / 60) });
  if (s < 86400) return tf('ago.h', lang, { n: Math.round(s / 3600) });
  return tf('ago.d', lang, { n: Math.round(s / 86400) });
}
