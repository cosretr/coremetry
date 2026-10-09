import { describe, it, expect } from 'vitest';
import { completionQuery, applyCompletion, moveHighlight, staticItems, nameItems } from './chatCompletion';

// v0.10.687 — CoSRE girişte servis adı tamamlama (D4, cosre-chat-parity planı;
// ai-ui-patterns #7). SÖZLEŞME (saf):
//   1. Tetik: imleçteki token '@' ile başlıyorsa 1+ karakterde AÇIK tetik;
//      öneksiz token ≥3 karakter ve '-' içeriyorsa (servis adı biçimi)
//      OTOMATİK tetik; aksi null (sıradan sözcükler sunucuya gitmez).
//   2. applyCompletion token'ı kanonik adla değiştirir + boşluk; imleç adın
//      sonrasına.
//   3. moveHighlight sarar (son → ilk, ilk → son).
// v0.10.1138 — @ ve / kapsamı: '@' tek başına anma türleri, '@env:'/'@team:'
// sunucu adayları, mesaj başında '/' komut menüsü; açık '@' ile seçilen servis
// '@ad' olarak kalır (kapsam anması), otomatik tetikte çıplak ad.
describe('completionQuery', () => {
  it("'@' öneki açık tetik", () => {
    expect(completionQuery('hata var @sh', 12)).toEqual({ query: 'sh', start: 9, end: 12, explicit: true, kind: 'service' });
    // v0.10.1138 — yalnız '@': anma türleri menüsü (sunucu sorgusu yok)
    expect(completionQuery('@', 1)).toEqual({ query: '', start: 0, end: 1, explicit: true, kind: 'mention' });
  });
  it('öneksiz: ≥3 karakter ve tire → otomatik; sıradan sözcük null', () => {
    expect(completionQuery('shop-pay servisinde', 8)).toEqual({ query: 'shop-pay', start: 0, end: 8, explicit: false, kind: 'service' });
    expect(completionQuery('neden yavaş', 11)).toBeNull();
    expect(completionQuery('ab-', 3)).toEqual({ query: 'ab-', start: 0, end: 3, explicit: false, kind: 'service' });
    expect(completionQuery('a-', 2)).toBeNull();
  });
  it('imleç token ortasındaysa yalnız imlece kadar olan kısım', () => {
    expect(completionQuery('@shop-payment x', 5)).toEqual({ query: 'shop', start: 0, end: 5, explicit: true, kind: 'service' });
  });
  it('v0.10.1138 — @env: / @team: / @trace: / komut', () => {
    expect(completionQuery('hatalar @env:pr', 15)).toMatchObject({ query: 'pr', kind: 'env', start: 8 });
    expect(completionQuery('@team:', 6)).toMatchObject({ query: '', kind: 'team' });
    expect(completionQuery('@trace:abc', 10)).toBeNull();
    expect(completionQuery('/r', 2)).toMatchObject({ query: 'r', kind: 'command', start: 0 });
    // yalnız mesaj başında; yol komut değildir
    expect(completionQuery('neden /r', 8)).toBeNull();
    expect(completionQuery('/api/x', 6)).toBeNull();
  });
});

describe('staticItems / nameItems', () => {
  it('komut menüsü önekle süzülür', () => {
    expect(staticItems(completionQuery('/', 1)).map(i => i.insert)).toEqual(['/wiki ', '/trace ', '/rca ', '/logs ', '/help ']);
    expect(staticItems(completionQuery('/r', 2)).map(i => i.insert)).toEqual(['/rca ']);
  });
  it("'@' anma türlerini, '@t' eşleşen türleri önerir", () => {
    expect(staticItems(completionQuery('@', 1)).map(i => i.insert)).toEqual(['@trace:', '@problem:', '@env:', '@team:', '@wiki ']);
    expect(staticItems(completionQuery('@t', 2)).map(i => i.insert)).toEqual(['@trace:', '@team:']);
    expect(staticItems(completionQuery('svc-or', 6))).toEqual([]); // otomatik tetikte tür yok
  });
  it('ad satırları türüne göre yazılır; açık servis anması kapsam taşır', () => {
    expect(nameItems(completionQuery('@sv', 3), ['svc-orders'])).toEqual([{ label: 'svc-orders', insert: '@svc-orders ', hint: 'servis', service: 'svc-orders' }]);
    expect(nameItems(completionQuery('svc-o', 5), ['svc-orders'])).toEqual([{ label: 'svc-orders', insert: 'svc-orders ' }]);
    expect(nameItems(completionQuery('@env:p', 6), ['prod'])[0].insert).toBe('@env:prod ');
    expect(nameItems(completionQuery('@team:', 6), ['platform'])[0].insert).toBe('@team:platform ');
  });
});

describe('applyCompletion', () => {
  it('token kanonik adla değişir, boşluk eklenir, imleç sonda', () => {
    const q = completionQuery('hata var @sh', 12)!;
    expect(applyCompletion('hata var @sh', q, 'shop-payment')).toEqual({ text: 'hata var shop-payment ', caret: 22 });
  });
  it('metnin ortasında değişim sonrasını korur', () => {
    const q = completionQuery('@shop-payment x', 5)!;
    expect(applyCompletion('@shop-payment x', q, 'shop-gateway')).toEqual({ text: 'shop-gateway -payment x', caret: 13 });
  });
  it('v0.10.1138 — öneri satırı: anma korunur, tür seçiminde boşluk yok', () => {
    const q = completionQuery('hata var @sh', 12)!;
    expect(applyCompletion('hata var @sh', q, { label: 'shop', insert: '@shop-payment ', service: 'shop-payment' }))
      .toEqual({ text: 'hata var @shop-payment ', caret: 23 });
    const k = completionQuery('@e', 2)!;
    expect(applyCompletion('@e', k, { label: '@env:', insert: '@env:' })).toEqual({ text: '@env:', caret: 5 });
  });
});

describe('moveHighlight', () => {
  it('sarar', () => {
    expect(moveHighlight(0, -1, 3)).toBe(2);
    expect(moveHighlight(2, 1, 3)).toBe(0);
    expect(moveHighlight(1, 1, 3)).toBe(2);
    expect(moveHighlight(0, 1, 0)).toBe(0);
  });
});
