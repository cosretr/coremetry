// v0.10.1153 — /ai "CoSRE etkileşimleri" görünüm modeli (SAF).
import { describe, expect, it } from 'vitest';
import type { AIExchange } from '@/lib/types';
import {
  NO_LLM_LABEL, exchangeCallsSummary, exchangeFeedbackLabel, exchangeModelLabel,
  exchangeTierLabel, exchangeTokensLabel, exchangeUsedLLM,
  AI_EXCHANGES_QUERY_FIELDS, aiExchangesQueryKey, aiExchangesRequestParams,
  exchangeConversationHref, exchangeFeedbackCounts, exchangeStatusBadge,
  parseExchangeFeedbackFilter, type AIExchangesQueryParams,
} from './exchangeView';

const base: AIExchange = {
  exchangeId: 'aaaa0000aaaa0000aaaa0000aaaa0000', createdAt: 1, tier: 'scope', question: '/help',
  status: 'ok', durationMs: 3, llmCalls: 0, inputTokens: 0, outputTokens: 0, models: [], calls: [],
};

describe('exchangeView', () => {
  it('LLM\'siz tur "LLM yok" işaretini ve token yerine — gösterir', () => {
    expect(exchangeUsedLLM(base)).toBe(false);
    expect(exchangeModelLabel(base)).toBe(NO_LLM_LABEL);
    expect(exchangeTokensLabel(base)).toBe('—');
    expect(exchangeCallsSummary(base)).toBe(NO_LLM_LABEL);
  });

  it('LLM\'li tur modelleri, profili ve token toplamını gösterir', () => {
    const ex: AIExchange = {
      ...base, tier: 'wiki', llmCalls: 2, inputTokens: 950, outputTokens: 205, models: ['gemma4'], profileId: 'local',
      calls: [
        { id: '1', createdAt: 1, surface: 'wiki-select', provider: 'openai', model: 'gemma4', durationMs: 1, inputTokens: 50, outputTokens: 5, status: 'ok', llm: true },
        { id: '2', createdAt: 2, surface: 'wiki-chat', provider: 'openai', model: 'gemma4', durationMs: 1, inputTokens: 900, outputTokens: 200, status: 'ok', llm: true },
      ],
    };
    expect(exchangeModelLabel(ex)).toBe('gemma4 (local)');
    expect(exchangeTokensLabel(ex)).toBe('950 / 205');
    expect(exchangeCallsSummary(ex)).toBe('2 model çağrısı');
  });

  it('işaret satırları model çağrısı sayılmaz', () => {
    const ex: AIExchange = {
      ...base, tier: 'intent', llmCalls: 1, models: ['qwen3'],
      calls: [
        { id: '1', createdAt: 1, surface: 'chat-intent', provider: 'openai', model: 'qwen3', durationMs: 1, inputTokens: 1, outputTokens: 1, status: 'ok', llm: true },
        { id: '2', createdAt: 2, surface: 'chat-offtopic', provider: 'openai', model: 'qwen3', durationMs: 0, inputTokens: 0, outputTokens: 0, status: 'ok', llm: false },
      ],
    };
    expect(exchangeCallsSummary(ex)).toBe('1 model çağrısı · 1 işaret satırı');
  });

  it('kademe + rota etiketi; bilinmeyen kademe adıyla kalır', () => {
    expect(exchangeTierLabel({ tier: 'guided', route: 'service_health' })).toBe('Guided · service_health');
    expect(exchangeTierLabel({ tier: 'scope' })).toBe('Kapsam / komut');
    expect(exchangeTierLabel({ tier: 'yeni' })).toBe('yeni');
  });

  it('geri bildirim', () => {
    expect(exchangeFeedbackLabel({ feedback: 1 })).toBe('👍');
    expect(exchangeFeedbackLabel({ feedback: -1 })).toBe('👎');
    expect(exchangeFeedbackLabel({})).toBe('—');
  });
});

// ── v0.10.1280 — CoSRE adım 8: /ai 👍/👎 süzgeci + arşiv bağı ─────────────

describe('aiExchangesQueryKey — anahtar isteğin TÜM girdilerini taşır', () => {
  const base: AIExchangesQueryParams = { from: 1, to: 2, limit: 100, offset: 0, feedback: '', q: '', user: '', tier: '' };
  it('her alan anahtar listesinde (yeni girdi unutulamaz)', () => {
    // Örnek nesnenin HER alanı listede olmalı: tip bir alan kazanıp liste
    // kazanmazsa bu kırmızı (anahtar o girdiyi görmezdi).
    expect([...AI_EXCHANGES_QUERY_FIELDS].sort()).toEqual(Object.keys(base).sort());
  });
  it.each([
    ['from', { from: 9 }],
    ['to', { to: 9 }],
    ['limit', { limit: 50 }],
    ['feedback', { feedback: 'down' }],
    ['offset', { offset: 100 }],
    ['q', { q: 'x' }],
    ['user', { user: 'u' }],
    ['tier', { tier: 'loop' }],
  ] as const)('%s değişince anahtar değişir', (_name, patch) => {
    const a = JSON.stringify(aiExchangesQueryKey(base));
    const b = JSON.stringify(aiExchangesQueryKey({ ...base, ...patch } as AIExchangesQueryParams));
    expect(b).not.toBe(a);
  });
  it('aynı girdiler aynı anahtar', () => {
    expect(aiExchangesQueryKey({ ...base })).toEqual(aiExchangesQueryKey(base));
  });
  it('süzgeç yoksa istek feedback alanı TAŞIMAZ (eski gövde); varsa taşır', () => {
    expect('feedback' in aiExchangesRequestParams(base)).toBe(false);
    expect(aiExchangesRequestParams({ ...base, feedback: 'down' }).feedback).toBe('down');
  });
});

describe('exchange 👍/👎 görünümü', () => {
  it('süzgeç URL değeri kapalı kümeye', () => {
    expect(parseExchangeFeedbackFilter('down')).toBe('down');
    expect(parseExchangeFeedbackFilter('up')).toBe('up');
    expect(parseExchangeFeedbackFilter('rated')).toBe('rated');
    expect(parseExchangeFeedbackFilter('DOWN')).toBe('');
    expect(parseExchangeFeedbackFilter(null)).toBe('');
  });
  it('👎 hazır nedeni oyun yanında', () => {
    expect(exchangeFeedbackLabel({ feedback: -1, feedbackReason: 'incomplete' })).toBe('👎 eksik');
    expect(exchangeFeedbackLabel({ feedback: 1, feedbackReason: 'wrong' })).toBe('👍');
  });
  it('sayım', () => {
    expect(exchangeFeedbackCounts([{ feedback: 1 }, { feedback: -1 }, { feedback: -1 }, {}])).toEqual({ up: 1, down: 2 });
  });
  it('sonuç rozeti: iptal arıza rengi almaz', () => {
    expect(exchangeStatusBadge({ status: 'ok' })).toEqual({ label: 'ok', cls: 'b-gray' });
    expect(exchangeStatusBadge({ status: 'aborted' })).toEqual({ label: 'iptal', cls: 'b-warn' });
    expect(exchangeStatusBadge({ status: 'error' })).toEqual({ label: 'hata', cls: 'b-err' });
  });
  it('arşiv bağı yalnız konuşma kimliği varken', () => {
    expect(exchangeConversationHref({ conversationId: 'a b' })).toBe('/cosre?chat=a%20b');
    expect(exchangeConversationHref({})).toBeNull();
  });
});
