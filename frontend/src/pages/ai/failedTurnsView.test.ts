import { describe, expect, it } from 'vitest';
import { emptyPromoteForm, failedSummary, parseAnon, parseList, promoteBody, reasonsText, turnHref } from './failedTurnsView';

const turn = { exchangeId: 'ex-1', question: 'checkout kafka lag var mı', reasons: ['reask', 'verify_mismatch'], tier: 'guided' };

describe('failedTurnsView', () => {
  it('gerekçe etiketleri sırayı korur, bilinmeyen kod aynen', () => {
    expect(reasonsText(['reask', 'verify_mismatch', 'x'])).toBe('Yeniden soruldu, Doğrulayıcı uyuşmazlığı, x');
  });

  it('tur bağlantısı Sorular çekmecesini pencere ve soru süzgeciyle açar', () => {
    const href = new URL(turnHref(turn, 30), 'http://x');
    expect(href.pathname).toBe('/ai');
    expect(href.searchParams.get('range')).toBe('30d');
    expect(href.searchParams.get('exchange')).toBe('ex-1');
    expect(href.searchParams.get('xq')).toBe('checkout kafka lag var mı');
  });

  it('liste ve anonimleştirme ayrıştırma', () => {
    expect(parseList('a, b\n a \n\n c')).toEqual(['a', 'b', 'c']);
    expect(parseAnon('pay-api => checkout\nbad\nx = y')).toEqual([{ from: 'pay-api', to: 'checkout' }, { from: 'x', to: 'y' }]);
  });

  it('terfi gövdesi: anonimleştirme yalnız işaretliyse, boş alanlar gövdeye girmez', () => {
    const f = { ...emptyPromoteForm(turn), expectRoute: 'messaging_health', mustContain: 'lag', anonPairs: 'checkout => svc' };
    expect(promoteBody(turn, f)).toEqual({
      exchangeId: 'ex-1', reasons: ['reask', 'verify_mismatch'], expectTier: 'guided', expectRoute: 'messaging_health', mustContain: ['lag'],
    });
    expect(promoteBody(turn, { ...f, anonymize: true }).anonymize).toEqual([{ from: 'checkout', to: 'svc' }]);
    expect(promoteBody(turn, { ...f, question: 'yeni soru' }).question).toBe('yeni soru');
  });

  it('özet cümlesi', () => {
    expect(failedSummary(0, 0, 7, false)).toBe('Son 7 günde CoSRE turu yok.');
    expect(failedSummary(5, 50, 7, true)).toContain('(%10)');
  });
});
