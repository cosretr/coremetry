// @vitest-environment jsdom
//
// ApprovalCard.test — agent-core S5 (docs/cosre/agent-core-ideas.md §4.7 "v2 UI
// testi (approve/deny/expired)"). Paylaşılan testdata/cosre/ui-stream-v1-approval
// GERÇEK AI SDK'dan geçer, MessageList çizer:
//   • kart çalışacak argümanı gösterir; Onayla / Reddet → approvals[] kalemi
//     (stepId, token, exchangeId, stepBase, approved)
//   • akış sürerken (onApproval yok) düğmeler kapalı
//   • süre dolunca düğme yok, "Süresi doldu"
//   • devam akışından sonra kart "Onaylandı", düğme yok
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { cosreFixture, sseFetch } from '@/test/cosreSse';
import { createCosreChat, type CosreChatCore } from '@/lib/cosre/useCosreChat';
import { approvalViews } from '@/lib/cosre/approval';
import type { CosreApprovalAnswer } from '@/lib/types';
import { MessageList, type CosreChatStatus } from './MessageList';
import { ApprovalCard } from './ApprovalCard';

const NOW = 1_800_000_000_000; // fixture expiresAt 1_800_000_300_000 → 5 dk kaldı

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => { (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true; });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
  vi.restoreAllMocks();
});

async function mount(node: React.ReactNode) {
  if (!host) {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
  }
  await act(async () => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  return host!;
}

async function pendingCore(): Promise<CosreChatCore> {
  const core = createCosreChat({ fetch: sseFetch(cosreFixture('ui-stream-v1-approval')).fetch });
  await core.chat.sendMessage({ text: 'notu ekle' });
  return core;
}

const draw = (core: CosreChatCore, onApproval?: (a: CosreApprovalAnswer) => void, status?: CosreChatStatus) =>
  mount(<MessageList messages={core.chat.messages} visibleParts={core.visibleParts}
    status={status ?? (core.chat.status as CosreChatStatus)} onApproval={onApproval} />);

const button = (el: Element, text: string) =>
  [...el.querySelectorAll('button')].find(b => b.textContent === text) as HTMLButtonElement | undefined;

describe('ApprovalCard × ui-stream-v1-approval (gerçek SDK)', () => {
  it.each([['Onayla', true], ['Reddet', false]] as const)('%s → approvals[] kalemi (approved=%s)', async (label, approved) => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW);
    const core = await pendingCore();
    const answers: CosreApprovalAnswer[] = [];
    const el = await draw(core, a => answers.push(a));
    const card = el.querySelector('[data-cosre-approval="s3"]')!;
    expect(card.getAttribute('data-state')).toBe('pending');
    expect(card.textContent).toContain('Probleme not ekle');
    expect(card.textContent).toContain('DB havuzu doldu'); // çalışacak argüman, model metni değil
    expect(card.textContent).toContain('Geçerlilik sonu'); // yoklama yok: tek zamanlayıcı süre dolunca
    await act(async () => { button(card, label)!.click(); });
    expect(answers).toEqual([{
      stepId: 's3', tool: 'add_problem_note', args: { problemId: 'p-1', text: 'DB havuzu doldu' },
      token: 'eyJrIjoiY29zcmUtYXBwcm92YWwifQ.c2ln', approved, exchangeId: 'x-approval-1', stepBase: 3,
    }]);
  });

  it('akış sürerken / cevap kancası yokken düğmeler kapalı', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW);
    const core = await pendingCore();
    const el = await draw(core, () => {}, 'streaming');
    expect(button(el, 'Onayla')?.disabled).toBe(true);
    expect(button(el, 'Reddet')?.disabled).toBe(true);
  });

  it('süre dolunca düğme yok, "Süresi doldu" (zaman aşımı = ret)', async () => {
    const core = await pendingCore();
    const view = approvalViews(core.chat.messages.at(-1)!)[0];
    const onAnswer = vi.fn();
    const el = await mount(<ApprovalCard view={view} onAnswer={onAnswer} now={() => view.approval.expiresAt + 1} />);
    expect(el.querySelector('[data-cosre-approval]')?.getAttribute('data-state')).toBe('expired');
    expect(el.textContent).toContain('Süresi doldu');
    expect(button(el, 'Onayla')).toBeUndefined();
    expect(onAnswer).not.toHaveBeenCalled();
  });

  it('devam akışından sonra kart "Onaylandı", düğme yok', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(NOW);
    const first = sseFetch(cosreFixture('ui-stream-v1-approval'));
    const second = sseFetch(cosreFixture('ui-stream-v1-approval-resume'));
    let n = 0;
    const fetch = ((u: RequestInfo | URL, i?: RequestInit) => (n++ === 0 ? first : second).fetch(u, i)) as typeof globalThis.fetch;
    const core = createCosreChat({ fetch });
    await core.chat.sendMessage({ text: 'notu ekle' });
    const view = approvalViews(core.chat.messages.at(-1)!)[0];
    await core.answerApproval({ stepId: 's3', tool: view.approval.tool, args: view.args, token: view.approval.token, approved: true, stepBase: 3 });
    const el = await draw(core, () => {});
    const card = el.querySelector('[data-cosre-approval="s3"]')!;
    expect(card.getAttribute('data-state')).toBe('approved');
    expect(card.textContent).toContain('Onaylandı');
    expect(button(card, 'Onayla')).toBeUndefined();
    expect(el.textContent).toContain('Not probleme eklendi.');
  });
});

describe('approvalViews (saf)', () => {
  it('eşli araç parçası yoksa kart yok; kullanıcı mesajında yok', async () => {
    const core = await pendingCore();
    const m = core.chat.messages.at(-1)!;
    expect(approvalViews({ ...m, parts: m.parts.filter(p => p.type !== 'dynamic-tool') })).toEqual([]);
    expect(approvalViews(core.chat.messages[0])).toEqual([]);
  });

  it('istemci {approved:false} yazdıysa denied', async () => {
    const core = await pendingCore();
    const m = core.chat.messages.at(-1)!;
    const parts = m.parts.map(p => (p.type === 'dynamic-tool' && p.toolCallId === 's3'
      ? { ...p, state: 'output-available' as const, output: { approved: false } } : p));
    expect(approvalViews({ ...m, parts: parts as typeof m.parts })[0].state).toBe('denied');
  });
});
