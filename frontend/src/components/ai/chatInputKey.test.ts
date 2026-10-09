import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { chatInputSubmitKey, autoGrowTextarea, CHAT_INPUT_MAX_PX } from './chatInputKey';

// v0.10.664 — ai-ui-patterns #5 (tek satırlı input) + #6 (akarken input
// kilitli, köprü sorusu sessizce düşüyor). Saf kural + BAĞLANMA pinleri.

describe('chatInputSubmitKey', () => {
  it('Enter → gönder', () => expect(chatInputSubmitKey({ key: 'Enter' })).toBe(true));
  it.each([
    ['Shift', { key: 'Enter', shiftKey: true }],
    ['Alt', { key: 'Enter', altKey: true }],
    ['Ctrl', { key: 'Enter', ctrlKey: true }],
    ['Meta', { key: 'Enter', metaKey: true }],
  ])('%s+Enter → yeni satır (gönderme)', (_n, e) => expect(chatInputSubmitKey(e)).toBe(false));
  it('IME birleştirme sırasında Enter gönderMEZ', () => {
    expect(chatInputSubmitKey({ key: 'Enter', isComposing: true })).toBe(false);
    expect(chatInputSubmitKey({ key: 'Enter', nativeEvent: { isComposing: true } })).toBe(false);
  });
  it('başka tuşlar → gönderme', () => expect(chatInputSubmitKey({ key: 'a' })).toBe(false));
});

describe('autoGrowTextarea', () => {
  it('içerik kadar büyür, tavanda durur', () => {
    const el = { style: { height: '' }, scrollHeight: 54 };
    autoGrowTextarea(el);
    expect(el.style.height).toBe('54px');
    const big = { style: { height: '' }, scrollHeight: 900 };
    autoGrowTextarea(big);
    expect(big.style.height).toBe(`${CHAT_INPUT_MAX_PX}px`);
  });
});

describe('BAĞLANMA', () => {
  const src = (f: string) => readFileSync(new URL(f, import.meta.url), 'utf8');
  // v0.10.1145 — iki yüzey textarea'yı artık TEK composer'dan (ChatComposer) alır;
  // pin oraya taşındı: composer <textarea> + chatInputSubmitKey, yüzeyler onu kullanır
  // ve composer'a `busy` ile kilit geçirmez.
  it('iki yüzey de ChatComposer (<textarea> + chatInputSubmitKey) kullanır, input akarken KİLİTLİ DEĞİL', () => {
    const c = src('./ChatComposer.tsx');
    expect(c).toContain('<textarea');
    expect(c).toContain('chatInputSubmitKey(e)');
    expect(c).not.toMatch(/<textarea[\s\S]{0,600}disabled=/);
    for (const f of ['../CopilotChat.tsx', './AIDrawerBody.tsx']) {
      const s = src(f);
      expect(s, f).toContain('<ChatComposer');
      expect(s, f).not.toMatch(/<ChatComposer[\s\S]{0,400}disabled=\{busy\}/);
    }
  });
  it('useChatThread: akarken gelen soru DÜŞMEZ — durdur ve kuyruğa al, finally gönderir', () => {
    const hook = src('./useChatThread.ts');
    // Yalnız send'in gövdesi: retry'nin `busyRef.current` muhafızı meşru (boşta çalışır).
    const sendBody = hook.slice(hook.indexOf('const send = useCallback('), hook.indexOf('const retry = useCallback('));
    expect(sendBody).not.toMatch(/^\s*if \(!q \|\| busyRef\.current\) return;/m);
    expect(sendBody).toMatch(/^\s*if \(busyRef\.current\) \{/m);
    expect(sendBody).toContain('queuedRef.current = { q, so };'); // v0.10.1138 — kapsamıyla kuyruğa
    // send'in finally'si (dosyada başka finally blokları da var) — copilotChat çağrısından sonraki ilk.
    const fin = hook.indexOf('} finally {', hook.indexOf('await api.copilotChat('));
    expect(hook.slice(fin, fin + 900)).toContain('queuedRef.current');
  });
});
