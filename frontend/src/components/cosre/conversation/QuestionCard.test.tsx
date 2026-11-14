// @vitest-environment jsdom
//
// QuestionCard.test — v0.10.2102 (docs/cosre/experience-audit.md D2).
// Sözleşme: data-question ≤3 seçenekli kapalı soru; kart etkinse tık
// seçeneği verir, pasifse düğmeler kapalı ve seçilen aria-pressed. Biçimsiz
// yük kart çizdirmez (düz metin soru balonda kalır).
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, it, expect, afterEach, beforeAll, vi } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QUESTION_KINDS, parseDataQuestion } from '@/lib/cosre/protocol';
import type { CosreQuestion } from '@/lib/types';
import { QuestionCard } from './QuestionCard';
import { questionOf, withClarifyOption } from './question';

const Q: CosreQuestion = { kind: 'source', text: 'Coremetry mi wiki mi?', allowFree: true, options: [
  { id: 'telemetri', label: 'Coremetry verisi' }, { id: 'wiki', label: 'Wiki dokümanları' },
] };
const opt = (id: string) => ({ id, label: id });

describe('parseDataQuestion', () => {
  it.each([
    [{ type: 'data-question', data: { ...Q } }, Q],
    [{ type: 'data-question', data: { ...Q, allowFree: 'yes' } }, { ...Q, allowFree: false }],
    // v0.10.2103 (D3) — doc_or_entity + trace_synth aynı kart.
    [{ type: 'data-question', data: { ...Q, kind: 'trace_synth', options: [{ id: 'yes', label: 'Evet, incele' }, { id: 'no', label: 'Hayır' }] } },
      { ...Q, kind: 'trace_synth', options: [{ id: 'yes', label: 'Evet, incele' }, { id: 'no', label: 'Hayır' }] }],
    [{ type: 'data-question', data: { ...Q, kind: 'doc_or_entity' } }, { ...Q, kind: 'doc_or_entity' }],
    // v0.10.2139 (router boşlukları D6) — yazım hatalı çıplak ad: en yakın ≤3 servis.
    [{ type: 'data-question', data: { ...Q, kind: 'entity_pick', options: [opt('svc-a'), opt('svc-b')] } },
      { ...Q, kind: 'entity_pick', options: [opt('svc-a'), opt('svc-b')] }],
    [{ type: 'data-question', data: { ...Q, kind: 'later' } }, null],
    [{ type: 'data-question', data: { ...Q, text: '' } }, null],
    [{ type: 'data-question', data: { ...Q, options: [] } }, null],
    [{ type: 'data-question', data: { ...Q, options: ['a', 'b', 'c', 'd'].map(opt) } }, null],
    [{ type: 'data-question', data: { ...Q, options: [opt('a'), opt('a')] } }, null],
    [{ type: 'data-question', data: { ...Q, options: [{ id: 'a', label: '' }] } }, null],
    [{ type: 'data-pick', data: { ...Q } }, null],
  ])('%j → %j', (p, want) => {
    expect(parseDataQuestion(p)).toEqual(want);
  });

  it('kapalı küme Go kaynağıyla eşit', () => {
    const part = readFileSync(join(__dirname, '..', '..', '..', '..', '..', 'internal', 'cosre', 'uistream', 'part.go'), 'utf8');
    const m = /QuestionKinds = \[\]string\{([^}]*)\}/.exec(part);
    expect(m?.[1].split(',').map(s => s.trim().replace(/"/g, ''))).toEqual([...QUESTION_KINDS]);
    expect(part).toMatch(/QuestionMaxOptions = 3\b/);
  });
});

describe('question yardımcıları', () => {
  it('questionOf: son geçerli yük', () => {
    expect(questionOf([{ type: 'data-question', data: { ...Q } }, { type: 'data-question', data: {} }])).toEqual(Q);
    expect(questionOf([{ type: 'text' }])).toBeNull();
  });
  it('withClarifyOption: son katman', () => {
    expect(withClarifyOption({ conversation: 'c1', clarifyOption: 'x' }, 'wiki')).toEqual({ conversation: 'c1', clarifyOption: 'wiki' });
    expect(withClarifyOption(undefined, 'wiki')).toEqual({ clarifyOption: 'wiki' });
  });
});

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => { (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true; });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});
async function draw(el: React.ReactElement) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(el); });
  return host;
}

describe('QuestionCard', () => {
  it('etkin: düğme başına seçenek, tık seçeneği verir; soru cümlesi tekrar yazılmaz', async () => {
    const onAnswer = vi.fn();
    const el = await draw(<QuestionCard question={Q} onAnswer={onAnswer} />);
    const btns = [...el.querySelectorAll<HTMLButtonElement>('[data-question-option]')];
    expect(btns.map(b => b.textContent)).toEqual(['Coremetry verisi', 'Wiki dokümanları']);
    expect(el.textContent).not.toContain(Q.text);
    expect(el.querySelector('[role="group"]')!.getAttribute('aria-label')).toBe(Q.text);
    await act(async () => { btns[1].click(); });
    expect(onAnswer).toHaveBeenCalledWith(Q.options[1]);
  });
  it('pasif: düğmeler kapalı, seçilen aria-pressed', async () => {
    const el = await draw(<QuestionCard question={Q} chosen="wiki" />);
    const btns = [...el.querySelectorAll<HTMLButtonElement>('[data-question-option]')];
    expect(btns.every(b => b.disabled)).toBe(true);
    expect(btns.map(b => b.getAttribute('aria-pressed'))).toEqual(['false', 'true']);
  });
});
