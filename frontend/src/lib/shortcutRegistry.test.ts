// shortcutRegistry — Dalga 6 kapısı (v0.9.926)
//
// Ne çiviliyor: kısayol kayıt defterinin YIĞIN olduğu ve bir kaydın
// unmount'ının ALTTAKİNİ yok etmediği.
//
// Bulunan hata: defter `Map<string, Shortcut>`ti ve `set(sc.keys, sc)`
// ikinci kaydı birincinin üzerine yazıyordu. İki nav'lı tablo aynı anda
// mount olduğunda j/k'nın sahibi mount sırasına kalıyordu — kabul
// edilebilir bir varsayılan. Asıl hata unmount'taydı: kaldırma koruması
// ("yalnız biz kaydettiysek sil") tepedeki için TUTUYOR, dolayısıyla
// ikinci tablo kapandığında binding TAMAMEN siliniyordu ve geriye kalan
// tablonun klavye gezinmesi ÖLÜYORDU. Bir daha da geri gelmiyordu.
//
// Ekranda hiçbir şey "bozulmuyor" — sadece tuşlar çalışmıyor. Hiçbir
// kapı bunu göremezdi; bu yüzden bu kapı var.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// Yorumları BOŞALT: `keyboard.ts`in KENDİ yorumu eski hatalı satırı
// (`registry.set(sc.keys, sc)`) tarihsel kayıt olarak alıntılıyor. Ham
// metinde arama yapan bir kapı o alıntıyı canlı kod sanardı — depoda
// yedi kez ısıran tuzağın aynadaki hâli.
function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '))
    .replace(/^\s*\/\/.*$/gm, '');
}

const SRC = stripComments(readFileSync(resolve(__dirname, 'keyboard.ts'), 'utf8'));
const NAV = stripComments(readFileSync(resolve(__dirname, 'useTableNav.ts'), 'utf8'));
const DT = stripComments(readFileSync(resolve(__dirname, '..', 'components', 'ui', 'DataTable', 'DataTable.tsx'), 'utf8'));

describe('kısayol defteri yığın', () => {
  it('defter tuş başına DİZİ tutuyor', () => {
    expect(/type Registry = Map<string, Shortcut\[\]>/.test(SRC)).toBe(true);
  });

  it('kayıt üzerine YAZMIYOR, yığına ekliyor', () => {
    // Eski hata tam olarak buydu: `registry.set(sc.keys, sc)`.
    const OVERWRITE = 'registry.set(sc.keys, ' + 'sc)';
    expect(SRC.includes(OVERWRITE), 'kayıt hâlâ üzerine yazıyor').toBe(false);
    expect(SRC).toContain('stack.push(sc)');
  });

  it('kaldırma KENDİ kaydını çıkarıyor, tuşu silmiyor', () => {
    expect(SRC).toContain('lastIndexOf(sc)');
    expect(SRC).toContain('splice(i, 1)');
    // Yığın boşalmadıkça tuş defterden düşmemeli.
    expect(SRC).toContain('if (stack.length === 0) registry.delete(sc.keys)');
  });

  it('gönderim yığının TEPESİNE gidiyor', () => {
    expect(SRC).toContain('function owner(');
    expect(SRC).toContain('owner(combo)');
  });

  it('yardım ekranı yalnız SAHİP binding\'leri listeliyor', () => {
    // Gölgelenmiş kayıtlar listelenirse operatöre çalışmayan bir kısayol
    // vaat edilmiş olur.
    //
    // v0.9.928 — "sahip" tanımı yığının tepesi olmaktan çıkıp ARBİTRAJIN
    // cevabı oldu (son etkileşim). Yardım ekranı da aynı fonksiyondan
    // geçmeli; `stack[stack.length - 1]` diye sabit kalsaydı iki tablolu
    // bir sayfada j/k'yı YANLIŞ tabloya atfederdi. Kapı artık dispatch ile
    // yardım ekranının AYNI kaynağı kullandığını çiviliyor.
    const i = SRC.indexOf('export function listShortcuts');
    expect(SRC.slice(i)).toContain('pickOwner(stack, active)');
  });
});

describe('oto-kaydırma tabloya KAPSAMLI', () => {
  it('sorgu data-table-id ile daraltılıyor', () => {
    // Eskiden `document.querySelector('[data-row-idx=N]')` belgedeki İLK
    // eşleşeni buluyordu: iki tablolu sayfada j/k bir tabloda seçim
    // yaparken kaydırma DİĞERİNDE oluyordu.
    expect(NAV).toContain('data-table-id');
    expect(NAV).toContain('options.pageId');
  });

  it('rowProps kimliği satıra basıyor', () => {
    expect(DT).toContain("'data-table-id': storageKey");
  });

  it('DataTable pageId\'yi nav\'a GEÇİRİYOR', () => {
    // v0.9.660 sınıfı: prop var, geçiriliyor, ama kullanılmıyorsa
    // yetenek "var görünen yok"tur. Burada zincirin her halkası aranıyor.
    expect(DT).toContain('pageId: storageKey');
  });
});

// v0.10.1857 (Log Search Faz 2 dilim 2.6) — /logs f / e / t kayıtları. Yardım
// ekranı listShortcuts'tan okur; kısayollar yalnız Logs bağlıyken kayıtlı
// (useShortcuts sayfa ömrü — Logs.keys.test.tsx davranışı çiviler). Burada
// kaynak pini: sayfa üç tuşu 'Logs' grubunda etiketle kaydediyor ve küresel
// kısayollar (GlobalShortcuts) bu tuşları sahiplenmiyor.
describe('/logs f / e / t kayıtları', () => {
  const LOGS = stripComments(readFileSync(resolve(__dirname, '..', 'pages', 'Logs.tsx'), 'utf8'));
  const GLOBAL = stripComments(readFileSync(resolve(__dirname, '..', 'components', 'GlobalShortcuts.tsx'), 'utf8'));
  it('Logs sayfası useShortcuts ile f, e, t kaydediyor (etiketli, Logs grubu)', () => {
    for (const k of ['f', 'e', 't']) {
      expect(LOGS, k).toMatch(new RegExp(`keys: '${k}', label: '[^']+|keys: '${k}', label: "[^"]+`));
    }
    expect((LOGS.match(/group: 'Logs'/g) ?? []).length).toBe(3);
    expect(LOGS).toContain('useShortcuts([');
  });
  it('küresel kısayollar f / e / t bağlamıyor (yalnız Logs kapsamı)', () => {
    for (const k of ['f', 'e', 't']) expect(GLOBAL.includes(`keys: '${k}'`), k).toBe(false);
  });
});

