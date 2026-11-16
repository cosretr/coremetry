// @vitest-environment jsdom
//
// v0.10.2301 (sunucu metinleri S4) — inceleme kaydının metin anahtarları
// (öneri metni, zaman çizelgesi etiketi, atlama / veri-yok adım özeti, kapalı
// AI kapısı satırı). Pin:
//   - Go matrisinin ürettiği her anahtar (internal/sre/testdata/
//     inv_text_msgs.golden.json) EN çizilir: yer tutucu ve ham argüman
//     dışında Türkçe kalmaz;
//   - Go kaynağındaki her anahtar literali katalogda;
//   - TR arayüz sunucu metnini AYNEN çizer (innerHTML anahtarsız kayıtla aynı);
//   - eski kayıt (anahtarsız) EN'de özgün metin + "TR" işareti;
//   - öneri gerekçesi ve kontrol gerekçesi özgün kalır (işaretli).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { t } from '@/lib/i18n';
import type { SREInvestigation, StdMsg } from '@/lib/types';
import { stdMsgText, stdMsgView } from '@/lib/problemStandardView';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
vi.mock('@/lib/api', () => ({ api: new Proxy({}, { get: () => () => Promise.resolve({}) }) }));

import { InvestigationResult } from './InvestigationResult';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ROOT = resolve(__dirname, '../../../..');
const GO = (rel: string) => readFileSync(resolve(ROOT, rel), 'utf8');
type Fx = { name: string; tr: string; msg: StdMsg };
const FX: Fx[] = JSON.parse(GO('internal/sre/testdata/inv_text_msgs.golden.json'));
const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;

function rawStrings(msg: StdMsg): string[] {
  const out: string[] = [];
  for (const [k, v] of Object.entries(msg.a ?? {})) {
    if (typeof v === 'object') out.push(...rawStrings(v));
    else if (typeof v === 'string' && !k.endsWith('K')) out.push(v);
  }
  return out;
}

describe('S4 — Go fikstürü EN katalogda', () => {
  it('fikstür öneri, çizelge, adım ve kapı satırlarını kapsar', () => {
    const fam = new Set(FX.map(f => f.msg.k.split('.')[0]));
    expect([...fam].sort()).toEqual(['inv', 'rec', 'step', 'tl']);
    expect(FX.length).toBeGreaterThan(50);
  });
  it('her anahtar çizilir, yer tutucu ve Türkçe kalmaz', () => {
    const bad: string[] = [];
    for (const f of FX) {
      const en = stdMsgText(f.msg, 'en');
      if (en == null) { bad.push(`${f.name}: çizilemedi`); continue; }
      if (/[{}]/.test(en)) bad.push(`${f.name}: yer tutucu — ${en}`);
      let rest = en;
      for (const s of rawStrings(f.msg)) rest = rest.split(s).join('');
      if (TR_CHARS.test(rest) || /\b(veri yok|yok|okuyucusu|isteği|gözden|inceleyin|Alarm|başladı)\b/.test(rest)) bad.push(`${f.name}: Türkçe — ${en}`);
    }
    expect(bad).toEqual([]);
  });
  it('TR: sunucu metni bayt bayt', () => {
    for (const f of FX) expect(stdMsgView(f.msg, f.tr, 'tr')).toEqual({ text: f.tr, orig: false });
  });
  it('örnek EN çizimleri', () => {
    const en = (name: string) => stdMsgText(FX.find(f => f.name === name)!.msg, 'en');
    expect(en('rec:rollback-argo-unknown')).toBe('Rolling back svc-a rev 42 → rev 41 could be considered (Argo: status unknown) — a human makes the decision and applies it');
    expect(en('rec:sat-hot-nil')).toBe('Review resource usage of pod-1 (CPU 91% / memory ? of limit) and scaling');
    expect(en('gated:3')).toBe('No AI summary (automatic AI off) — hourly AI call cap reached (20)');
    const tl = FX.filter(f => f.name.startsWith('tl:first_error')).map(f => stdMsgText(f.msg, 'en'));
    expect(tl).toEqual(['First error (ERROR log increase)', 'First error (erroring representative trace)']);
  });
});

describe('S4 — Go kaynağındaki anahtar literalleri katalogda', () => {
  it('rec / tl / step / inv anahtarları', () => {
    const src = ['recommend.go', 'timeline.go', 'orchestrator.go', 'narrate.go', 'readers_errors.go', 'readers_anomalies.go', 'hypotheses.go']
      .map(f => GO(`internal/sre/${f}`)).join('\n');
    const keys = new Set<string>();
    for (const x of src.matchAll(/(?:NewStdMsg|recMsg)\("((?:rec|tl|step|inv)\.[\w.]+)"/g)) keys.add(x[1]);
    for (const x of src.matchAll(/skippedOutcome\([^,]+, "[^"]*", "([\w.]+)"\)/g)) keys.add(x[1]);
    for (const x of src.matchAll(/"((?:tl\.src|tl\.deploy|tl\.rollout|tl\.spike|rec\.argo)[\w.]*)"/g)) keys.add(x[1]);
    // "St" eki çalışma anında (timeline.go: k+"St").
    for (const k of ['tl.deploy', 'tl.deployPrev', 'tl.rollout']) keys.add(`${k}St`);
    expect(keys.size).toBeGreaterThan(55);
    expect([...keys].filter(k => t(`std.m.${k}`, 'en') === `std.m.${k}`)).toEqual([]);
  });
});

// Kayıt fikstürü: Go matrisinden alınmış anahtarlar.
const fx = (name: string) => { const f = FX.find(x => x.name === name); if (!f) throw new Error(name); return f; };
const NOW_NS = Date.now() * 1e6;
const skip = fx('step:deps:2');
const nod = fx('step:errors:14');
const gated = fx('gated:0');
const rec = fx('rec:dep');
const tl = fx('tl:incident_attach:11');
const INV: SREInvestigation = {
  problemId: 'p-1', service: 'svc-a', kind: 'live', state: 'done', attempts: 1, run: 1,
  enqueuedAt: NOW_NS - 300e9, finishedAt: NOW_NS - 60e9, problemStartedAt: NOW_NS - 900e9, anchor: { from: 0, to: 0 },
  steps: [
    { name: 'deps', state: 'skipped', summary: skip.tr, summaryMsg: skip.msg, durationMs: 0, at: NOW_NS },
    { name: 'errors', state: 'ok', summary: nod.tr, summaryMsg: nod.msg, durationMs: 5, at: NOW_NS },
  ],
  findings: [],
  summary: { text: 'AI özeti yok (otomatik AI kapalı)', reason: 'AI yapılandırılmamış ya da kapalı', gated: true, at: NOW_NS, gateMsg: gated.msg },
  timeline: [{ at: NOW_NS - 600e9, kind: 'incident_attach', label: tl.tr, labelMsg: tl.msg }],
  recommendations: [{ id: 'hyp-dependency', text: rec.tr, textMsg: rec.msg, rationale: 'Bağımlılık arızası hipotezi orta 0.62 (kod skoru)', state: 'pending' }],
} as unknown as SREInvestigation;
const stripMsgs = (inv: SREInvestigation): SREInvestigation => ({
  ...inv,
  steps: inv.steps.map(s => ({ ...s, summaryMsg: undefined })),
  summary: inv.summary ? { ...inv.summary, gateMsg: undefined } : undefined,
  timeline: inv.timeline?.map(e => ({ ...e, labelMsg: undefined })),
  recommendations: inv.recommendations?.map(r => ({ ...r, textMsg: undefined })),
}) as SREInvestigation;

describe('S4 — inceleme sonucu', () => {
  let host: HTMLDivElement | null = null;
  let root: Root | null = null;
  afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });
  const render = async (inv: SREInvestigation) => {
    if (root) { act(() => { root?.unmount(); }); host?.remove(); }
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => {
      root!.render(<QueryClientProvider client={qc}><MemoryRouter><InvestigationResult inv={inv} /></MemoryRouter></QueryClientProvider>);
    });
    return { text: host.textContent ?? '', html: host.innerHTML };
  };
  it('EN: anahtarlı metinler İngilizce; gerekçe özgün + işaret', async () => {
    m.lang = 'en';
    const { text } = await render(INV);
    for (const s of ['no dependency request', 'no data: no erroring operations or exception groups in the window',
      'No AI summary (automatic AI off) — AI not configured or off', 'Attached to incident',
      'Check the health and open problems of downstream service svc-b']) expect(text, s).toContain(s);
    for (const tr of [skip.tr, nod.tr, tl.tr, rec.tr, 'AI özeti yok']) expect(text, tr).not.toContain(tr);
    const marked = [...host!.querySelectorAll('[lang="tr"]')].map(e => e.textContent);
    expect(marked).toEqual(['Bağımlılık arızası hipotezi orta 0.62 (kod skoru)TR']);
  });
  it('EN: eski kayıt (anahtarsız) özgün metin + işaret', async () => {
    m.lang = 'en';
    const { text } = await render(stripMsgs(INV));
    for (const tr of [skip.tr, nod.tr, tl.tr, rec.tr]) expect(text, tr).toContain(tr);
    expect(host!.querySelectorAll('[lang="tr"]').length).toBe(6);
  });
  it('TR: anahtarlı ve anahtarsız kayıt aynı HTML', async () => {
    m.lang = 'tr';
    const a = await render(INV);
    const b = await render(stripMsgs(INV));
    expect(a.html).toBe(b.html);
    for (const tr of [skip.tr, nod.tr, tl.tr, rec.tr]) expect(a.text, tr).toContain(tr);
    expect(host!.querySelector('[lang="tr"]')).toBeNull();
  });
});
