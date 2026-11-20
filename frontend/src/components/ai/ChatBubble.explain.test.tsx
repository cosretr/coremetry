// @vitest-environment jsdom
//
// ChatBubble.explain.test.tsx — v0.10.1400 (Tek CoSRE). Operatör: "Trace ve
// exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı
// drawer hissi, CoSRE bütünselliğine uymuyor."
//
// Açıklama turu (turn.explain) sohbet balonunda ✨ Explain anatomisiyle
// çizilir: Karar şeridi (cümle gövdede tekrarlanmaz), Kanıt satırı, önbellek
// etiketi, kanıt trace satırları; bekleyişte tek tip AiThinking; kanıt
// sayfaya YALNIZ BİR KEZ duyurulur (canlı tur), arşivden gelen tur duyurmaz.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: q.includes('prefers-reduced-motion: reduce'), media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { ChatBubble } from './ChatBubble';
import { AI_EVIDENCE_EVENT, type AIEvidenceDetail } from './aiEvents';
import type { ChatTurn } from '@/lib/types';

const TRACE = '5b8efff798038103d269b633813fc60c';
const TEXT = [
  '**Hata ve Anlamı**',
  '- payments POST /charge 402 döndü.',
  '',
  '**Kök Neden ve Sonraki Adım**',
  '- Ödeme sağlayıcısı kartı reddediyor. Kontrol: sağlayıcı yanıt kodu.',
].join('\n');

let host: HTMLDivElement;
let root: Root;
let evidence: AIEvidenceDetail[] = [];
const onEv = (e: Event) => { evidence.push((e as CustomEvent<AIEvidenceDetail>).detail); };

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  evidence = [];
  window.addEventListener(AI_EVIDENCE_EVENT, onEv);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  window.removeEventListener(AI_EVIDENCE_EVENT, onEv);
});

async function render(turn: ChatTurn) {
  await act(async () => {
    root.render(<MemoryRouter><ChatBubble turn={turn} /></MemoryRouter>);
  });
}

const done: ChatTurn = {
  role: 'assistant', text: TEXT, exchangeId: 'x1',
  explain: {
    kind: 'exception', id: 'ora:00000000000000b2', label: 'ORD-200', surface: 'explain-exception:chat',
    evidenceSpanIds: ['aaaaaaaaaaaaaaa1'], evidenceTraceIds: [TRACE],
  },
};

describe('açıklama turu balonu', () => {
  it('Karar şeridi + Kanıt satırı + kanıt trace satırı; Karar cümlesi gövdede tekrar etmez', async () => {
    await render(done);
    const v = host.querySelector('.cx-verdict');
    expect(v?.textContent).toContain('Ödeme sağlayıcısı kartı reddediyor.');
    expect(host.textContent?.split('Ödeme sağlayıcısı kartı reddediyor.').length).toBe(2);
    expect(host.querySelector('.cx-evidence')?.textContent).toContain('Kanıt: 1 span · 1 trace');
    expect(host.querySelector('[data-explain-traces]')?.textContent).toContain(TRACE);
    expect(host.querySelector('[data-explain-cached]')).toBeNull();
    // Eylem satırı (kopyala) sohbetin olağan balonundaki gibi.
    expect(host.textContent).toContain('Kopyala');
  });

  it('kanıt sayfaya BİR KEZ duyurulur (yeniden çizim tekrar etmez)', async () => {
    await render(done);
    await render({ ...done });
    expect(evidence).toEqual([{ spanIds: ['aaaaaaaaaaaaaaa1'] }, { traceIds: [TRACE] }]);
  });

  it('arşivden gelen tur (exchangeId yok) kanıt duyurmaz', async () => {
    await render({ ...done, exchangeId: undefined });
    expect(evidence).toEqual([]);
    expect(host.querySelector('.cx-verdict')).toBeTruthy();
  });

  it('önbellek isabeti etiketlenir (✨ düğmesiyle aynı cevap)', async () => {
    await render({ ...done, explain: { ...done.explain!, cached: true, cachedAtMs: Date.now() - 120_000 } });
    const c = host.querySelector('[data-explain-cached]');
    expect(c?.textContent).toContain('♻ önbellekten');
    expect(c?.textContent).toContain('2 dk önce');
  });

  it('bekleyiş: metin yokken tek tip AiThinking; akarken anatomi yok (ham metin)', async () => {
    await render({ role: 'assistant', text: '', pending: true, explain: { kind: 'trace', id: TRACE } });
    expect(host.querySelector('.cm-wait-center')).toBeTruthy();
    await render({ role: 'assistant', text: TEXT, pending: true, explain: { kind: 'trace', id: TRACE } });
    expect(host.querySelector('.cx-verdict')).toBeNull();
    expect(host.textContent).toContain('Kök Neden ve Sonraki Adım');
    expect(evidence).toEqual([]);
  });

  it('künyesiz cevap eski çizimde (ExplainBody yok)', async () => {
    await render({ role: 'assistant', text: TEXT, exchangeId: 'x2' });
    expect(host.querySelector('.cx-verdict')).toBeNull();
    expect(host.textContent).toContain('Ödeme sağlayıcısı kartı reddediyor.');
  });
});

// v0.10.1405 (Tek CoSRE adım 2; operatör: "Trace ve exception üzerinden
// çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE
// bütünselliğine uymuyor.") — runbook / problem / … açıklama turları da balonda
// ExplainBody ile çizilir; runbook "RUNBOOK" etiketi + "N geçmiş çözüm" notunu
// (CopilotExplain kart başlığı + meta satırının aynısı) taşır.
describe('göç eden türler (v0.10.1405)', () => {
  it('runbook: RUNBOOK etiketi + not satırı + ExplainBody', async () => {
    await render({
      role: 'assistant', text: TEXT, exchangeId: 'x1',
      explain: { kind: 'runbook', id: '0000000000000p01', label: 'RUNBOOK', renderer: 'body',
        note: 'Based on 2 past resolved instances of this rule on this service.' },
    });
    expect(host.querySelector('[data-explain-label]')?.textContent).toBe('RUNBOOK');
    expect(host.querySelector('[data-explain-note]')?.textContent).toBe('Based on 2 past resolved instances of this rule on this service.');
    expect(host.querySelector('.cx-verdict')?.textContent).toContain('Ödeme sağlayıcısı kartı reddediyor.');
  });
  it('bekleyen runbook turu etiketi gösterir, notu göstermez', async () => {
    await render({ role: 'assistant', text: 'yazıyor', pending: true, explain: { kind: 'runbook', id: 'p-42', note: 'x' } });
    expect(host.querySelector('[data-explain-label]')?.textContent).toBe('RUNBOOK');
    expect(host.querySelector('[data-explain-note]')).toBeNull();
  });
  it('problem: etiket satırı yok, gövde ExplainBody', async () => {
    await render({ role: 'assistant', text: TEXT, exchangeId: 'x2', explain: { kind: 'problem', id: 'p-1', label: 'orders-api p99 latency', renderer: 'body' } });
    expect(host.querySelector('[data-explain-label]')).toBeNull();
    expect(host.querySelector('.cx-verdict')).toBeTruthy();
  });
});
