// pickChatImpl.test — v0.10.1647 (CoSRE v2 Faz 2b B1). Bayrak × niyet tablosu.
// B7'de `aiSubject` satırı v2'ye döner (plan §5.2).
import { describe, it, expect } from 'vitest';
import { pickChatImpl, type ChatIntent } from './pickChatImpl';

const INTENTS: ChatIntent[] = ['fab', 'chatParam', 'ask', 'prefill', 'aiSubject'];

describe('pickChatImpl', () => {
  it.each(INTENTS)('bayrak kapalı / yok / bilinmiyor → v1 (%s)', (intent) => {
    expect(pickChatImpl({ cosreV2: false }, intent)).toBe('v1');
    expect(pickChatImpl({}, intent)).toBe('v1');
    expect(pickChatImpl(null, intent)).toBe('v1');
    expect(pickChatImpl(undefined, intent)).toBe('v1');
  });

  it.each([
    ['fab', 'v2'], ['chatParam', 'v2'], ['ask', 'v2'], ['prefill', 'v2'],
    // v0.10.1672 (B7): açıklama turu da v2 (Q1-a geçiş dalı silindi).
    ['aiSubject', 'v2'],
  ] as const)('bayrak açık: %s → %s', (intent, want) => {
    expect(pickChatImpl({ cosreV2: true }, intent)).toBe(want);
  });
});
