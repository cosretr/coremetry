// @vitest-environment jsdom
//
// v0.10.987 (operatör "3 seçenek") — trace sayfasında ikinci düğme "Hızlı
// açıkla" / "Quick explain": eski tek atışlık klasik açıklama. Aynı özne
// (?ai=trace) + ?aiquick=1; sunucu gövdede quick:true görünce klasik yolu
// koşar. Sözleşme: (1) katalog metinleri, (2) tık aynı özneyi yazar ve
// aiquick=1 ekler, yabancı param korunur, (3) "CoSRE'ye sor" düğmesi açıkken
// hızlı düğme "açık" sayılmaz — tık kapatmak yerine hızlıya GEÇER ve
// aiquick düşer/gelir, (4) explainInit gövdesi quick taşır.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { AIExplainButton } from '@/components/ai/AIExplainButton';
import { __resetCopilotEnabledCache } from '@/components/ai/useCopilotEnabled';
import { IconSparkles } from '@/components/icons';
import { api } from '@/lib/api';
import { AI_QUICK_PARAM } from '@/lib/aiSubject';
import { setUserLang, t, useT } from '@/lib/i18n';

const TRACE_ID = 'abc123def456';
let host: HTMLDivElement;
let root: Root;
let lastSearch = '';

function Probe() { lastSearch = useLocation().search; return null; }

// Trace.tsx'in iki düğmesi (kaynak pini aşağıda aynı anahtarları doğrular).
function TraceButtons({ id }: { id: string }) {
  const tr = useT();
  return (
    <>
      <AIExplainButton subject={{ kind: 'trace', id }} emphasis="strong"
        title={tr('ai.askCosreTraceHint')}
        label={<><IconSparkles /> <span>{tr('ai.askCosre')}</span></>} />
      <AIExplainButton subject={{ kind: 'trace', id }} quick
        title={tr('ai.quickExplainHint')}
        label={<><IconSparkles /> <span>{tr('ai.quickExplain')}</span></>} />
    </>
  );
}

async function mount(initial: string) {
  await act(async () => {
    root.render(<MemoryRouter initialEntries={[initial]}><Probe /><TraceButtons id={TRACE_ID} /></MemoryRouter>);
  });
}
const buttons = () => Array.from(host.querySelectorAll('button'));

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  lastSearch = '';
  __resetCopilotEnabledCache();
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true });
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setUserLang(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('katalog', () => {
  it('iki dilde ad + ipucu', () => {
    expect(t('ai.quickExplain', 'tr')).toBe('Hızlı açıkla');
    expect(t('ai.quickExplain', 'en')).toBe('Quick explain');
    expect(t('ai.quickExplainHint', 'tr')).toContain('canlı okuma yok');
    expect(t('ai.quickExplainHint', 'en')).toContain('no live reads');
  });
});

describe('hızlı düğme — adres sözleşmesi', () => {
  it('tık aynı özneyi yazar + aiquick=1; yabancı param korunur; ikinci tık kapatır', async () => {
    setUserLang('tr');
    await mount(`/trace?id=${TRACE_ID}&span=s1`);
    const [ask, quick] = buttons();
    expect(quick.textContent?.trim()).toBe('Hızlı açıkla');
    await act(async () => { quick.click(); });
    let p = new URLSearchParams(lastSearch);
    expect(p.get('ai')).toBe('trace');
    expect(p.get(AI_QUICK_PARAM)).toBe('1');
    expect(p.get('span')).toBe('s1');
    expect(quick.getAttribute('aria-expanded')).toBe('true');
    expect(ask.getAttribute('aria-expanded')).toBe('false'); // aynı özne ama hızlı kip: CoSRE düğmesi açık sayılmaz
    await act(async () => { quick.click(); });
    p = new URLSearchParams(lastSearch);
    expect(p.get('ai')).toBeNull();
    expect(p.get(AI_QUICK_PARAM)).toBeNull();
  });

  it('CoSRE açıkken hızlıya tık kapatmaz, kipe geçer; tersi aiquick\'i düşürür', async () => {
    setUserLang('en');
    await mount(`/trace?id=${TRACE_ID}`);
    const [ask, quick] = buttons();
    await act(async () => { ask.click(); });
    expect(new URLSearchParams(lastSearch).get(AI_QUICK_PARAM)).toBeNull();
    await act(async () => { quick.click(); });
    let p = new URLSearchParams(lastSearch);
    expect(p.get('ai')).toBe('trace');
    expect(p.get(AI_QUICK_PARAM)).toBe('1');
    await act(async () => { ask.click(); });
    p = new URLSearchParams(lastSearch);
    expect(p.get('ai')).toBe('trace');
    expect(p.get(AI_QUICK_PARAM)).toBeNull();
  });
});

describe('kaynak pini', () => {
  it('Trace.tsx hızlı düğmeyi quick ile kurar; api gövdesi quick taşır', () => {
    const page = readFileSync(resolve(__dirname, 'Trace.tsx'), 'utf8');
    expect(page).toContain("<AIExplainButton subject={{ kind: 'trace', id }} quick");
    expect(page).toContain("tr('ai.quickExplain')");
    const apiSrc = readFileSync(resolve(__dirname, '../lib/api.ts'), 'utf8');
    expect(apiSrc).toContain('...(quick ? { quick: true } : {})');
    const panel = readFileSync(resolve(__dirname, '../components/CopilotExplain.tsx'), 'utf8');
    expect(panel).toContain("useState(() => kind === 'trace' && readAiQuickParam())");
    expect(panel).toContain('api.copilotExplainTrace(id, withCode, opts, spanId, quick)');
  });
});
