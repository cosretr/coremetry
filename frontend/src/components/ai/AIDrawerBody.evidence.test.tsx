// @vitest-environment jsdom
//
// v0.10.1033 — operatör (prod, trace'ten açılan AI paneli): "Kanıt span'lere
// gerek yok." Açıklamanın altında "Kanıt span'leri (6)" başlıklı, ham hex
// span kimliklerinden oluşan altı satır vardı; kimse okumuyordu. Liste
// kaldırıldı — ama kimlikler İKİ yerde yaşamaya devam ediyor ve bu test
// ikisini de çalışma zamanında pinliyor:
//   (a) çekmece gövdesinde "Kanıt span'leri" bölümü ve ham span kimliği YOK;
//       Kanıt satırı yalnız sayıyı ve doğru ipucunu ("waterfall'da kutulu") söyler;
//   (b) kimlikler sayfaya duyurulur (emitAiEvidence → Trace.tsx evidenceIds →
//       TraceWaterfall `.wf-evidence`) — v0.9.408 kutulaması ("kök neden
//       soruşturulması gereken kısımlar kutulanmıyor") girdisini kaybetmez;
//   (c) takip sohbetinin bağlamında ("Kanıt span'leri: …") kalır — operatöre
//       görünmez, "Hangi kanıta dayanıyorsun?" çipini besler;
//   (d) exception'ın "Kanıt trace'leri" listesi AYNEN durur.
//
// NEDEN GERÇEK MOUNT: kaldırılan bölüm bir render dalıydı ve kutulama girdisi
// CopilotExplain → onEvidence → emitAiEvidence zinciri; kaynak pini yalnız bir
// metnin yokluğunu kanıtlardı. Adlar sentetik (payments, prod).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { api } from '@/lib/api';
import type { AISubject } from '@/lib/aiSubject';
import type { ChatStreamEvent } from '@/lib/types';
import { AIDrawerBody } from './AIDrawerBody';
import { AI_EVIDENCE_EVENT, type AIEvidenceDetail } from './aiEvents';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';

const TRACE = '0af7651916cd43dd8448eb211c80319c';
const SPANS = ['b7ad6b7169203331', '00f067aa0ba902b7', '53995c3f42cd8ad8'];
const FP = 'exc-fp-0001';
const SAMPLE_TRACES = ['4bf92f3577b34da6a3ce929d0e0e4736', '5b8efff798038103d269b633813fc60c'];
const ANSWER = 'Kök neden: payments yavaş.';

let host: HTMLDivElement;
let root: Root;
let emitted: AIEvidenceDetail[];
const onEvidence = (e: Event) => { emitted.push((e as CustomEvent<AIEvidenceDetail>).detail); };

beforeEach(() => {
  window.history.replaceState({}, '', '/');
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  __resetCopilotEnabledCache();
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
  vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'drw-1', title: 't', updatedAt: 1, messages: [] });
  emitted = [];
  window.addEventListener(AI_EVIDENCE_EVENT, onEvidence);
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  window.removeEventListener(AI_EVIDENCE_EVENT, onEvidence);
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function mount(subject: AISubject) {
  await act(async () => {
    root.render(<MemoryRouter><AIDrawerBody subject={subject} onClose={() => {}} /></MemoryRouter>);
  });
  // config → auto-koşu → cevap: birkaç mikro görev
  for (let i = 0; i < 3; i++) await act(async () => { await Promise.resolve(); });
}

const button = (label: string) =>
  Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes(label));

describe('AIDrawerBody — "Kanıt span\'leri" listesi kaldırıldı (v0.10.1033)', () => {
  it('trace: bölüm ve ham span kimliği çizilmez; Kanıt satırı waterfall kutusunu işaret eder', async () => {
    vi.spyOn(api, 'copilotExplainTrace').mockResolvedValue({ explanation: ANSWER, exchangeId: 'x1', evidenceSpanIds: SPANS });
    await mount({ kind: 'trace', id: TRACE });

    expect(host.textContent).toContain(ANSWER); // cevap gerçekten geldi (boş gövde yanlış-yeşil vermesin)
    expect(host.textContent).not.toContain("Kanıt span'leri");
    for (const id of SPANS) expect(host.innerHTML).not.toContain(id);
    expect(host.querySelector('.wf-evidence')).toBeNull();
    const line = host.querySelector('.cx-evidence')?.textContent ?? '';
    expect(line).toContain('Kanıt: 3 span');
    expect(line).toContain("waterfall'da kutulu");
    expect(line).not.toContain('çekmecenin altında');
  });

  it('trace: kimlikler sayfaya duyurulur — waterfall kutulamasının (v0.9.408) girdisi sağlam', async () => {
    vi.spyOn(api, 'copilotExplainTrace').mockResolvedValue({ explanation: ANSWER, exchangeId: 'x1', evidenceSpanIds: SPANS });
    await mount({ kind: 'trace', id: TRACE });
    expect(emitted).toEqual([{ spanIds: SPANS }]);
  });

  it('trace: kimlikler takip sohbetinin bağlamında kalır (görünmez)', async () => {
    vi.spyOn(api, 'copilotExplainTrace').mockResolvedValue({ explanation: ANSWER, exchangeId: 'x1', evidenceSpanIds: SPANS });
    const chat = vi.spyOn(api, 'copilotChat').mockImplementation(
      async (_m, onEvent: (e: ChatStreamEvent) => void) => {
        onEvent({ kind: 'answer', text: 'payments p95 arttı', exchangeId: 'c1' });
        onEvent({ kind: 'done', ok: true });
      });
    await mount({ kind: 'trace', id: TRACE });
    await act(async () => { button('Bu neden oluyor?')?.click(); });
    expect(chat).toHaveBeenCalledTimes(1);
    // copilotChat konumsal: (messages, onEvent, signal, service, operation, explain, …)
    const explain = String(chat.mock.calls[0][5] ?? '');
    expect(explain).toContain(`Kanıt span'leri: ${SPANS.join(', ')}`);
    for (const id of SPANS) expect(host.innerHTML).not.toContain(id);
  });

  it('exception: "Kanıt trace\'leri" listesi DURUR; span kimliği yine çizilmez', async () => {
    vi.spyOn(api, 'copilotExplainException').mockResolvedValue({
      explanation: ANSWER, exchangeId: 'x2', evidenceTraceIds: SAMPLE_TRACES, evidenceSpanIds: [SPANS[0]],
    });
    await mount({ kind: 'exception', id: FP });

    expect(host.textContent).toContain(`Kanıt trace'leri (${SAMPLE_TRACES.length})`);
    expect(host.querySelectorAll('.wf-evidence')).toHaveLength(SAMPLE_TRACES.length);
    for (const id of SAMPLE_TRACES) expect(host.textContent).toContain(id);
    expect(host.textContent).not.toContain("Kanıt span'leri");
    expect(host.innerHTML).not.toContain(SPANS[0]);
    const line = host.querySelector('.cx-evidence')?.textContent ?? '';
    expect(line).toContain('Kanıt: 2 trace');
    expect(line).toContain('kimlikler çekmecenin altında');
    expect(emitted).toEqual([{ spanIds: [SPANS[0]] }, { traceIds: SAMPLE_TRACES }]);
  });
});
