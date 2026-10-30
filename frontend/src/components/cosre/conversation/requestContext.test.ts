// requestContext.test — v0.10.1650 (CoSRE v2 Faz 2b B2). v2 `context` kuralları
// v1 useChatThread.send ile aynı: pencere ezmesi, @-kapsam (trace bağlamında
// düşer, /komut kalır), Derin / Kod / kod kilidi, profil, açıklama niyeti.
// Uçtan uca v1 ↔ v2 gövde eşitliği CosreChatV2.test'te (gerçek iki istek).
import { describe, it, expect } from 'vitest';
import { cosreRequestContext } from './requestContext';
import type { ChatRequestContext } from '@/components/ai/chatContextScope';

const tz = (c: Record<string, unknown> | undefined) => {
  const { tz: _a, tzOffsetMin: _b, ...rest } = c ?? {};
  return rest;
};
const screen: ChatRequestContext = { service: 'orders-api', operation: '', rangeS: 3600, trace: '', env: 'prod', page: { page: 'service', path: '/service' } };
const sel = { profile: '', deep: false, code: false };

describe('cosreRequestContext', () => {
  it.each<[string, Parameters<typeof cosreRequestContext>, Record<string, unknown>]>([
    ['ekran kapsamı', [screen, sel, 'neden yavaş', undefined, false],
      { service: 'orders-api', rangeS: 3600, env: 'prod', page: screen.page }],
    ['profil + Derin + Kod', [screen, { profile: 'fast', deep: true, code: true }, 'q', undefined, false],
      { service: 'orders-api', rangeS: 3600, env: 'prod', profile: 'fast', page: screen.page, deep: true, code: true }],
    ['kod kilidi: hap kapalıyken de kod turu + ipucu', [screen, sel, 'q', undefined, true],
      { service: 'orders-api', rangeS: 3600, env: 'prod', page: screen.page, code: true, codeLocked: true }],
    ['sabit pencere ezer', [screen, sel, 'q', { window: { rangeS: 600, toMs: 1_700_000_000_000 } }, false],
      { service: 'orders-api', rangeS: 600, env: 'prod', toMs: 1_700_000_000_000, page: screen.page }],
    ['@-kapsam + /komut', [screen, sel, '/trace @orders-api son hatalar', { chosen: ['orders-api'] }, false],
      { service: 'orders-api', rangeS: 3600, env: 'prod', page: screen.page, scope: { services: ['orders-api'] }, command: 'trace' }],
    ['trace bağlamında @-kapsam düşer', [{ ...screen, trace: 'abc' }, sel, '@orders-api neden', { chosen: ['orders-api'] }, false],
      { service: 'orders-api', rangeS: 3600, trace: 'abc', env: 'prod', page: screen.page }],
    ['açıklama niyeti', [screen, sel, "Bu trace'i açıkla", { explainAsk: { kind: 'trace', id: 'abc', src: 'chip' } }, false],
      { service: 'orders-api', rangeS: 3600, env: 'prod', page: screen.page, explainAsk: { kind: 'trace', id: 'abc', src: 'chip' } }],
  ])('%s', (_n, args, want) => {
    expect(tz(cosreRequestContext(...args))).toEqual(want);
  });
});
