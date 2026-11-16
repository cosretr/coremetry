// @vitest-environment jsdom
//
// v0.10.2292 — pin: ortak yardımcıların metni arayüz dilinde. İngilizcede
// sohbet kanıt kartı (EvidenceCard) ve linksiz özne ipucu (SubjectLink)
// bilinen Türkçe etiket ÇİZMEZ; Türkçede metin aynen. Saf yardımcılar
// (gürültü gerekçeleri, özne ipucu, kanıt satırları, aralık, anomali özet
// satırı, AI akışı hata metni, evaluator ağ hatası) lang verilmezse Türkçe.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { AnomalyEvent, ChatEvidence } from '@/lib/types';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr' }));
vi.mock('@/lib/i18n', async (orig) => ({
  ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang, currentLang: () => m.lang,
}));

import { EvidenceCard } from '@/components/ai/EvidenceCard';
import { SubjectLink } from '@/components/SubjectLink';
import { NOISE_REASONS, noiseReasonHint, noiseReasonLabel } from './problemVerdict';
import { subjectTitle } from './problemSubject';
import { fmtDelta, fmtRangeTR, redRows } from './chatEvidence';
import { anomalyPlainSummary } from './anomalyNarrative';
import { t } from './i18n';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ev = (over: Partial<ChatEvidence> = {}): ChatEvidence => ({
  service: 'svc-a', verdict: 'v1', rangeS: 3600, problems: [{ id: 'p1', ruleName: 'r1' }], changes: [], logPatterns: [],
  red: { current: { errorRate: 2.5, p95Ms: 120, rate: 4 }, baseline: { errorRate: 1, p95Ms: 100, rate: 4 } },
  ...over,
} as ChatEvidence);
const pat = { pattern: 'pat-1', currentCount: 10, baselineCount: 2, ratio: 5, kind: 'surge' };

let host: HTMLDivElement | null = null;
let root: Root | null = null;
const close = () => { act(() => { root?.unmount(); }); host?.remove(); host = null; root = null; };
async function mount(node: React.ReactNode) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => { root!.render(<MemoryRouter>{node}</MemoryRouter>); });
  const out = { text: host.textContent ?? '', html: host.innerHTML };
  close();
  return out;
}
async function all() {
  const out = [
    await mount(<EvidenceCard ev={ev()} />),
    await mount(<EvidenceCard ev={ev({ red: { current: { errorRate: 2.5, p95Ms: 120, rate: 4 } }, logPatterns: [pat] } as Partial<ChatEvidence>)} />),
    await mount(<SubjectLink service="db:oracle@db-1" href="" />),
    await mount(<SubjectLink service="ext:src-1/OP1/E1" href="" />),
  ];
  return { text: out.map(r => r.text).join('\n'), html: out.map(r => r.html).join('\n') };
}

beforeEach(() => { m.lang = 'en'; });
afterEach(() => { close(); document.body.innerHTML = ''; });

const TR_LABELS = ['Kanıt', 'son 1 sa', 'şimdi', 'Şimdi', 'Taban', 'taban', 'hata oranı', 'span/sn', 'Değişiklikler', 'kayıtlı değişiklik',
  'Log desenleri', 'patlayan', 'Açık problemler', 'okunamadı', 'veritabanı örneği', 'Dış metrik', 'bir servis değil'];
const leaked = (r: { text: string; html: string }) => TR_LABELS.filter(l => r.text.includes(l) || r.html.includes(l));

describe('Ortak yardımcılar — İngilizce arayüz (v0.10.2292)', () => {
  it('kanıt kartı ve özne ipucu: Türkçe etiket yok', async () => {
    const r = await all();
    expect(leaked(r), `Türkçe etiket İngilizce arayüzde: ${leaked(r).join(' | ')}`).toEqual([]);
    for (const en of ['Evidence · svc-a · last 1 h', 'error rate', '2.5%', 'Now', 'Baseline', 'spans/s', 'Changes (window)',
      'no recorded changes', 'Log patterns', ' · baseline 2, ×5.0', 'baseline unreadable', 'Open problems: 1',
      'RED now / baseline', 'oracle database instance (db-1) — not a service', 'External metric source (src-1) series — not a service'])
      expect(r.text + r.html, en).toContain(en);
  });

  it('saf yardımcılar: lang verilmezse Türkçe aynen', async () => {
    expect(NOISE_REASONS.map(x => x.label)).toEqual(['Beklenen davranış', 'Eşik fazla hassas', 'Bilinen ve kabul edilmiş', 'Yanlış atıf', 'Diğer']);
    expect(NOISE_REASONS.map(x => x.hint)[4]).toBe('not zorunlu');
    expect(noiseReasonLabel('expected')).toBe('Beklenen davranış');
    expect(noiseReasonLabel('expected', 'en')).toBe('Expected behaviour');
    expect(noiseReasonLabel('')).toBe('');
    expect(noiseReasonHint('other', 'en')).toBe('note required');
    expect(subjectTitle('db:oracle@db-1')).toBe('oracle veritabanı örneği (db-1) — bir servis değil, bu yüzden servis sayfası linki yok');
    expect(subjectTitle('svc-a', 'en')).toBeUndefined();
    expect(fmtRangeTR(86400)).toBe('son 1 gün');
    expect(fmtRangeTR(5400)).toBe('son 1.5 sa');
    expect(fmtRangeTR(120, 'en')).toBe('last 2 min');
    expect(fmtDelta(1, 0)).toEqual({ dir: 'new', text: '▲ yeni' });
    expect(fmtDelta(1, 0, 'en')).toEqual({ dir: 'new', text: '▲ new' });
    expect(redRows(ev().red).map(x => `${x.label}=${x.now}`)).toEqual(['hata oranı=%2.5', 'p95=120 ms', 'span/sn=4.0']);
    expect(redRows(ev().red, 'en').map(x => `${x.label}=${x.now}`)).toEqual(['error rate=2.5%', 'p95=120 ms', 'spans/s=4.0']);
    const a = { status: 'active', startedAt: 0, lastSeen: 0, currentCount: 3, currentRatio: 2, kind: 'trace_op', pattern: 'x', service: 'svc-a' } as unknown as AnomalyEvent;
    expect(anomalyPlainSummary(a, 90 * 60e3)).toBe('Sürüyor · 1 sa 30 dk · 3 olay, normalin üstünde');
    expect(anomalyPlainSummary(a, 90 * 60e3, 'en')).toBe('Ongoing · 1 h 30 min · 3 events, above normal');
    expect([t('aiStream.noAnswer', 'tr'), t('aiStream.empty', 'tr'), t('evalHealth.netErr', 'tr')])
      .toEqual(['AI akışı cevapsız kapandı', 'insight akışı boş kapandı', 'Sağlık okunamadı (ağ hatası).']);
    expect(t('aiStream.noAnswer', 'en')).toBe('The AI stream closed without an answer');
  });
});

describe('Ortak yardımcılar — Türkçe arayüzde metin aynen (v0.10.2292)', () => {
  it('kanıt kartı ve özne ipucu', async () => {
    m.lang = 'tr';
    const r = await all();
    for (const tr of ['Kanıt · svc-a · son 1 sa', 'hata oranı', '%2.5', 'Şimdi', 'Taban', 'span/sn', 'Değişiklikler (pencere)',
      'kayıtlı değişiklik yok', 'Log desenleri', ' · taban 2, ×5.0', 'taban okunamadı', 'Açık problemler: 1', 'RED şimdi / taban',
      'oracle veritabanı örneği (db-1) — bir servis değil, bu yüzden servis sayfası linki yok',
      'Dış metrik kaynağı (src-1) serisi — bir servis değil, bu yüzden servis sayfası linki yok'])
      expect(r.text + r.html, tr).toContain(tr);
  });
});
