import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.9.659 — operatör: "Problems sayfasında P1 P2 exceptions default
// listelensin."
//
// Bu bir KARAR TERSİ: v0.9.487'de operatör "defaultta sadece P1'ler
// gözüksün" demişti. Kararın arkasındaki olay kayıtlı — tek servisten 12
// dakikada 11.260 olay üreten bir exception P2 görünüyordu ve varsayılan
// görünümde HİÇ çıkmıyordu (v0.9.627).

const src = readFileSync(resolve(__dirname, './Inbox.tsx'), 'utf8');

// arr — `const NAME[: type] = ['a','b']` dizisini okur.
//
// Tip açıklaması TUZAK: `const KIND_DEFAULT: readonly InboxKind[] = [...]`
// içinde ilk `[` tipe ait. Ve bulunamayınca indexOf -1 döndürüp dosyanın
// İLK dizisini okutuyordu — sessizce makul görünen yanlış bir cevap,
// yani testin en tehlikeli hâli. Artık regex `= [` sonrasını alıyor ve
// eşleşme yoksa PATLIYOR.
function arr(name: string): string[] {
  const m = new RegExp(`const ${name}[^=]*=\\s*\\[([^\\]]*)\\]`).exec(src);
  if (!m) throw new Error(`${name} bulunamadı — sabit yeniden adlandırılmış olabilir`);
  return m[1].split(',').map(s => s.trim().replace(/['"]/g, '')).filter(Boolean);
}

describe('test yardımcısı', () => {
  it('eşleşme yoksa PATLIYOR, sessizce yanlış dizi okumuyor', () => {
    expect(() => arr('YOK_BOYLE_BIR_SABIT')).toThrow();
  });
});

// v0.10.1014 — operatör: "Problems sekmesinde bütün hepsi gelsin, hangisi
// gerçek problem hangisi değil zamanla öğretelim." Üç önceki kararın TERSİ
// (v0.9.487 yalnız P1 → v0.9.659 P1+P2; v0.9.328 yalnız exception; v0.9.443
// HTTP hataları kapalı): gürültü varsayılan süzgeçle gizlenmez, operatör
// işaretleyerek öğretir. Bu testler yeni varsayılanı çiviliyor — eskiye dönüş
// yine operatör kararı ister.
describe('Problems varsayılan görünümü', () => {
  // v0.10.1081 — operatör: "Problems sayfasında sadece P1'ler gözüksün ve
  // first seen'e göre sıralı olsun". v0.10.1014'ün öncelik yarısının tersi;
  // tür varsayılanı HER ŞEY kalır. İstek düzeyi Inbox.rowOpen.test.tsx'te.
  it('öncelik varsayılanı: yalnız P1', () => {
    expect(arr('PRIO_DEFAULT')).toEqual(['P1']);
    expect(arr('PRIO_ALL')).toEqual(['P1', 'P2', 'P3']);
  });

  it('sıralama varsayılanı: ilk görülme, en yeni önce; kişisel kayıt okunmaz', () => {
    expect(src).toContain("const SORT_DEFAULT = { id: 'firstSeen', dir: 'desc' as const };");
    expect(src).toContain('initialSort: SORT_DEFAULT,');
    expect(src).toContain('persistSort: false,');
  });

  it('tür varsayılanı: hepsi (problem, exception, HTTP hatası, anomali, olay)', () => {
    expect(arr('KIND_DEFAULT')).toEqual(['problem', 'exception', 'httperror', 'anomaly', 'incident']);
    expect(arr('KIND_DEFAULT')).toEqual(arr('KIND_ALL'));
  });

  // Ekrandaki açıklama sabit "P1" yazıyordu; varsayılan değişince YALAN
  // söylerdi. Artık sabitten türüyor.
  it('ekrandaki açıklama sabitten türüyor, elle yazılmıyor', () => {
    expect(src).toContain("PRIO_DEFAULT.join(' + ')");
    expect(src).not.toContain('Default view: <b>P1</b>');
  });
});
