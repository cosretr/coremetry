// @vitest-environment jsdom
//
// v0.10.2300 (sunucu metinleri S3) — öncelik gerekçesi anahtarları + yerleşik
// kural adının EN görünümü. Pin:
//   - Go matrislerinin ürettiği her anahtar (internal/chstore|api/testdata/
//     priority_reason_msgs.golden.json) EN çizilir: yer tutucu kalmaz, ham
//     argümanlar dışında Türkçe harf kalmaz;
//   - Go kaynağındaki her gerekçe anahtarı literali katalogda;
//   - TR arayüz sunucu metnini AYNEN çizer (fikstürdeki metin bayt bayt);
//   - süre biçimi Go shortDur'un dallarını izler;
//   - satır alt çizgisi (DetailLine): EN'de gerekçe İngilizce, TR'de aynen.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { t } from '@/lib/i18n';
import type { InboxItem, StdMsg } from '@/lib/types';
import { enDur, prioReasonView, detailPrioReasonMsg, problemTitleView, stdMsgText } from './problemStandardView';

const m = vi.hoisted(() => {
  // InboxCells → ProblemDetail → uPlot modül yüklemesinde matchMedia ister.
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { lang: 'en' as 'en' | 'tr' };
});
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
import { DetailLine } from '@/pages/inbox/InboxCells';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ROOT = resolve(__dirname, '../../..');
const GO = (rel: string) => readFileSync(resolve(ROOT, rel), 'utf8');
type Fx = { name: string; tr: string; msg: StdMsg };
const FX: Fx[] = [
  ...JSON.parse(GO('internal/chstore/testdata/priority_reason_msgs.golden.json')),
  ...JSON.parse(GO('internal/api/testdata/priority_reason_msgs.golden.json')),
];
const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;

// Ham (çevrilmeyen) metin argümanları — iç içe mesajlar dahil.
function rawStrings(msg: StdMsg): string[] {
  const out: string[] = [];
  for (const [k, v] of Object.entries(msg.a ?? {})) {
    if (typeof v === 'object') out.push(...rawStrings(v));
    else if (typeof v === 'string' && !k.endsWith('K')) out.push(v);
  }
  return out;
}

describe('S3 — Go fikstürü EN katalogda', () => {
  it('fikstür her merdiveni kapsar', () => {
    const fam = new Set(FX.map(f => f.msg.k.split('.').slice(0, 2).join('.')));
    for (const k of ['prio.breach', 'prio.inc', 'prio.ex', 'prio.ora', 'prio.an', 'prio.keepR', 'prio.keep', 'prio.forceP3', 'prio.linked', 'prio.storm'])
      expect(fam, k).toContain(k);
  });
  it('her anahtar çizilir, yer tutucu ve Türkçe kalmaz', () => {
    const bad: string[] = [];
    for (const f of FX) {
      const en = stdMsgText(f.msg, 'en');
      if (en == null) { bad.push(`${f.name}: çizilemedi`); continue; }
      if (/[{}]/.test(en)) bad.push(`${f.name}: yer tutucu — ${en}`);
      let rest = en;
      for (const s of rawStrings(f.msg)) rest = rest.split(s).join('');
      if (TR_CHARS.test(rest) || /\b(olay|önce|bitti|toplam|eşik|son 1 sa|kaynak|dk|sn)\b/.test(rest)) bad.push(`${f.name}: Türkçe — ${en}`);
    }
    expect(bad).toEqual([]);
  });
  it('TR: sunucu metni bayt bayt', () => {
    for (const f of FX) expect(prioReasonView(f.tr, f.msg, 'tr')).toEqual({ text: f.tr, orig: false });
  });
  it('örnek EN çizimleri', () => {
    const en = (name: string) => stdMsgText(FX.find(f => f.name === name)!.msg, 'en');
    expect(en('ex-burst-ended')).toBe('11,260 events / 12m (~938/min) · ended 5h ago');
    expect(en('ex-stopped')).toBe('600 total · stopped 2h 5m ago');
    expect(en('ex-burst-secs')).toBe('1,200 events / 30s (~1200/min)');
    expect(en('ex-seen')).toBe('seen in last 4h · 150 total');
    expect(en('critical-total-loss-frac')).toBe('critical + total loss (0/99.5)');
    expect(en('storm')).toBe('storm: 9 services (threshold 5)');
    expect(en('ora-steady')).toBe('steady flow (lbl-1) · last 1h 5');
    expect(en('keep:custom-*:critical')).toBe('source priority kept (exception list: custom-*) · critical + 2.5x threshold');
    expect(en('fold:incident:i-1')).toBe('linked problem: critical + 2.5x threshold');
    expect(en('force:anomaly')).toBe('kind rule: items other than exception / HTTP error are P3 in the inbox (source priority P2)');
    // Zaten İngilizce olan gerekçeler aynen.
    for (const n of ['critical-breach', 'critical-stale', 'warning-steady', 'inc-critical-ack', 'ex-active', 'an-peak'])
      expect(en(n), n).toBe(FX.find(f => f.name === n)!.tr);
  });
  it('süre: Go shortDur dalları (eşitlikte çifte)', () => {
    expect([0, 30, 59.4, 150, 210, 3599, 3600, 7500, 93600].map(enDur)).toEqual(['0s', '30s', '59s', '2m', '4m', '60m', '1h', '2h 5m', '26h']);
  });
});

describe('S3 — Go kaynağındaki gerekçe anahtarları katalogda', () => {
  it('stdM / NewStdMsg / nestedReasonMsg literalleri', () => {
    const src = ['internal/chstore/problem.go', 'internal/chstore/incident_priority.go', 'internal/api/inbox.go',
      'internal/api/exception_oracle.go', 'internal/api/inbox_keep_priority.go', 'internal/api/inbox_incident_fold.go'].map(GO).join('\n');
    const keys = new Set<string>();
    for (const x of src.matchAll(/(?:stdM|NewStdMsg|nestedReasonMsg)\("((?:prio|keep)\.[\w.]+)"/g)) {
      if (x[1].endsWith('.')) { keys.add(`${x[1]}critical`); keys.add(`${x[1]}warning`); } else keys.add(x[1]);
    }
    expect(keys.size).toBeGreaterThan(50);
    expect([...keys].filter(k => t(`std.m.${k}`, 'en') === `std.m.${k}`)).toEqual([]);
  });
});

describe('S3 — düşüş', () => {
  it('anahtar yok → EN sunucu metni + orig; iç anahtar çizilemezse bütün cümle düşer', () => {
    expect(prioReasonView('kaynak önceliği korundu', undefined, 'en')).toEqual({ text: 'kaynak önceliği korundu', orig: true });
    expect(prioReasonView('x', { k: 'prio.linked', a: { rM: { k: 'nope' } } }, 'en')).toEqual({ text: 'x', orig: true });
    expect(prioReasonView('', undefined, 'en')).toEqual({ text: '', orig: false });
  });
  it('detay: başlık ucunun anahtarı yalnız aynı gerekçeye aitse', () => {
    const std = { priorityReason: 'critical', priorityReasonMsg: { k: 'prio.critical' } } as never;
    expect(detailPrioReasonMsg('critical', std)).toEqual({ k: 'prio.critical' });
    expect(detailPrioReasonMsg('critical open 5.2h', std)).toBeUndefined();
    expect(detailPrioReasonMsg('critical', null)).toBeUndefined();
  });
});

describe('S3 — yerleşik kural adı (yalnız görünüm)', () => {
  const v = (ruleName: string, kind: string, lang: 'en' | 'tr' = 'en', ruleId = '', metric = '') => problemTitleView({ ruleName, ruleId, metric }, kind, lang);
  it('EN: std.kind\'dan; ham kuyruk korunur', () => {
    expect(v('Exception fırtınası', 'exception_storm')).toEqual({ text: 'Exception storm', orig: false });
    expect(v('HTTP hatası · 503', 'exception_group.http')).toEqual({ text: 'HTTP error · 503', orig: false });
    expect(v('Dış kaynak erişilemiyor · src-1', 'anomaly.ext_down').text).toBe('External source unreachable · src-1');
    expect(v('Anomaly · err (external) · tavan aşıldı', 'anomaly.ext_cap').text).toBe('Anomaly · err (external) · cap exceeded');
    expect(v('Anomaly · err (external) · küme', 'anomaly_cluster.ext').text).toBe('Anomaly · err (external) · cluster');
    expect(v('Yaygın yavaşlama · trafik çöküşü', 'svc_slowdown.collapse').text).toBe('Widespread slowdown · traffic collapse');
    expect(v('Runtime · JVM GC zaman payı', 'runtime.gc', 'en', 'runtime:jvm-gc-share:svc-a', 'runtime.jvm_gc_share').text).toBe('Runtime · JVM GC time share');
    expect(v('Coremetry · disk dolacak', 'self', 'en', 'self-disk-eta').text).toBe('Coremetry · disk will fill');
  });
  it('EN: operatör / zaten İngilizce ad aynen; serbest ad orig', () => {
    expect(v('checkout p99', 'rule.user')).toEqual({ text: 'checkout p99', orig: false });
    expect(v('DB health · oracle', 'db_health')).toEqual({ text: 'DB health · oracle', orig: false });
    expect(v('ML · servis anomalisi', 'anomaly_auto')).toEqual({ text: 'ML · servis anomalisi', orig: true });
    expect(v('Coremetry · başka', 'self', 'en', 'self-volume-spike').orig).toBe(true);
    expect(v('Dış kaynak erişilemiyor', 'anomaly.ext_down').orig).toBe(true); // kuyruk yok → çizilemez
  });
  it('TR: kalıcı ad aynen', () => {
    for (const [n, k] of [['Exception fırtınası', 'exception_storm'], ['HTTP hatası · 503', 'exception_group.http'], ['ML · servis anomalisi', 'anomaly_auto']])
      expect(v(n, k, 'tr')).toEqual({ text: n, orig: false });
  });
});

describe('S3 — satır alt çizgisi (DetailLine)', () => {
  let host: HTMLDivElement | null = null;
  let root: Root | null = null;
  afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });
  const render = async (it: InboxItem) => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(<MemoryRouter><DetailLine it={it} /></MemoryRouter>); });
    return host.textContent ?? '';
  };
  const keepFx = FX.find(f => f.name === 'keep:svc-slowdown:*:critical')!;
  const row = {
    id: 'problem:p-1', kind: 'problem', source: 'Alert rule', priority: 'P1', severity: 'critical', service: 'svc-a',
    title: 'rule-1', description: '', startedAt: 0, lastSeen: 0, status: 'open',
    priorityReason: keepFx.tr, priorityReasonMsg: keepFx.msg,
    problem: { id: 'p-1', ruleId: 'svc-slowdown:svc-a', metric: 'p99_ms', value: 1, threshold: 1 },
  } as unknown as InboxItem;
  it('EN: gerekçe İngilizce, Türkçe yok', async () => {
    m.lang = 'en';
    const txt = await render(row);
    expect(txt).toContain('source priority kept (widespread slowdown) · critical + 2.5x threshold');
    expect(TR_CHARS.test(txt)).toBe(false);
  });
  it('EN: anahtarsız gerekçe özgün dil işaretiyle', async () => {
    m.lang = 'en';
    await render({ ...row, priorityReasonMsg: undefined });
    expect(host!.querySelector('[lang="tr"]')?.textContent).toBe(`${keepFx.tr}TR`);
  });
  it('TR: sunucu metni aynen, işaret yok', async () => {
    m.lang = 'tr';
    const txt = await render(row);
    expect(txt).toContain(` · ${keepFx.tr}`);
    expect(host!.querySelector('[lang="tr"]')).toBeNull();
  });
});
