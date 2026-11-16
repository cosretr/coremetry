// @vitest-environment jsdom
//
// v0.10.2258 — pin: arayüz dili İngilizceyken Anomalies → Services tablosunda
// bilinen Türkçe etiket ÇİZİLMEZ (sütun başlıkları, durum, sinyal rozeti,
// boş / hata metni, ortak DataTable varsayılan hata metni, Bitenler bölümü);
// Türkçe arayüzde metin aynen kalır. Pod sayfası İngilizce pininin şekli.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import type { UnifiedAnomaliesPage, UnifiedAnomalyRow } from '@/lib/types';

const m = vi.hoisted(() => ({ lang: 'en' as 'en' | 'tr', mode: 'rows' as 'rows' | 'empty' | 'error' }));
vi.mock('@/lib/i18n', async (orig) => ({ ...(await orig<typeof import('@/lib/i18n')>()), useLang: () => m.lang }));

const NOW_NS = Date.now() * 1e6;
const row = (key: string, status: UnifiedAnomalyRow['status'], startMinAgo: number, endMinAgo: number): UnifiedAnomalyRow => ({
  key, service: 'checkout-api', start: NOW_NS - startMinAgo * 60e9, end: NOW_NS - endMinAgo * 60e9,
  signals: ['trace', 'metric'], status, mlCount: 1, legacyCount: 0,
});
// v0.10.2272 — ML lideri olay kaydı taşıyan satır: "Ne oldu" arayüz dilinde
// (kart başlığı); sunucunun Türkçe cümlesi (summary) yalnız yedek.
const SERVER_TR = 'checkout-api: GET /cart yanıt süresi normalin 3 katı, 12 dk (14:00’ten beri).';
function mlRow(rec = true): UnifiedAnomalyRow {
  const v = Array.from({ length: 20 }, (_, i) => (i >= 10 ? 135 : 43));
  const record = { v: 1, subject: { service: 'checkout-api', operation: 'GET /cart', resolved: true },
    series: { source: 'vm', query: 'q', aggregation: 'p95', unit: 'ms', stepSec: 60 },
    baseline: { expected: 43, lo: 31, hi: 58, method: 'm', stepSec: 60 }, direction: 'up',
    onsetAt: NOW_NS - 12 * 60e9, detectedAt: NOW_NS - 10 * 60e9, peakValue: 135, valueAtDetection: 107, expectedValue: 43, volume: 1240,
    evidence: [], class: 'service_anomaly', title: '',
    snapshot: { t0: Math.floor(NOW_NS / 1e9) - 1200, step: 60, aggregation: 'p95', v, lo: v.map(() => 31), hi: v.map(() => 58) } };
  return { ...row(rec ? 'm' : 'm0', 'running', 12, 0), signals: ['trace'],
    mlLead: { id: rec ? 'ev1' : 'ev0', detector: 'op_latency', signal: 'trace', stage: 'shadow', service: 'checkout-api', startedAt: NOW_NS - 12 * 60e9, lastSeen: NOW_NS,
      windowSec: 300, baseline: 43, observed: 135, score: 5, direction: 'up', summary: SERVER_TR, record: rec ? JSON.stringify(record) : undefined } };
}
vi.mock('@/lib/queries', () => ({
  useUnifiedAnomalies: (p: { section: 'open' | 'done' }) => {
    if (m.mode === 'error') return { data: undefined, isLoading: false, isError: true };
    const items = m.mode === 'empty' ? [] : p.section === 'open'
      ? [row('a', 'running', 12, 0), row('b', 'watching', 75, 0), row('c', 'rejected', 30, 20), mlRow()]
      : [row('d', 'done', 90, 60)];
    const data: UnifiedAnomaliesPage = { items, total: items.length, openTotal: 3, doneTotal: m.mode === 'empty' ? 0 : 1, offset: 0, limit: 50, truncated: true, partial: 'legacy' };
    return { data, isLoading: false, isError: false };
  },
}));

import { UnifiedAnomalies } from './UnifiedAnomalies';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; m.lang = 'en'; m.mode = 'rows'; });

function render(): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<MemoryRouter><UnifiedAnomalies onSignal={() => {}} detectorStatus={<span />} /></MemoryRouter>); });
  host.querySelectorAll('details').forEach(d => { d.open = true; });
  return host;
}

const TR_LABELS = [
  'Başlangıç', 'Servis', 'Ne oldu', 'Sinyaller', 'Durum', 'Sürüyor', 'İzleniyor', 'Değil dendi', 'Bitti', 'sürdü', ' dk',
  'Metrik', 'Hepsi', 'Bitenler', 'okunamadı', 'liste eksik', 'kaynak tavanında', 'Teknik ayrıntı', 'Şu an süren', 'boş sonuç değil',
];

describe('Anomalies → Services tablosu — İngilizce arayüz (v0.10.2258)', () => {
  it('satırlı görünüm: Türkçe etiket yok, İngilizce başlıklar var', () => {
    const el = render();
    const text = el.textContent ?? '';
    const leaked = TR_LABELS.filter(l => text.includes(l));
    expect(leaked, `Türkçe etiket İngilizce arayüzde: ${leaked.join(' | ')}`).toEqual([]);
    for (const en of ['Started', 'Service', 'What happened', 'Signals', 'Status', 'Ongoing · 12 min', 'Watching · 1 h 15 min',
      'Marked not an issue', 'Ended (1)', 'Metric', 'All', 'Could not read legacy detector events; the list is incomplete.',
      'The list was cut at the source cap (last 24 hours).', 'Technical detail / Detector status']) {
      expect(text, en).toContain(en);
    }
    expect(el.querySelector('table[aria-label="Ongoing anomalies"]')).not.toBeNull();
    // "Ne oldu": kayıtlı ML liderinde İngilizce kart başlığı, sunucu cümlesi yok.
    expect(text).toContain('GET /cart p95 latency now 135 ms (1,240 calls); expected ≈ 43 ms.');
    expect(text).not.toContain('yanıt süresi normalin');
  });
  it('boş ve hata metni İngilizce', () => {
    m.mode = 'empty';
    let el = render();
    expect(el.textContent).toContain('No ongoing anomalies right now.');
    act(() => root?.unmount()); host?.remove();
    m.mode = 'error';
    el = render();
    expect(el.textContent).toContain('Could not read anomalies');
    expect(el.textContent).not.toContain('okunamadı');
  });
  it('Türkçe arayüzde metin aynen', () => {
    m.lang = 'tr';
    const el = render();
    const text = el.textContent ?? '';
    for (const tr of ['Başlangıç', 'Ne oldu', 'Sürüyor · 12 dk', 'İzleniyor · 1 sa 15 dk', 'Değil dendi', 'Bitenler (1)', 'Metrik', 'Hepsi',
      'eski dedektör olayları okunamadı; liste eksik.', 'Liste kaynak tavanında kesildi (son 24 saat).',
      'GET /cart p95 gecikmesi şu an 135 ms (1.240 çağrı); beklenen ≈ 43 ms.']) {
      expect(text, tr).toContain(tr);
    }
  });
});

describe('ortak DataTable varsayılan hata metni (v0.10.2258)', () => {
  it('İngilizce arayüzde İngilizce, Türkçede aynı cümle', async () => {
    const { DataTableStateRow } = await import('@/components/coremetry/DataTable');
    const { DATA_TABLE_STATE_TEXT } = await import('@/components/ui/DataTable');
    for (const [lang, want] of [['en', 'Could not read the data — this is an error, not an empty result.'], ['tr', DATA_TABLE_STATE_TEXT.error]] as const) {
      m.lang = lang;
      host = document.createElement('div');
      document.body.appendChild(host);
      root = createRoot(host);
      act(() => { root!.render(<table><tbody><DataTableStateRow kind="error" colSpan={3} /></tbody></table>); });
      expect(host.textContent).toContain(want);
      act(() => root?.unmount()); host.remove();
    }
  });
});

describe('"Ne oldu" — kayıt yoksa sunucu cümlesi (v0.10.2272)', () => {
  it('kayıtsız / bozuk / invalid / bilinmeyen ölçü → summary', async () => {
    const { listHeadline } = await import('./unifiedRows');
    const r0 = mlRow(false);
    expect(listHeadline(r0, 'en')).toBe('GET /cart yanıt süresi normalin 3 katı, 12 dk (14:00’ten beri).');
    const bad = mlRow(); bad.mlLead = { ...bad.mlLead!, record: '{"v":1' };
    expect(listHeadline(bad, 'en')).toContain('normalin 3 katı');
    const inv = mlRow(); inv.mlLead = { ...inv.mlLead!, publishState: 'invalid' };
    expect(listHeadline(inv, 'en')).toContain('normalin 3 katı');
    const unk = mlRow(); const j = JSON.parse(unk.mlLead!.record!); j.series.aggregation = 'x'; j.series.source = 'thanos:a';
    unk.mlLead = { ...unk.mlLead!, detector: 'metric_x', record: JSON.stringify(j) };
    expect(listHeadline(unk, 'en')).toContain('normalin 3 katı');
  });
});

// v0.10.2272 — Service Overview «Anomaliler · bu pencere» tablosu (AnomalyWindowTable):
// İngilizce arayüzde Türkçe başlık / rozet / düğme / ipucu / altyazı çizilmez.
describe('AnomalyWindowTable — İngilizce arayüz (v0.10.2272)', () => {
  const S = 1e9;
  const ev = (over: Partial<import('@/lib/types').AnomalyEvent>): import('@/lib/types').AnomalyEvent => ({
    id: 'sha1', kind: 'trace_op_latency', pattern: 'POST /v1/charges', service: 'payments-api',
    startedAt: 1_700_000_000 * S, lastSeen: 1_700_000_600 * S, peakRatio: 0, currentRatio: 3.1, currentCount: 12, sample: '', status: 'active', ...over,
  });
  const sil = (fp: string) => ({ id: 's', fingerprint: fp, kind: 'trace_op', pattern: '', service: '', createdBy: 'ops', createdAt: 1_700_000_000 * S, untilAt: 1_700_600_000 * S, reason: '', active: true });
  async function renderTable(lang: 'en' | 'tr'): Promise<{ text: string; html: string }> {
    m.lang = lang;
    const { AnomalyWindowTable } = await import('@/features/anomalies/AnomalyWindowTable');
    host = document.createElement('div');
    document.body.appendChild(host);
    root = createRoot(host);
    act(() => {
      root!.render(<MemoryRouter><AnomalyWindowTable canEdit truncated onOpen={() => {}} onMute={() => {}} onVerdict={() => {}}
        events={[ev({}), ev({ id: 'v1', kind: 'behavior_change', verdict: 'anomaly' }), ev({ id: 'v2', verdict: 'not_anomaly' }), ev({ id: 'm1', kind: 'ml:op_latency' })]}
        silences={[sil('v2')]} /></MemoryRouter>);
    });
    return { text: host.textContent ?? '', html: host.innerHTML };
  }
  const TR_WINDOW = ['Tür', 'Desen', 'Son görülme', 'Tepe', 'Geri bildirim', 'Eylemler', 'anomali ✓', 'değil ✗', 'sessiz', 'detay',
    'Değil', 'sessize al', 'Anomali', 'gecikme', 'davranış', 'taban yok', 'KESİLDİ', 'olayları', 'çekmece', 'kararı kaydet', 'karne'];
  it('İngilizce: Türkçe metin yok (görünen metin + ipuçları)', async () => {
    const { text, html } = await renderTable('en');
    const leaked = TR_WINDOW.filter(l => text.includes(l) || html.includes(l));
    expect(leaked, `Türkçe metin İngilizce arayüzde: ${leaked.join(' | ')}`).toEqual([]);
    for (const en of ['Kind', 'Pattern', 'Last seen', 'Peak ×', 'Feedback', 'anomaly ✓', 'not an anomaly ✗', 'muted · until',
      '→ details', 'Not one → mute', 'Not one', 'trace_op latency', 'behavior', 'no baseline', 'TRUNCATED, the list is incomplete']) {
      expect(text, en).toContain(en);
    }
    expect(html).toContain('Real anomaly: record the verdict (scorecard label only; does not change detection)');
    expect(html).toContain('Open the ML event page');
  });
  it('Türkçe arayüzde metin aynen', async () => {
    const { text, html } = await renderTable('tr');
    for (const tr of ['Tür', 'Son görülme', 'Geri bildirim', 'anomali ✓', 'değil ✗', "'e dek", '→ detay', 'Değil → sessize al', 'trace_op gecikme', 'taban yok', 'KESİLDİ, liste tam değil']) {
      expect(text, tr).toContain(tr);
    }
    expect(html).toContain('Gerçek anomali: kararı kaydet (yalnız karne etiketi; tespiti değiştirmez)');
  });
});
