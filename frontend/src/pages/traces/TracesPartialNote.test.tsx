// @vitest-environment jsdom
//
// v0.10.2466 — Operator-reported: 12 saatlik cluster süzgeçli liste 500
// veriyordu. Artık sunucu kısmi sonuç döner; not kapsamı + süre/sınırı
// söyler ve "Daha uzun bekle" düğmesi YALNIZ longWaitSec > 0 iken çıkar.
// Ayrıca CoSRE "daha uzun bekle" çipinin tıkı context.longWait taşır ve
// admin tavanı aralık dışını kabul etmez.
import { describe, it, expect, vi } from 'vitest';
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { TracesPartialNote, partialElapsedText } from './TracesPartialNote';
import { followupSendOpts } from '@/components/ai/followupSend';
import { parseLongWaitCap } from '@/pages/settings/TracesLongWaitPanel';
import { buildCopilotChatContext } from '@/lib/copilotChatContext';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function render(el: React.ReactElement) {
  const host = document.createElement('div');
  document.body.appendChild(host);
  const root = createRoot(host);
  act(() => root.render(el));
  return { host, unmount: () => act(() => root.unmount()) };
}

describe('TracesPartialNote', () => {
  it('shows elapsed / limit and the long-wait button that fires once', () => {
    expect(partialElapsedText({ elapsedMs: 25068, limitSec: 25 })).toBe('25.1 / 25');
    const onLongWait = vi.fn();
    const { host, unmount } = render(
      <TracesPartialNote lang="tr" onLongWait={onLongWait}
        partial={{ reason: 'timeout', elapsedMs: 25068, limitSec: 25, longWaitSec: 60, coveredFromNs: 1_700_000_000_000_000_000 }} />);
    expect(host.textContent).toContain('Kısmi sonuç');
    expect(host.textContent).toContain('25.1 / 25');
    const btn = host.querySelector('[data-traces-long-wait]') as HTMLButtonElement;
    expect(btn.textContent).toContain('60');
    act(() => btn.click());
    expect(onLongWait).toHaveBeenCalledTimes(1);
    unmount();
  });

  it('hides the button after a long wait already ran', () => {
    const { host, unmount } = render(
      <TracesPartialNote lang="en" onLongWait={() => {}}
        partial={{ reason: 'timeout', elapsedMs: 61000, limitSec: 60, longWaitSec: 0 }} />);
    expect(host.querySelector('[data-traces-long-wait]')).toBeNull();
    expect(host.textContent).toContain('no slice finished');
    unmount();
  });
});

describe('long-wait plumbing', () => {
  it('CoSRE chip click carries context.longWait; other chips do not', () => {
    expect(followupSendOpts({ text: 'Evet, 60 sn bekle', kind: 'long_wait', longWait: true })).toEqual({ longWait: true });
    expect(followupSendOpts({ text: 'x', kind: 'trace' })?.longWait).toBeUndefined();
    expect(buildCopilotChatContext({ longWait: true })).toMatchObject({ longWait: true });
    expect(buildCopilotChatContext({ service: 'a' })).not.toHaveProperty('longWait');
  });

  it('admin cap accepts only integers in range', () => {
    expect(parseLongWaitCap('60', 25, 180)).toBe(60);
    expect(parseLongWaitCap('24', 25, 180)).toBeNull();
    expect(parseLongWaitCap('181', 25, 180)).toBeNull();
    expect(parseLongWaitCap('45.5', 25, 180)).toBeNull();
  });
});
