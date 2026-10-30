// cosrePersist.test — v0.10.1618 (CoSRE v2 Faz 2a): v2 → v1 kalıcılık eşlemesi.
// v1'in kaydettiği şey değişmez: aynı ChatMessage alanları, aynı tavan, aynı
// "durduruldu" notu (v1 kaynakları okunarak pinlenir — biri kayarsa kırmızı).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { PERSIST_MESSAGE_CAP, persistMessages } from '@/components/ai/chatPersist';
import { COSRE_PERSIST_CAP, STOPPED_NOTE, hasCompletedExchange, toPersistMessages, toRequestMessages } from './cosrePersist';
import type { CosreUIMessage } from './resetReducer';

const text = (t: string) => ({ type: 'text' as const, text: t, state: 'done' as const });
const user = (id: string, t: string): CosreUIMessage => ({ id, role: 'user', parts: [text(t)] });
const asst = (id: string, parts: CosreUIMessage['parts'], metadata?: CosreUIMessage['metadata']): CosreUIMessage =>
  ({ id, role: 'assistant', parts, ...(metadata ? { metadata } : {}) });

describe('toPersistMessages', () => {
  it('v1 persistMessages ile aynı şekil: {role,text} + exchangeId + codeRead', () => {
    const out = toPersistMessages([
      user('u1', 'q'),
      asst('01M62F9N000000000000000000', [{ type: 'step-start' }, text('cevap'),
        { type: 'data-code-lock', id: 'code-lock', data: { locked: true } }], { exchangeId: 'x1', callId: 'c1' }),
    ]);
    expect(out).toEqual([{ role: 'user', text: 'q' }, { role: 'assistant', text: 'cevap', exchangeId: 'x1', codeRead: true }]);
    // aynı tur v1 tarafında aynı baytları üretir
    expect(out).toEqual(persistMessages([
      { role: 'user', text: 'q' },
      { role: 'assistant', text: 'cevap', exchangeId: 'x1', codeRead: true },
    ]));
  });

  it('turnId exchangeId\'den önce (v1 turnArchiveId sırası)', () => {
    expect(toPersistMessages([asst('a', [text('x')], { turnId: 't', exchangeId: 'e' })])[0].exchangeId).toBe('t');
  });

  it('hatalı (metadata.error ya da data-error) ve boş metinli mesaj düşer; system rolü düşer', () => {
    const out = toPersistMessages([
      user('u1', 'q'),
      asst('a1', [text('yarım')], { error: true }),
      asst('a2', [text('x'), { type: 'data-error', id: 'error', data: { class: 'closed' } }]),
      asst('a3', [text('  ')]),
      { id: 's', role: 'system', parts: [text('sys')] },
    ]);
    expect(out).toEqual([{ role: 'user', text: 'q' }]);
    expect(hasCompletedExchange(out)).toBe(false);
  });

  it('partial (durdurulan) mesaj kaydedilir — v1 stopped turu da kaydeder', () => {
    expect(toPersistMessages([asst('a', [text('yarım')], { partial: true })])).toEqual([{ role: 'assistant', text: 'yarım' }]);
  });

  it('birden çok text parçası \\n\\n ile birleşir', () => {
    expect(toPersistMessages([asst('a', [text('bir'), { type: 'step-start' }, text('iki')])])[0].text).toBe('bir\n\niki');
  });

  it('tavan v1 ile eşit, son 40', () => {
    expect(COSRE_PERSIST_CAP).toBe(PERSIST_MESSAGE_CAP);
    const many = Array.from({ length: 45 }, (_, i) => user(`u${i}`, `q${i}`));
    const out = toPersistMessages(many);
    expect(out).toHaveLength(40);
    expect(out[0].text).toBe('q5');
  });
});

describe('toRequestMessages', () => {
  it('durdurulan tur notla, hatalı tur elenir', () => {
    expect(toRequestMessages([
      user('u', 'q'), asst('a', [text('yarım')], { partial: true }), asst('b', [text('x')], { error: true }),
    ])).toEqual([{ role: 'user', text: 'q' }, { role: 'assistant', text: `yarım${STOPPED_NOTE}` }]);
  });

  it('STOPPED_NOTE v1 useChatThread history notuyla bayt bayt aynı', () => {
    const src = readFileSync(resolve(__dirname, '../../components/ai/useChatThread.ts'), 'utf8');
    expect(src).toContain('`${t.text}' + STOPPED_NOTE.replace(/\n/g, '\\n') + '`');
  });
});
