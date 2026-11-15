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
vi.mock('@/lib/queries', () => ({
  useUnifiedAnomalies: (p: { section: 'open' | 'done' }) => {
    if (m.mode === 'error') return { data: undefined, isLoading: false, isError: true };
    const items = m.mode === 'empty' ? [] : p.section === 'open'
      ? [row('a', 'running', 12, 0), row('b', 'watching', 75, 0), row('c', 'rejected', 30, 20)]
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
      'eski dedektör olayları okunamadı; liste eksik.', 'Liste kaynak tavanında kesildi (son 24 saat).']) {
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
