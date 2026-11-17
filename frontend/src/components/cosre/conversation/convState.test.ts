// convState.test.ts — v0.10.2325: konuşma bağlamı satırı ve yankı (SAF).
import { describe, it, expect } from 'vitest';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';
import { convStateLine, latestConvState, withStateEcho } from './convState';

const msg = (role: 'user' | 'assistant', parts: unknown[]): CosreUIMessage => ({ id: Math.random().toString(36), role, parts } as unknown as CosreUIMessage);
const st = (data: Record<string, unknown>) => ({ type: 'data-state', id: 'state', data });

describe('convState', () => {
  it('son durum taşıyan asistan mesajı; satır servis · pencere · ortam', () => {
    const ms = [
      msg('assistant', [st({ service: 'orders', updatedAt: 1 })]),
      msg('user', [{ type: 'text', text: 'peki dün?' }]),
      msg('assistant', [st({ service: 'checkout', updatedAt: 2 }), st({ service: 'checkout', env: 'prod', window: { rangeS: 21600, label: 'dün sabah (06:00–12:00)' }, updatedAt: 3 })]),
      msg('assistant', [{ type: 'text', text: 'durumsuz' }]),
    ];
    const s = latestConvState(ms);
    expect(s?.updatedAt).toBe(3);
    expect(convStateLine(s)).toBe('checkout · dün sabah (06:00–12:00) · prod');
  });
  it('durum yoksa satır ve yankı yok', () => {
    expect(convStateLine(latestConvState([msg('assistant', [{ type: 'text', text: 'x' }])]))).toBe('');
    expect(withStateEcho({ a: 1 }, null)).toEqual({ a: 1 });
    expect(withStateEcho(undefined, { service: 'payments', updatedAt: 9 })).toEqual({ state: { service: 'payments', updatedAt: 9 } });
  });
});
