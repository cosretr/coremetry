import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.711 — Traces Pager'ı: kesin son sayfa yoksa "Last ⇥" sırayı tersine
// çevirip sayfa 0'a döner; ters sıradayken "⇤ First". Kaynak pini.
const src = readFileSync(resolve(__dirname, './Traces.tsx'), 'utf8');

describe('Traces Pager sona git', () => {
  it('onEnd sırayı çevirir ve sayfayı sıfırlar; etiket sıraya göre', () => {
    expect(src).toContain('onEnd={() => {');
    expect(src).toContain("endLabel={order === 'desc' ? 'Last ⇥' : '⇤ First'}");
    expect(src).toContain('setPage(0);');
    // v0.10.827 — yön çevirme SAF çekirdeğe taşındı (lib/traceReach.ts).
    expect(src).toContain('reverseEndSort(sort, order)');
    // lastReachablePage hâlâ verilir: kesin+ulaşılabilir son sayfa varsa o kazanır.
    expect(src).toContain('lastReachablePage={lastReachablePage(countRes?.value');
  });

  // v0.10.827 (operatör-bildirimli): "Last'a basınca gösterge hâlâ 1 diyor".
  //
  // Tık ile göstergenin okuduğu kaynak AYNI turda değişmek zorunda. Öncesinde
  // tık yalnız `dt.setSort`u yazıyordu ve `order`a ancak bir efekt üzerinden
  // ulaşıyordu; o efektin `if (!server) return;` dalı tanımadığı bir kolon
  // kimliğinde tıkı sessizce yutuyordu. Kimliği üreten ifade de tam o dalı
  // besliyordu: `dt.sort.id ?? 'startTime'` — 'startTime' kolon kimliği değil.
  it('onEnd order/sort state\'ini DOĞRUDAN yazar (etiket efekte asılı değil)', () => {
    const onEnd = src.slice(src.indexOf('onEnd={() => {'));
    const body = onEnd.slice(0, onEnd.indexOf('}}'));
    expect(body).toContain('setOrder(next.dir)');
    expect(body).toContain('setSort(next.id)');
    expect(body).toContain('dt.setSort(next)');
  });

  // Kapı KENDİ metnini ısırmasın (şerhler kelimeyi zaten geçiriyor) ve
  // KOMŞUSUNU da ısırmasın: AggregateTable'ın `dt.sort.id ?? 'count'`u
  // meşru bir GÖSTERİM yedeği, sunucuya kimlik üretmiyor. Aranan şey
  // dolayısıyla onEnd GÖVDESİNDEKİ eski ifade.
  it("çevirici efektin tanımadığı 'startTime' kimliği artık ÜRETİLMİYOR", () => {
    const onEnd = src.slice(src.indexOf('onEnd={() => {'));
    const body = onEnd.slice(0, onEnd.indexOf('}}')).replace(/^\s*\/\/.*$/gm, '');
    expect(body).not.toMatch(/dt\.sort\.id\s*\?\?/);
    expect(body).not.toMatch(/['"]startTime['"]/);
  });
});
