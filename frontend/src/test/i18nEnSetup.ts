// v0.10.2307 — vitest kurulumu: İngilizce katalog üretimde tembel (lib/i18n.ts
// loadEnCatalog); testler İngilizce çıktıyı bugünkü gibi eşzamanlı görsün diye
// her test dosyasında testlerden önce kaydedilir. beforeAll + dinamik import:
// kurulum dosyası i18n'i STATİK import etseydi modül, test dosyasının
// vi.mock('@/lib/branding') kaydından önce gerçek branding ile önbelleğe
// girerdi; beforeAll test dosyası toplandıktan sonra koşar, aynı (mock'lu)
// örneği alır. Uygulama kodu i18n.en'i statik import etmez (ilk yük).
import { beforeAll } from 'vitest';

beforeAll(async () => {
  const [{ EN }, i18n] = await Promise.all([import('../lib/i18n.en'), import('../lib/i18n')]);
  // vi.mock('@/lib/i18n') ile kısmi mock'lanan dosyada fonksiyon olmayabilir.
  if (typeof i18n.registerEnCatalog === 'function') i18n.registerEnCatalog(EN);
});
