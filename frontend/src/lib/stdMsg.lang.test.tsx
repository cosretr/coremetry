// @vitest-environment jsdom
//
// v0.10.2299 (sunucu metinleri S2) — problem standardı metin anahtarları. Pin:
//   - Go matrisinin ürettiği her anahtar (internal/chstore/testdata/
//     problem_standard_msgs.golden.json) EN katalogda çizilir: yer tutucu
//     kalmaz, ham argümanlar dışında Türkçe harf kalmaz;
//   - Go kaynağındaki her anahtar literali katalogda (matrisin değmediği dal
//     da kapsanır);
//   - TR arayüz sunucu metnini AYNEN çizer (fikstürdeki Türkçe metin bayt
//     bayt); anahtar yoksa / bilinmiyorsa EN sunucu metnine düşer + orig;
//   - başlık bloğu: EN'de kapsanan alanlarda Türkçe yok, serbest manşet "TR"
//     işaretli; TR'de metin aynen.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import { t } from '@/lib/i18n';
import type { ProblemStandard, StdMsg } from '@/lib/types';
import { stdGateTitle, stdHeadlineView, stdMsgText, stdMsgView, stdContextView } from './problemStandardView';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));
import { ProblemStandardBlock } from '@/features/anomalies/ProblemStandardHeader';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ROOT = resolve(__dirname, '../../..');
const GO = (rel: string) => readFileSync(resolve(ROOT, rel), 'utf8');
type Fx = { field: string; tr: string; msg: StdMsg };
const FX: Fx[] = JSON.parse(GO('internal/chstore/testdata/problem_standard_msgs.golden.json'));
const TR_CHARS = /[çğıöşüÇĞİÖŞÜ]/;

const rawStrings = (msg: StdMsg) => Object.entries(msg.a ?? {})
  .filter(([k, v]) => typeof v === 'string' && !k.endsWith('K') && v !== 'inf')
  .map(([, v]) => String(v));

describe('S2 — Go fikstürü EN katalogda', () => {
  it('fikstür dolu ve her alan türü var', () => {
    expect(FX.length).toBeGreaterThan(100);
    // v0.10.2383 — 'reason': etki kuralı "Neden" satırları.
    expect([...new Set(FX.map(f => f.field))].sort()).toEqual(['context', 'evidence', 'gate', 'headline', 'impact', 'next', 'reason']);
  });
  it('her anahtar çizilir, yer tutucu ve Türkçe kalmaz', () => {
    const bad: string[] = [];
    for (const f of FX) {
      const en = stdMsgText(f.msg, 'en');
      if (en == null) { bad.push(`${f.msg.k}: çizilemedi`); continue; }
      if (/[{}]/.test(en)) bad.push(`${f.msg.k}: yer tutucu — ${en}`);
      let rest = en;
      for (const s of rawStrings(f.msg)) rest = rest.split(s).join('');
      if (TR_CHARS.test(rest)) bad.push(`${f.msg.k}: Türkçe — ${en}`);
    }
    expect(bad).toEqual([]);
  });
  it('TR: sunucu metni bayt bayt (anahtar yok sayılır)', () => {
    for (const f of FX) expect(stdMsgView(f.msg, f.tr, 'tr')).toEqual({ text: f.tr, orig: false });
  });
  it('örnek EN çizimleri (sayı / birim / oran / tekil)', () => {
    const en = (k: string) => FX.find(f => f.tr === k)?.msg;
    expect(stdMsgText(en('svc-a: hata oranı arttı — %3,25, normal %0,4'))).toBe('svc-a: error rate rose — 3.25%, normal 0.4%');
    expect(stdMsgText(en('svc-a: yanıt süresi uzadı — p99 1,45 sn, normal 320 ms'))).toBe('svc-a: response time rose — p99 1.45 s, normal 320 ms');
    expect(stdMsgText(en('svc-a: trafik düştü — %91,2 azaldı (3,5/sn)'))).toBe('svc-a: traffic dropped — down 91.2% (3.5/s)');
    expect(stdMsgText(en('svc-a: SLO hata bütçesi hızla tükeniyor — tam kayıp'))).toBe('svc-a: SLO error budget burning fast — full loss');
    expect(stdMsgText(en('svc-a: operasyon yavaşladı — GET /a (4,2×)'))).toBe('svc-a: operation slowed — GET /a (4.2×)');
    expect(stdMsgText(en('Kural eşiği aşıldı.'))).toBe('Rule threshold exceeded.');
    expect(stdMsgText(en('Tek ölçümde eşik aşıldı; Oracle hata tablosundan.'))).toBe('Threshold exceeded in a single measurement; from the Oracle error table.');
    expect(stdMsgText(en('Tek ölçümde eşik aşıldı.'))).toBe('Threshold exceeded in a single measurement.');
    expect(stdMsgText({ k: 'imp.occur', a: { cNum: 1 } })).toBe('1 occurrence');
    // v0.10.2383 — ML servis hata oranı manşeti + "Neden" satırları (Go fikstüründen).
    expect(stdMsgText(en('svc-a servisinde hata oranı normalde %0,4, şimdi %6,8 (17 kat).'))).toBe('svc-a: error rate normally 0.4%, now 6.8% (17×).');
    expect(stdMsgText(en('svc-a servisinde hata oranı normalde neredeyse hiç yok, şimdi %6,8.'))).toBe('svc-a: error rate normally near zero, now 6.8%.');
    expect(stdMsgText(en('svc-a servisinde hata oranı normalde %2, şimdi %1,5.'))).toBe('svc-a: error rate normally 2%, now 1.5%.');
    expect(stdMsgText(en("Son 5 dakikanın 5'inde eşik aşıldı (en az 3 gerekir)"))).toBe('Threshold exceeded in 5 of the last 5 minutes (at least 3 required)');
    expect(stdMsgText(en('Eşik %1,4: en az %1, normalin 1,5 katı ve 1 puan üstü'))).toBe('Threshold 1.4%: at least 1%, 1.5× normal and 1 pt above normal');
    expect(stdMsgText(en('Yük yeterli: dakikada yaklaşık 3.100 istek (en az 10 gerekir)'))).toBe('Enough load: about 3,100 requests per minute (at least 10 required)');
    expect(stdMsgText(en('Kaynak örneklenmemiş sunucu metriği'))).toBe('Source: unsampled server metric');
    // v0.10.2391 — yanıt süresi + consumer lag "Neden" satırları.
    expect(stdMsgText(en("Son 5 dakikanın 4'ünde medyan eşiği aşıldı (en az 3 gerekir)"))).toBe('Median threshold exceeded in 4 of the last 5 minutes (at least 3 required)');
    expect(stdMsgText(en('Eşik 270 ms: normalin 1,5 katı ve 100 ms üstü'))).toBe('Threshold 270 ms: 1.5× normal and 100 ms above normal');
    expect(stdMsgText(en('Eşik 1,75 sn: normalin 2 katı ve 1 sn üstü'))).toBe('Threshold 1.75 s: 2× normal and 1 s above normal');
    expect(stdMsgText(en('Eşik 1.200 kayıt: en az 1.000 kayıt ve normalin 2 katı'))).toBe('Threshold 1,200 records: at least 1,000 records and 2× normal');
    expect(stdMsgText(en('Dedektör sürdürme koşulunu geçti; log kayıtlarından.'))).toBe('Detector passed its sustain condition; from log records.');
  });
});

describe('S2 — Go kaynağındaki anahtar literalleri katalogda', () => {
  it('stdM / K-argüman / dal anahtarları', () => {
    const src = ['internal/chstore/std_msg.go', 'internal/chstore/problem_standard.go', 'internal/chstore/signal_gates.go',
      'internal/chstore/problem_standard_ml.go'].map(GO).join('\n'); // v0.10.2383
    const keys = new Set<string>();
    for (const x of src.matchAll(/stdM\("([^"]+)"/g)) keys.add(x[1]);
    for (const x of src.matchAll(/"((?:evl|ev\.base|ev\.src|h\.ex|h\.self|metric)\.[\w-]+)"/g)) keys.add(x[1]);
    for (const k of [...keys]) if (k.startsWith('h.ex.')) keys.add(`${k}.reg`);
    const cal = [...GO('internal/chstore/anomaly_calendar.go').matchAll(/Calendar\w+\s+= "([\w-]+)"/g)].map(x => `cal.${x[1]}`);
    expect(cal.length).toBe(3);
    cal.forEach(k => keys.add(k));
    expect(keys.size).toBeGreaterThan(120);
    const missing = [...keys].filter(k => t(`std.m.${k}`, 'en') === `std.m.${k}`);
    expect(missing).toEqual([]);
  });
});

describe('S2 — düşüş', () => {
  it('anahtar yok / bilinmiyor / argüman eksik → sunucu metni + orig (yalnız EN)', () => {
    expect(stdMsgView(undefined, 'Olay: x', 'en')).toEqual({ text: 'Olay: x', orig: true });
    expect(stdMsgView({ k: 'nope' }, 'metin', 'en')).toEqual({ text: 'metin', orig: true });
    expect(stdMsgView({ k: 'h.errUp', a: { sp: '' } }, 'metin', 'en')).toEqual({ text: 'metin', orig: true });
    expect(stdMsgView({ k: 'gate.calendar', a: { calK: 'cal.unknown-kind' } }, 'özel gün: x', 'en').orig).toBe(true);
    expect(stdMsgView(undefined, '', 'en')).toEqual({ text: '', orig: false });
  });
  it('EN manşet Go tavanıyla kırpılır', () => {
    const s = { headline: 'x', headlineMsg: { k: 'h.rule', a: { sp: 'svc-a: ', name: 'n'.repeat(200) } } } as unknown as ProblemStandard;
    const v = stdHeadlineView(s, 'en');
    expect([...v.text].length).toBe(90);
    expect(v.text.endsWith('…')).toBe(true);
  });
  it('bağlam anahtar dizisi eşleşmezse sunucu metni', () => {
    const s = { context: ['a', 'b'], contextMsgs: [{ k: 'ctx.batch' }] } as unknown as ProblemStandard;
    expect(stdContextView(s, 'en')).toEqual({ text: 'a · b', orig: true });
  });
});

// Başlık bloğu fikstürü: Go fikstüründen alınmış mesajlar.
const pick = (field: string, tr: string) => {
  const f = FX.find(x => x.field === field && x.tr === tr);
  if (!f) throw new Error(`fikstürde yok: ${field} ${tr}`);
  return f.msg;
};
const STD: ProblemStandard = {
  family: 'error', kind: 'anomaly.error_rate', subject: 'svc-a',
  headline: 'svc-a: hata oranı arttı — %3,25, normal %0,4', headlineMsg: pick('headline', 'svc-a: hata oranı arttı — %3,25, normal %0,4'),
  impact: { primary: { text: 'hata oranı %3,25 (normal %0,4)', value: 3.25, normal: 0.4, msg: pick('impact', 'hata oranı %3,25 (normal %0,4)') }, known: true },
  since: { startedAt: 0, durationSec: 600, ongoing: true },
  evidence: { sentence: 'Dedektör sürdürme koşulunu geçti; ölçü VictoriaMetrics\'ten (örneklenmemiş).', source: 'vm', sampled: false,
    msg: pick('evidence', 'Dedektör sürdürme koşulunu geçti; ölçü VictoriaMetrics\'ten (örneklenmemiş).') },
  confidence: { level: 'medium', gates: [
    { id: 'G2', name: 'sürdürme', status: 'pass', note: 'dedektörün sürdürme kuralı', noteMsg: pick('gate', 'dedektörün sürdürme kuralı') },
    { id: 'G6', name: 'takvim', status: 'flag', note: 'özel gün: holiday', noteMsg: pick('gate', 'özel gün: holiday') },
  ] },
  nextStep: { text: 'Servisin hata kırılımına bak', href: '/service?name=svc-a', msg: pick('next', 'Servisin hata kırılımına bak') },
  context: ['yakın deploy: svc-a v2', 'takvim: tatil günü', 'batch servis'],
  contextMsgs: [pick('context', 'yakın deploy: svc-a v2'), pick('context', 'takvim: tatil günü'), pick('context', 'batch servis')],
};
const TR_LEFT = ['hata oranı', 'normal %', 'Dedektör', 'örneklenmemiş', 'yakın deploy', 'takvim', 'batch servis', 'kırılım', 'sürdürme', 'özel gün'];

describe('S2 — başlık bloğu', () => {
  let host: HTMLDivElement | null = null;
  let root: Root | null = null;
  afterEach(() => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; });
  const render = async (std: ProblemStandard) => {
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    await act(async () => { root!.render(<MemoryRouter><ProblemStandardBlock std={std} /></MemoryRouter>); });
    return host.textContent ?? '';
  };
  it('EN: kapsanan alanlarda Türkçe yok', async () => {
    m.lang = 'en';
    const txt = await render(STD);
    const title = stdGateTitle(STD, 'en');
    expect(TR_LEFT.filter(w => txt.includes(w) || title.includes(w))).toEqual([]);
    for (const en of ['error rate rose — 3.25%, normal 0.4%', 'error rate 3.25% (normal 0.4%)',
      'Detector passed its sustain condition; measured from VictoriaMetrics (unsampled).',
      'recent deploy: svc-a v2 · calendar: holiday · batch service', "Look at the service's error breakdown"]) expect(txt, en).toContain(en);
    expect(title).toContain("G2 sustained: ✓ — detector's sustain rule");
    expect(title).toContain('G6 calendar: ! — special day: holiday');
  });
  it('EN: anahtarsız manşet (serbest metin) özgün dil işaretiyle', async () => {
    m.lang = 'en';
    await render({ ...STD, subject: '', headline: 'Olay: başlık-1', headlineMsg: undefined });
    expect(host!.querySelector('h1 [lang="tr"]')?.textContent).toBe('Olay: başlık-1TR');
  });
  it('TR: sunucu metni aynen', async () => {
    m.lang = 'tr';
    const txt = await render(STD);
    for (const tr of [STD.impact.primary.text, STD.evidence.sentence, STD.context!.join(' · '), STD.nextStep.text, 'hata oranı arttı — %3,25, normal %0,4'])
      expect(txt, tr).toContain(tr);
    expect(host!.querySelector('[lang="tr"]')).toBeNull();
    expect(stdGateTitle(STD, 'tr')).toContain('G2 sürdürme: ✓ — dedektörün sürdürme kuralı');
  });
});
