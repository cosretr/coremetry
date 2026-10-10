// v0.10.1153 — /ai "CoSRE etkileşimleri" görünüm modeli (SAF).
import { describe, expect, it } from 'vitest';
import type { AIExchange } from '@/lib/types';
import {
  NO_LLM_LABEL, exchangeCallsSummary, exchangeFeedbackLabel, exchangeModelLabel,
  exchangeTierLabel, exchangeTokensLabel, exchangeUsedLLM,
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
