// @vitest-environment jsdom
//
// v0.10.1300 — CoSRE: hipotez doğrula çipi varken eski düğme gösterilmez
// (operatör 2026-10-17). Saf karar sreHypothesisDedup.test.ts'te; burada
// BAĞLANMA: ChatBubble action bloğunun düğmesini shownFollowups'a göre
// çiziyor mu, ve iki sohbet yüzeyi çip şeridinin followups'ını yalnız son
// cevaba veriyor mu.
import { describe, it, expect, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ChatFollowup, ChatTurn } from '@/lib/types';

vi.mock('@/lib/api', () => ({ api: { aiSignal: vi.fn(), enqueueProblemInvestigation: vi.fn() } }));
vi.mock('./useCopilotEnabled', () => ({ useCopilotEnabled: () => true, useCopilotConfig: () => null }));
vi.mock('@/components/CosreChart', () => ({ CosreChart: () => null }));

import { ChatBubble } from './ChatBubble';

const LABEL = 'Deploy hipotezini doğrula';
const turn: ChatTurn = {
  role: 'assistant', text: 'cevap',
  blocks: [{ id: 'a1', type: 'action', seq: 1, final: true,
    payload: { kind: 'sre_hypothesis', problemId: 'p-1', hypothesis: 'deploy', label: LABEL } }],
};
const chip = (hypothesis: string): ChatFollowup => ({ text: LABEL, kind: 'hypothesis',
  action: { kind: 'sre_hypothesis', problemId: 'p-1', hypothesis, label: LABEL } });
const html = (shownFollowups?: ChatFollowup[]) => renderToStaticMarkup(
  <QueryClientProvider client={new QueryClient()}><MemoryRouter>
    <ChatBubble turn={turn} shownFollowups={shownFollowups} />
  </MemoryRouter></QueryClientProvider>);

describe('ChatBubble — sre_hypothesis düğmesi çip tekrarında çizilmez', () => {
  it('aynı problem + tür çipi görünürken düğme yok', () => {
    expect(html([chip('deploy')])).not.toContain(LABEL);
  });
  it('çip yokken düğme kalır (arşiv / eski sunucu / önceki cevap)', () => {
    expect(html()).toContain(LABEL);
    expect(html([{ text: 'yavaşlar', kind: 'slow' }])).toContain(LABEL);
  });
  it('farklı tür çipi düğmeyi bastırmaz', () => {
    expect(html([chip('config')])).toContain(LABEL);
  });
});

describe('kaynak pinleri — çip followups yalnız son cevaba', () => {
  const src = (f: string) => readFileSync(new URL(f, import.meta.url), 'utf8');
  it('CopilotChat', () => {
    const c = src('../CopilotChat.tsx');
    expect(c).toContain('shownFollowups={isLast && showFollowups ? last?.followups : undefined}');
    expect(c).toContain('shownFollowups={showFollowups ? last?.followups : undefined}');
  });
  // v0.10.1407 — AIDrawerBody (çekmece özne kipi) silindi; tek yüzey CopilotChat.
});
