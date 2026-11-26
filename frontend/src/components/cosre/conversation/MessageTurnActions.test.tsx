// @vitest-environment jsdom
//
// MessageTurnActions.test — v0.10.2472: "Yeniden üret" her bitmiş cevapta
// (kendi mesaj kimliğiyle), "Düzenle" yalnız son kullanıcı mesajında; akarken
// ikisi de yok; düzenleyici yeni metni mesaj kimliğiyle verir, Vazgeç geri alır.
import { describe, it, expect, afterEach, beforeAll } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { MessageList, type CosreChatMessage, type CosreChatStatus } from './MessageList';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
beforeAll(() => { (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true; });
afterEach(async () => {
  await act(async () => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

const msg = (id: string, role: 'user' | 'assistant', text: string) =>
  ({ id, role, parts: [{ type: 'text', text, state: 'done' }] }) as unknown as CosreChatMessage;

const convo = [
  msg('u1', 'user', 'checkout endpointleri'),
  msg('a1', 'assistant', "checkout'un 3 endpoint'i var."),
  msg('u2', 'user', 'ikincisini aç'),
  msg('a2', 'assistant', 'POST /api/pay son 1 saatte sakin.'),
];

async function draw(status: CosreChatStatus, onRegenerate: (id: string) => void, onEdit: (id: string, t: string) => void) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(<MemoryRouter><MessageList messages={convo} visibleParts={m => m.parts} status={status}
      onRegenerate={onRegenerate} onEdit={onEdit} /></MemoryRouter>);
  });
  return host;
}

describe('MessageTurnActions', () => {
  it('her bitmiş cevapta Yeniden üret kendi kimliğiyle; Düzenle yalnız son soruda', async () => {
    const regen: string[] = [];
    const el = await draw('ready', id => regen.push(id), () => {});
    const btns = [...el.querySelectorAll<HTMLButtonElement>('[data-cosre-regenerate]')];
    expect(btns).toHaveLength(2);
    await act(async () => { btns[0].click(); });
    await act(async () => { btns[1].click(); });
    expect(regen).toEqual(['a1', 'a2']);
    expect(el.querySelectorAll('[data-cosre-edit]')).toHaveLength(1);
    expect(btns.every(b => !/\p{Extended_Pictographic}/u.test(b.textContent ?? ''))).toBe(true);
  });

  it('akarken eylem yok', async () => {
    const el = await draw('streaming', () => {}, () => {});
    expect(el.querySelector('[data-cosre-regenerate]')).toBeNull();
    expect(el.querySelector('[data-cosre-edit]')).toBeNull();
  });

  it('Düzenle: yeni metin mesaj kimliğiyle gider; Vazgeç düzenleyiciyi kapatır', async () => {
    const edits: [string, string][] = [];
    const el = await draw('ready', () => {}, (id, t) => edits.push([id, t]));
    await act(async () => { el.querySelector<HTMLButtonElement>('[data-cosre-edit]')!.click(); });
    const ta = el.querySelector<HTMLTextAreaElement>('[data-cosre-edit-form] textarea')!;
    expect(ta.value).toBe('ikincisini aç');
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
    await act(async () => {
      setter.call(ta, 'üçüncüsünü aç');
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => { el.querySelector<HTMLButtonElement>('[data-cosre-edit-form] button[type="submit"]')!.click(); });
    expect(edits).toEqual([['u2', 'üçüncüsünü aç']]);
    expect(el.querySelector('[data-cosre-edit-form]')).toBeNull();
    await act(async () => { el.querySelector<HTMLButtonElement>('[data-cosre-edit]')!.click(); });
    await act(async () => { el.querySelector<HTMLButtonElement>('[data-cosre-edit-form] button[type="button"]')!.click(); });
    expect(el.querySelector('[data-cosre-edit-form]')).toBeNull();
    expect(edits).toHaveLength(1);
  });
});
