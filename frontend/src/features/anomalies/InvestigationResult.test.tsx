// @vitest-environment jsdom
// InvestigationResult.test.tsx — v0.10.1287 (AI SRE Faz 1b): adım tablosu
// (sabit sıra, ≤8 — v0.10.1288 olaylar + node), bulgu kanıt çipleri, AI özeti / kapalı kapı cümlesi,
// "Sohbette sor" → coremetry:ai-ask (problem kapsamı).
import { describe, it, expect, afterEach, vi } from 'vitest';

// v0.10.2285 — metinler arayüz dilinde; bu test bugünkü Türkçe metni pinler.
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => 'tr' as const }));
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { SREInvestigation } from '@/lib/types';
import { InvestigationResult } from './InvestigationResult';
import { askInChatQuestion, findingEvidence, fmtStepDuration, orderedSteps, stepStateView } from './investigationResultView';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; vi.restoreAllMocks(); });

function render(inv: SREInvestigation) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><InvestigationResult inv={inv} /></MemoryRouter>); });
  return host;
}

const NOW_NS = Date.now() * 1e6;
const ev = (hop: string, href?: string) => ({
  tool: 'service_health_sweep', source: 'ClickHouse', query: `q-${hop}`, queryKind: 'summary',
  window: { fromMs: 1, toMs: 2 }, durationMs: 5, hop, href,
});

const inv: SREInvestigation = {
  problemId: 'p1', service: 'checkout', kind: 'auto', state: 'partial', attempts: 1, run: 1,
  enqueuedAt: NOW_NS - 300e9, finishedAt: NOW_NS - 60e9, problemStartedAt: NOW_NS - 900e9, anchor: { from: 0, to: 0, symptom: 'error' },
  steps: [
    { name: 'summary', state: 'ok', summary: 'Ne oldu: …', durationMs: 4100, at: NOW_NS },
    { name: 'log_spike', state: 'error', summary: 'log arka ucu bağlı değil', durationMs: 12, at: NOW_NS },
    { name: 'sweep', state: 'ok', summary: 'Sorunlu: POST /pay hata %9', durationMs: 950, at: NOW_NS,
      evidence: [ev('ops', '/traces?service=checkout'), ev('pods')] },
    { name: 'deploys', state: 'ok', summary: '1 değişiklik', durationMs: 300, at: NOW_NS, evidence: [ev('deploys')] },
    { name: 'deps', state: 'partial', summary: 'topoloji okunamadı', durationMs: 800, at: NOW_NS },
    { name: 'pivot', state: 'ok', summary: 'POST /pay — 2300 ms', durationMs: 1500, at: NOW_NS },
  ],
  findings: [
    { kind: 'operation', name: 'POST /pay', level: 'crit', reasons: ['hata %9'], evidenceRefs: ['evidences[0]', 'evidences[0]', 'evidences[9]'], step: 'sweep' },
    { kind: 'deploy', name: 'checkout@rev 42', level: 'warn', reasons: ['başlangıçtan 12 dk önce rollout'], evidenceRefs: ['evidences[0]'], step: 'deploys' },
  ],
  summary: { text: 'Ne oldu: checkout hata üretiyor.\nSonraki adım: rev 42\'yi inceleyin.', model: 'gemma', at: NOW_NS - 60e9 },
  synthRef: { topSuspect: 'payments-db', confidence: 0.58, computedAt: NOW_NS },
};

describe('InvestigationResult', () => {
  it('adım tablosu boru hattı sırasıyla, yalnız sapan durum rozetli', () => {
    const el = render(inv);
    const rows = Array.from(el.querySelectorAll('tbody tr')).map(r => r.querySelector('td')?.textContent);
    expect(rows).toEqual(['Servis taraması', 'Deploy', 'Bağımlılık', 'Trace pivotu', 'Log artışı', 'AI özeti']);
    expect(el.textContent).toContain('okunamadı');
    expect(el.textContent).toContain('kısmi');
    expect(el.querySelectorAll('tbody .badge, tbody [class*="badge"]').length).toBeGreaterThanOrEqual(2);
    expect(el.textContent).toContain('950 ms');
    expect(el.textContent).toContain('1,5 sn');
  });

  it('AI özeti, sentezleyici kopyası, bulgular + kanıt çipi, öneri notu', () => {
    const el = render(inv);
    expect(el.textContent).toContain('checkout hata üretiyor');
    expect(el.textContent).toContain('AI özeti · gemma');
    expect(el.textContent).toContain('şüpheli payments-db');
    expect(el.textContent).toContain('%58');
    expect(el.textContent).toContain('POST /pay');
    expect(el.textContent).toContain('(deploy · warn)');
    expect(el.querySelector('a[href="/traces?service=checkout"]')).toBeTruthy();
    expect(el.textContent).toContain('hiçbir değişiklik uygulanmadı');
  });

  it('kapı kapalı: sabit cümle + neden, ExplainBody yok', () => {
    const el = render({ ...inv, summary: { text: 'AI özeti yok (otomatik AI kapalı)', at: NOW_NS, gated: true, reason: 'otomatik AI açıklaması kapalı' } });
    expect(el.textContent).toContain('AI özeti yok (otomatik AI kapalı) — otomatik AI açıklaması kapalı');
    expect(el.textContent).not.toContain('AI özeti · ');
  });

  it('Sohbette sor → coremetry:ai-ask problem kapsamıyla', () => {
    const el = render(inv);
    let question = '';
    const h = (e: Event) => { question = (e as CustomEvent<{ question: string }>).detail.question; };
    window.addEventListener('coremetry:ai-ask', h);
    const btn = Array.from(el.querySelectorAll('button')).find(b => b.textContent === 'Sohbette sor');
    expect(btn).toBeTruthy();
    act(() => { btn!.click(); });
    window.removeEventListener('coremetry:ai-ask', h);
    expect(question).toBe(askInChatQuestion(inv));
    expect(question.startsWith('@problem:p1 ')).toBe(true);
  });

  // v0.10.1393 — oy rayı kaldırıldı (operatör: "Faydalı faydasız kaldıralım";
  // oy zayıf sinyal). AI özeti olan araştırma oy düğmesi çizmez.
  it('AI özetinin altında 👍/👎 rayı yok', () => {
    const el = render(inv);
    expect(inv.summary?.text).toBeTruthy();
    const labels = Array.from(el.querySelectorAll('button')).map(b => b.getAttribute('aria-label') ?? '');
    expect(labels.some(l => /faydal|faydas/i.test(l))).toBe(false);
    expect(el.textContent).not.toMatch(/👍|👎/);
  });

  it('0a tek-adım kaydı: tablo yok, tek satır özet', () => {
    const el = render({ ...inv, state: 'done', steps: [inv.steps[2]], summary: undefined, synthRef: undefined, findings: [] });
    expect(el.querySelector('table')).toBeNull();
    expect(el.textContent).toContain('Servis taraması (checkout): Sorunlu: POST /pay hata %9');
  });

  it('saf yardımcılar', () => {
    expect(findingEvidence(inv, inv.findings[0]).map(e => e.hop)).toEqual(['ops']);
    expect(findingEvidence(inv, inv.findings[1]).map(e => e.hop)).toEqual(['deploys']);
    expect(findingEvidence(inv, { kind: 'pod', name: 'x', level: 'warn', evidenceRefs: ['evidences[1]'] }).map(e => e.hop)).toEqual(['pods']);
    // v0.10.1288 — + olaylar, node: tavan 8, bilinmeyen adım sona düşer.
    const eight = orderedSteps([{ name: 'x', state: 'ok', durationMs: 0, at: 0 }, ...inv.steps,
      { name: 'node', state: 'ok', durationMs: 0, at: 0 }, { name: 'events', state: 'ok', durationMs: 0, at: 0 }]);
    expect(eight.map(s => s.name)).toEqual(['sweep', 'deploys', 'deps', 'pivot', 'log_spike', 'events', 'node', 'summary']);
    expect(stepStateView('ok').tone).toBeNull();
    expect(stepStateView('error').tone).toBe('danger');
    expect(fmtStepDuration(0)).toBe('—');
  });
});
