// v0.10.2307 / v0.10.2400 — vitest kurulumu: TR ve EN katalogları üretimde
// tembel (lib/i18n.ts loadCatalog; main.tsx ilk çizimden önce bekler).
// Testler ikisini de eşzamanlı görsün — modül düzeyindeki t() sabitleri
// (NOISE_REASONS, ANOMALY_KIND_PLAIN…) dahil — diye kataloglar burada
// globalThis'e bırakılır; lib/i18n.ts yalnız MODE==='test'te, kendi
// değerlendirmesinde okur (üretimde ölü kod). i18n'i burada STATİK import
// etmiyoruz: modül, test dosyasının vi.mock('@/lib/branding') kaydından önce
// gerçek branding ile önbelleğe girerdi. Katalog dosyalarının bağımlılığı yok.
import { EN, EN_EAGER } from '../lib/i18n.en';
import { TR } from '../lib/i18n.tr';

(globalThis as { __cmI18nPreload?: unknown }).__cmI18nPreload = { tr: TR, en: EN, eager: EN_EAGER };
