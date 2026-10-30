// v0.10.1646 (operatör onaylı) — /ai "CoSRE etkileşimleri": pencerenin TÜM
// soruları (sunucu sayfası + arama + kullanıcı/kademe süzgeci + CSV). Önceki
// semptom: LIMIT 200 eski turları gizliyordu. Bu dosya URL durumunun gidiş-
// dönüşünü (replace yazımı yabancı parametreleri korur, süzgeç değişince
// sayfa başa döner) ve istek parametrelerini çiviler.
import { describe, expect, it } from 'vitest';
import {
  EXCHANGE_TIER_OPTIONS, EXCHANGES_PAGE_SIZE, aiExchangesCSVParams, aiExchangesRequestParams,
  exchangesQueryParams, exchangesUrlSig, parseExchangeTier, readExchangesUrlState, writeExchangesUrlState,
} from './exchangeView';

describe('exchanges URL durumu', () => {
  it('boş URL → varsayılanlar', () => {
    expect(readExchangesUrlState(new URLSearchParams())).toEqual({ feedback: '', q: '', user: '', tier: '', page: 0 });
  });

  it('gidiş-dönüş: yazılan her alan geri okunur, yabancı parametre korunur', () => {
    const prev = new URLSearchParams('range=6h&exchange=ex-1&tab=calls');
    const next = writeExchangesUrlState(prev, { feedback: 'down', q: 'neden yavaş', user: 'user-a@example.test', tier: 'explain' });
    const st = readExchangesUrlState(next);
    expect(st).toEqual({ feedback: 'down', q: 'neden yavaş', user: 'user-a@example.test', tier: 'explain', page: 0 });
    expect(next.get('range')).toBe('6h');
    expect(next.get('exchange')).toBe('ex-1');
    expect(next.get('tab')).toBe('calls');
    // prev değişmez (setSearchParams(prev => …) kopya üretmeli).
    expect(prev.get('xq')).toBeNull();
  });

  it('sayfa yazımı süzgeçleri korur; süzgeç yazımı sayfayı sıfırlar', () => {
    const withFilter = writeExchangesUrlState(new URLSearchParams(), { q: 'abc' });
    const paged = writeExchangesUrlState(withFilter, { page: 3 });
    expect(readExchangesUrlState(paged)).toMatchObject({ q: 'abc', page: 3 });
    const refiltered = writeExchangesUrlState(paged, { user: 'u-1' });
    expect(readExchangesUrlState(refiltered)).toMatchObject({ q: 'abc', user: 'u-1', page: 0 });
    expect(refiltered.has('xpage')).toBe(false);
  });

  it('boş değer parametreyi siler; sayfa 0 URL\'e yazılmaz', () => {
    const a = writeExchangesUrlState(new URLSearchParams('xq=a&xuser=b&xtier=loop&fb=up&xpage=2'),
      { q: '', user: '', tier: '', feedback: '', page: 0 });
    expect(a.toString()).toBe('');
  });

  it('bozuk URL değerleri kapalı kümeye düşer', () => {
    const st = readExchangesUrlState(new URLSearchParams('xtier=NOPE&fb=evil&xpage=-4'));
    expect(st.tier).toBe('');
    expect(st.feedback).toBe('');
    expect(st.page).toBe(0);
    expect(readExchangesUrlState(new URLSearchParams('xpage=2.7')).page).toBe(2);
  });

  it('imza yalnız sahip olunan parametrelerden (yabancı yazım içe almayı tetiklemez)', () => {
    const a = new URLSearchParams('xq=abc&range=1h');
    const b = new URLSearchParams('xq=abc&range=24h&exchange=z');
    expect(exchangesUrlSig(a)).toBe(exchangesUrlSig(b));
    expect(exchangesUrlSig(new URLSearchParams('xq=abd'))).not.toBe(exchangesUrlSig(a));
  });
});

describe('kademe süzgeci', () => {
  it('seçenekler sunucunun kademe kümesi (Tümü + bilinenler)', () => {
    expect(EXCHANGE_TIER_OPTIONS[0].value).toBe('');
    for (const t of ['scope', 'explain', 'drawer', 'loop', 'clarify']) {
      expect(EXCHANGE_TIER_OPTIONS.some(o => o.value === t)).toBe(true);
    }
    expect(parseExchangeTier('explain')).toBe('explain');
    expect(parseExchangeTier('x')).toBe('');
  });
});

describe('istek parametreleri', () => {
  it('sayfa → offset; boş süzgeçler gönderilmez', () => {
    const p = exchangesQueryParams({ from: 1, to: 2 }, { feedback: '', q: '', user: '', tier: '', page: 2 });
    expect(p).toEqual({ from: 1, to: 2, limit: EXCHANGES_PAGE_SIZE, offset: 2 * EXCHANGES_PAGE_SIZE, feedback: '', q: '', user: '', tier: '' });
    expect(aiExchangesRequestParams(p)).toEqual({ from: 1, to: 2, limit: EXCHANGES_PAGE_SIZE, offset: 2 * EXCHANGES_PAGE_SIZE });
  });
  it('CSV aynı süzgeçleri taşır, sayfa taşımaz', () => {
    const p = exchangesQueryParams({ from: 1, to: 2 }, { feedback: 'down', q: 'a', user: 'u', tier: 'loop', page: 5 });
    expect(aiExchangesCSVParams(p)).toEqual({ from: 1, to: 2, feedback: 'down', q: 'a', user: 'u', tier: 'loop' });
  });
});
