import { describe, it, expect } from 'vitest';
import { commandOf, hasDrawerContext, parseChatInput, pickedServices, removeToken, scopeChips, scopeForRequest } from './chatScope';
import { answerVersion, canRegenerate, regenerateBase, MAX_ANSWER_VERSIONS } from './chatRegenerate';
import type { ChatTurn } from '@/lib/types';

// v0.10.1138 — composer @-anma / /-komut çözümleyicisi ve yeniden üretimin saf
// yarısı. Sunucu yarısı internal/api/chat_scope.go (komut listesi aynı).
const TID = '0af7651916cd43dd8448eb211c80319c';

describe('parseChatInput', () => {
  it('komut yalnız mesaj başında ve bilinen komutsa', () => {
    expect(commandOf('/rca svc-orders')).toBe('rca');
    expect(commandOf('  /WIKI deploy')).toBe('wiki');
    expect(commandOf('/help')).toBe('help');
    expect(commandOf('/api/orders hataları')).toBeUndefined();
    expect(commandOf('/deploy x')).toBeUndefined();
    expect(commandOf('neden /rca')).toBeUndefined();
  });
  it('tür anmaları + servis biçimi → yapısal kapsam', () => {
    expect(parseChatInput(`@trace:${TID.toUpperCase()} @problem:p-42 @env:prod @team:platform @wiki @svc-orders neden?`)).toEqual({
      scope: { trace: TID, problem: 'p-42', env: 'prod', team: 'platform', wiki: true, services: ['svc-orders'], shaped: ['svc-orders'] },
    });
  });
  it('servis: seçilen ya da servis biçimli; "@Override" / e-posta kapsam olmaz', () => {
    expect(parseChatInput('@checkout hataları')).toEqual({});
    expect(parseChatInput('@checkout hataları', ['checkout'])).toEqual({ scope: { services: ['checkout'] } });
    expect(parseChatInput('Caused by @Override at x @deprecated')).toEqual({});
    expect(parseChatInput('dev@example.test hataları')).toEqual({});
    expect(parseChatInput('@svc-orders, @svc-orders ve @svc-payments.')).toEqual({ scope: { services: ['svc-orders', 'svc-payments'], shaped: ['svc-orders', 'svc-payments'] } });
  });
  it('geçersiz trace kimliği ve boş değer düşer', () => {
    expect(parseChatInput('@trace:xyz @env: soru')).toEqual({});
  });
  it('serbest metin → boş (gövde bayt bayt eski)', () => {
    expect(parseChatInput('svc-orders neden yavaş?')).toEqual({});
    expect(parseChatInput("/api/x hatalı trace'lerini getir")).toEqual({});
  });
  it('komut + kapsam birlikte', () => {
    expect(parseChatInput('/trace @svc-orders ORDER_TIMEOUT')).toEqual({ command: 'trace', scope: { services: ['svc-orders'], shaped: ['svc-orders'] } });
  });
  // v0.10.1139 — köken: seçilmemiş biçim-eşleşmesi işaretlenir (sunucu bilinmiyorsa sessiz düşürür).
  it('biçim-eşleşmesi "shaped" işaretlenir; tamamlamadan seçilen işaretlenmez', () => {
    expect(parseChatInput('@john.doe ve @spring-boot ne dedi')).toEqual({
      scope: { services: ['john.doe', 'spring-boot'], shaped: ['john.doe', 'spring-boot'] },
    });
    const p = parseChatInput('@svc-orders ile @john.doe', ['svc-orders']);
    expect(p).toEqual({ scope: { services: ['svc-orders', 'john.doe'], shaped: ['john.doe'] } });
    expect(pickedServices(p.scope)).toEqual(['svc-orders']);
    expect(pickedServices(undefined)).toEqual([]);
  });
  it('çekmece bağlamında @-kapsamı düşer, açık /komut kalır', () => {
    const p = parseChatInput('/rca @svc-orders @env:prod');
    expect(hasDrawerContext({})).toBe(false);
    expect(hasDrawerContext({ explain: ' ', page: {} })).toBe(false);
    for (const c of [{ explain: 'x' }, { subject: 'trace:abc' }, { trace: TID }, { page: { traceId: TID } }]) {
      expect(hasDrawerContext(c)).toBe(true);
    }
    expect(scopeForRequest(p, true)).toEqual({ command: 'rca' });
    expect(scopeForRequest(parseChatInput('@svc-orders logda ne var'), true)).toEqual({});
    expect(scopeForRequest(p, false)).toBe(p);
  });
});

describe('scopeChips / removeToken', () => {
  it('her kapsam boyutu bir çip; ✕ token\'ı metinden çıkarır', () => {
    const p = parseChatInput('/rca @svc-orders @env:prod neden');
    const chips = scopeChips(p);
    expect(chips.map(c => c.label)).toEqual(['/rca', 'servis · svc-orders', 'env · prod']);
    expect(removeToken('/rca @svc-orders @env:prod neden', '@env:prod')).toBe('/rca @svc-orders neden');
    expect(removeToken('/rca @svc-orders neden', '/rca')).toBe('@svc-orders neden');
    expect(removeToken('@svc-orders neden', '@svc-orders')).toBe('neden');
  });
});

const t = (role: ChatTurn['role'], text: string, extra: Partial<ChatTurn> = {}): ChatTurn => ({ role, text, ...extra });

describe('chatRegenerate', () => {
  it('yalnız tamamlanmış son cevapta', () => {
    expect(canRegenerate([t('user', 'q'), t('assistant', 'a')])).toBe(true);
    expect(canRegenerate([t('user', 'q'), t('assistant', '', { pending: true })])).toBe(false);
    expect(canRegenerate([t('user', 'q'), t('assistant', '', { error: 'x' })])).toBe(false);
    expect(canRegenerate([t('assistant', 'merhaba')])).toBe(false);
  });
  it('taban: son çift düşer, aynı soru + kapsam, önceki cevaplar tavanlı', () => {
    const user = t('user', '/rca @svc-orders', { command: 'rca', scope: { services: ['svc-orders'] } });
    const prev = Array.from({ length: MAX_ANSWER_VERSIONS }, (_, i) => t('assistant', `eski ${i}`, { exchangeId: `x${i}` }));
    const r = regenerateBase([t('user', 'ilk'), t('assistant', 'ilk cevap'), user, t('assistant', 'son', { exchangeId: 'xs', alternatives: prev })])!;
    expect(r.base.map(x => x.text)).toEqual(['ilk', 'ilk cevap']);
    expect(r.question).toBe('/rca @svc-orders');
    expect(r.parsed).toEqual({ command: 'rca', scope: { services: ['svc-orders'] } });
    expect(r.alternatives).toHaveLength(MAX_ANSWER_VERSIONS);
    expect(r.alternatives[r.alternatives.length - 1].exchangeId).toBe('xs');
    expect(r.alternatives.every(a => !a.alternatives)).toBe(true);
  });
  // v0.10.1139 — yeniden yüklenen konuşma turları {role,text}: parsed undefined → send yeniden çözer.
  it('kapsamsız/komutsuz kullanıcı turunda parsed undefined', () => {
    const r = regenerateBase([t('user', '/rca @svc-orders'), t('assistant', 'cevap')])!;
    expect(r.parsed).toBeUndefined();
    expect(r.question).toBe('/rca @svc-orders');
  });
  it('sürüm görünümü: varsayılan en yeni; seçilen eski cevap kendi exchangeId\'siyle', () => {
    const last = t('assistant', 'yeni', { exchangeId: 'x2', alternatives: [t('assistant', 'eski', { exchangeId: 'x1' })] });
    expect(answerVersion(last, null)).toMatchObject({ index: 1, total: 2, view: { text: 'yeni' } });
    expect(answerVersion(last, 0)).toMatchObject({ index: 0, total: 2, view: { text: 'eski', exchangeId: 'x1' } });
    expect(answerVersion(last, 9).index).toBe(1);
  });
});
