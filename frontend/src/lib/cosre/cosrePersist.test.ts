// cosrePersist.test — v0.10.1618 (CoSRE v2 Faz 2a): v2 → v1 kalıcılık eşlemesi.
// v1'in kaydettiği şey değişmez: aynı ChatMessage alanları, aynı tavan, aynı
// "durduruldu" notu (v1 kaynakları okunarak pinlenir — biri kayarsa kırmızı).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { PERSIST_MESSAGE_CAP, persistMessages, restoreTurns } from '@/components/ai/chatPersist';
import { COSRE_PERSIST_CAP, STOPPED_NOTE, fromPersistMessages, hasCompletedExchange, messageText, toPersistMessages, toRequestMessages } from './cosrePersist';
import { toChatTurn } from './toChatTurn';
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

// v0.10.1664 (B6, plan §1.4 / §5.5) — bayrak çevrimi thread kaybettirmez:
// aynı blob iki yönde de okunur.
describe('fromPersistMessages + bayrak çevrimi yuvarlaması', () => {
  const ev = { tool: 'q', source: 'spans', window: { fromMs: 1, toMs: 2 } };
  const explain = { kind: 'trace', id: 'abc', label: 'trace abc' };
  const v1Blob = [
    { role: 'user' as const, text: 'bu trace neden yavaş' },
    {
      role: 'assistant' as const, text: 'çünkü db', exchangeId: '0123456789abcdef0123456789abcdef', codeRead: true,
      evidence: [ev], explain, blocks: [{ id: 'b1', type: 'charts_signals' as const, seq: 2, final: true, payload: { a: 1 } }],
      blocksAt: 1700000000000, blocksDropped: ['topology'],
    },
  ];

  it('v1 blobu → v2: metin, data-block, kanıt, kod kilidi; exchangeId arşiv kimliği (canlı değil)', () => {
    const out = fromPersistMessages(v1Blob as never, 'c1');
    expect(out.map(m => m.id)).toEqual(['c1:0', 'c1:1']);
    const a = out[1];
    expect(a.metadata).toEqual({ archiveExchangeId: v1Blob[1].exchangeId, explain, blocksAt: 1700000000000, blocksDropped: ['topology'] });
    expect(a.metadata?.exchangeId).toBeUndefined();
    expect(a.parts.map(p => p.type)).toEqual(['text', 'data-block-charts_signals', 'data-evidence-summary', 'data-code-lock']);
    const turn = toChatTurn(a, a.parts);
    expect(turn).toMatchObject({ text: 'çünkü db', archiveExchangeId: v1Blob[1].exchangeId, explain, codeRead: true, evidence: [ev] });
    expect(turn.exchangeId).toBeUndefined();
    expect(turn.blocks).toEqual([{ id: 'b1', type: 'charts_signals', seq: 2, final: true, payload: { a: 1 } }]);
  });

  it('v1 → v2 → v1: yeniden kayıt v1 persistMessages(restoreTurns(blob)) ile aynı', () => {
    expect(toPersistMessages(fromPersistMessages(v1Blob as never, 'c1'))).toEqual(persistMessages(restoreTurns(v1Blob as never)));
  });

  it('v2 → v1: v2 kaydı v1 restoreTurns ile aynı metinleri ve künyeyi verir (yarım tur dahil)', () => {
    const saved = toPersistMessages([
      user('01M62F9N000000000000000001', 'q1'),
      asst('01M62F9N000000000000000002', [text('a1'), { type: 'data-evidence-summary', id: 'evidence', data: { evidence: [ev] } }], { turnId: 't1', explain: explain as never }),
      user('01M62F9N000000000000000003', 'q2'),
      asst('01M62F9N000000000000000004', [text('a2 yarım')], { exchangeId: 'x2', partial: true }),
    ]);
    const back = restoreTurns(saved);
    expect(back.map(t => t.text)).toEqual(['q1', 'a1', 'q2', 'a2 yarım']);
    expect(back[1]).toMatchObject({ archiveExchangeId: 't1', explain, evidence: [ev] });
    expect(fromPersistMessages(saved, 'c').map(messageText)).toEqual(['q1', 'a1', 'q2', 'a2 yarım']);
  });

  it('boş metinli mesaj düşer; boş girdi boş dizi', () => {
    expect(fromPersistMessages(undefined, 'c')).toEqual([]);
    expect(fromPersistMessages([{ role: 'user', text: '  ' }], 'c')).toEqual([]);
  });
});

// CoSRE v2 araç seti dilim 1 — data-tool-result + data-stop kayıt yolundan
// (B6) gidip gelir; bilinmeyen kind / neden sessizce düşer, UI değişmez.
describe('data-tool-result + data-stop kalıcılığı (araç seti dilim 1)', () => {
  const tr = { kind: 'evidence', tool: 'query_traces', ok: true, query: 'SELECT 1', page_url: '/traces?service=orders-api' };
  const stop = { reason: 'call_cap', calls: 6, rounds: 3, elapsed_ms: 2140 };
  const msg = asst('01M62F9N000000000000000002', [
    text('cevap'),
    { type: 'data-tool-result', id: 's2', data: tr },
    { type: 'data-tool-result', id: 's3', data: { kind: 'chart', ok: true } },
    { type: 'data-tool-result', id: 's4', data: { kind: 'text', ok: true, page_url: 'https://evil.example/x' } },
    { type: 'data-stop', id: 'stop', data: stop },
  ]);

  it('toPersistMessages: geçerli sonuçlar + durma; bilinmeyen kind ve dış page_url düşer', () => {
    const [a] = toPersistMessages([msg]);
    expect(a.toolResults).toEqual([
      { id: 's2', data: { ...tr, id: 's2' } },
      { id: 's4', data: { kind: 'text', ok: true, id: 's4' } },
    ]);
    expect(a.stop).toEqual(stop);
  });

  it('gidiş-dönüş: fromPersistMessages aynı parçaları kurar, yeniden kayıt aynı', () => {
    const saved = toPersistMessages([user('u', 'q'), msg]);
    const back = fromPersistMessages(saved, 'c1');
    const parts = back[1].parts.filter(p => p.type === 'data-tool-result' || p.type === 'data-stop');
    expect(parts).toEqual([
      { type: 'data-tool-result', id: 's2', data: tr },
      { type: 'data-tool-result', id: 's4', data: { kind: 'text', ok: true } },
      { type: 'data-stop', id: 'stop', data: stop },
    ]);
    expect(toPersistMessages(back)).toEqual(saved);
  });

  it('görünür tur değişmez: toChatTurn parçaları yok sayar', () => {
    const plain = asst('01M62F9N000000000000000002', [text('cevap')]);
    expect(toChatTurn(msg, msg.parts)).toEqual(toChatTurn(plain, plain.parts));
  });

  it('arşivde bozuk kayıt düşer (bilinmeyen neden, biçimsiz id)', () => {
    const back = fromPersistMessages([
      { role: 'assistant', text: 'x', toolResults: [{ id: 'zz', data: tr as never }], stop: { ...stop, reason: 'bilinmeyen' as never } },
    ], 'c');
    expect(back[0].parts.map(p => p.type)).toEqual(['text']);
  });
});
