// pageJsdom — sayfa (Logs/Traces/Endpoints…) mount eden testler için jsdom
// eksikleri, MODÜL DEĞERLENDİRMESİNDEN ÖNCE. Yan etkili import; dosyanın
// İLK import'u olmalı:
//   import '@/test/pageJsdom';
// Neden ayrı dosya: sayfa modül ağacı `matchMedia`'yı modül kapsamında
// çağırıyor. Test dosyasının gövdesindeki atama statik import'lardan SONRA
// koşar; sayfa statik import edilince (v0.10.1707 — dinamik import ilk testin
// 5 s bütçesini yiyordu) "matchMedia is not a function" ile toplama düşer.
// Yalnız eksikse tanımlanır; davranış eklemez (eşleşme yok, gözlemci
// hiç tetiklenmez).
const g = globalThis as unknown as Record<string, unknown>;
if (typeof window !== 'undefined' && typeof window.matchMedia !== 'function') {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}
g.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} };
export {};
