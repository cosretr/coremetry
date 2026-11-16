// @vitest-environment jsdom
//
// v0.10.2298 (sunucu metinleri S1) — kapalı kodlar katalogdan. Pin:
//   - Go'daki her kod (hipotez kind / kategori / kontrol adı, kapı id'si,
//     zaman çizelgesi kind'ı) FE listesinde ve EN katalogda var; liste Go'dan
//     sapamaz (Go kaynağı okunur);
//   - bilinmeyen kod → sunucu metni; TR arayüz sunucu metnini AYNEN çizer;
//   - özgün dil sarmalayıcısı: EN'de lang="tr" + "TR" etiketi, TR'de hiçbir
//     şey eklemez (innerHTML birebir).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { t } from '@/lib/i18n';
import { STD_GATE_IDS, stdGateName, stdGateSummary } from '@/lib/problemStandardView';
import type { SignalGate } from '@/lib/types';
import {
  HYP_CATEGORIES, HYP_CHECKS, HYP_KINDS, TIMELINE_KIND, checkTitle, hypCategoryLabel, hypKindName, hypLabel, queueReasonText,
  timelineKindLabel,
} from './hypothesisView';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
import { OrigLang } from './OrigLang';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const GO = (rel: string) => readFileSync(resolve(__dirname, '../../../../', rel), 'utf8');
const hypGo = GO('internal/sre/hypotheses.go');
const table = hypGo.slice(hypGo.indexOf('var hypothesisTable'), hypGo.indexOf('\n}\n', hypGo.indexOf('var hypothesisTable')));
const goKinds = [...hypGo.matchAll(/\tHyp\w+\s+= "([a-z_]+)"/g)].map(x => x[1]);
const goCats = [...new Set([...table.matchAll(/\{Hyp\w+, "([a-z_]+)"/g)].map(x => x[1]))];
const goChecks = [...table.matchAll(/\{"([a-z0-9_]+)", [0-9.]+, chk\w+\}/g)].map(x => x[1]);
const goGates = [...GO('internal/chstore/signal_gates.go').matchAll(/SignalGate\{ID: "(G\d)"/g)].map(x => x[1]);
const goTimeline = [...GO('internal/sre/timeline.go').matchAll(/\tTL\w+\s+= "([a-z_]+)"/g)].map(x => x[1]);

const enHas = (k: string) => t(k, 'en') !== k;

describe('S1 — Go kodları FE listesinde ve EN katalogda', () => {
  it('Go listeleri okunabildi (regex boş dönmesin)', () => {
    expect(goKinds.length).toBe(8);
    expect(goChecks.length).toBeGreaterThanOrEqual(26);
    expect(goGates.length).toBe(7);
    expect(goTimeline.length).toBeGreaterThanOrEqual(7);
  });
  it('listeler Go aynası', () => {
    expect([...HYP_KINDS].sort()).toEqual([...goKinds].sort());
    expect([...HYP_CATEGORIES].sort()).toEqual([...goCats].sort());
    expect([...HYP_CHECKS].sort()).toEqual([...goChecks].sort());
    expect([...STD_GATE_IDS]).toEqual(goGates);
    expect([...TIMELINE_KIND].sort()).toEqual([...goTimeline].sort());
  });
  it('her kodun EN girdisi var', () => {
    const missing = [
      ...HYP_KINDS.map(k => `hyp.kind.${k}`), ...HYP_CATEGORIES.map(k => `hyp.cat.${k}`),
      ...HYP_CHECKS.map(k => `hyp.chk.${k}`), ...STD_GATE_IDS.map(k => `std.gate.${k}`),
      ...TIMELINE_KIND.map(k => `hyp.tl.${k}`), 'inv.queue.queued', 'inv.queue.done', 'orig.tr', 'orig.tr.title',
    ].filter(k => !enHas(k));
    expect(missing).toEqual([]);
  });
});

describe('S1 — çözümleme', () => {
  const h = { kind: 'db_slow' as const, label: 'sunucu-adı' };
  const gates: SignalGate[] = [
    { id: 'G1', name: 'trafik', status: 'pass' }, { id: 'G3', name: 'VM karar', status: 'unknown' },
    { id: 'G9', name: 'yeni-kapı', status: 'fail' }, { id: 'G5', name: 'deploy', status: 'context' },
  ];
  it('TR: sunucu metni aynen; lang verilmezse TR', () => {
    expect(hypLabel(h)).toBe('sunucu-adı');
    expect(hypLabel(h, 'tr')).toBe('sunucu-adı');
    expect(hypCategoryLabel('code_bug')).toBe('code_bug');
    expect(hypKindName('db_slow')).toBe('db_slow');
    expect(checkTitle('rate_up')).toBe('');
    expect(queueReasonText('queued', 'zaten kuyrukta')).toBe('zaten kuyrukta');
    expect(queueReasonText('done', 'zaten araştırıldı', 'tr')).toBe('zaten araştırıldı');
    expect(stdGateSummary(gates)).toBe('trafik ✓ VM karar ? yeni-kapı ✗');
  });
  it('EN: koddan; bilinmeyen kod sunucu metnine düşer', () => {
    expect(hypLabel(h, 'en')).toBe('Database slowness');
    expect(hypLabel({ kind: 'other' as never, label: 'x-1' }, 'en')).toBe('x-1');
    expect(hypCategoryLabel('code_bug', 'en')).toBe('code bug');
    expect(hypCategoryLabel('cat-1', 'en')).toBe('cat-1');
    expect(hypKindName('traffic_surge', 'en')).toBe('Traffic surge');
    expect(checkTitle('rate_up', 'en')).toBe('Request rate up');
    expect(checkTitle('c-unknown', 'en')).toBe('');
    expect(queueReasonText('running', 'zaten kuyrukta', 'en')).toBe('queued');
    expect(queueReasonText('partial', 'zaten araştırıldı', 'en')).toBe('investigated');
    expect(queueReasonText('done', undefined, 'en')).toBe('');
    expect(stdGateName({ id: 'G2', name: 'sürdürme' }, 'en')).toBe('sustained');
    expect(stdGateSummary(gates, 'en')).toBe('traffic ✓ VM decides ? yeni-kapı ✗');
    expect(timelineKindLabel('alert_resolved', 'en')).toBe('Alert resolved');
  });
});

describe('S1 — OrigLang', () => {
  let host: HTMLDivElement | null = null;
  let root: Root | null = null;
  afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });
  const render = async (node: React.ReactNode) => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(node); });
    return host.innerHTML;
  };
  it('TR: hiçbir şey eklemez', async () => {
    m.lang = 'tr';
    expect(await render(<p>önce <OrigLang>gerekçe-1</OrigLang></p>)).toBe('<p>önce gerekçe-1</p>');
  });
  it('EN: lang="tr" + soluk TR etiketi', async () => {
    m.lang = 'en';
    const html = await render(<p><OrigLang>gerekçe-1</OrigLang></p>);
    expect(html).toContain('<span lang="tr">gerekçe-1<span data-orig-lang=""');
    expect(html).toContain('title="Generated in Turkish');
    expect(host!.textContent).toBe('gerekçe-1TR');
  });
  it('EN blok: div sarmalayıcı', async () => {
    m.lang = 'en';
    const block = await render(<OrigLang block><p>özet</p></OrigLang>);
    expect(block.startsWith('<div lang="tr"><p>özet</p>')).toBe(true);
  });
});
