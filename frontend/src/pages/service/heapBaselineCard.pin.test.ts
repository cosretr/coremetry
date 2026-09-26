// v0.10.887 (paritesi #4 dilim 1) — heap bandı kartı: boş küme kaybolur, bant
// kesikli iki seri, cümle kaynağı/n'i söyler, "problem açmaz" yazar; yalnız JVM.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { heapBandSentence, HEAP_STATUS } from './HeapBaselineCard';
import type { HeapBaselinePod } from '@/lib/types';

const src = readFileSync(resolve(__dirname, 'HeapBaselineCard.tsx'), 'utf8');
const tab = readFileSync(resolve(__dirname, 'ServicePodsTab.tsx'), 'utf8');

describe('HeapBaselineCard — dürüstlük pinleri', () => {
  it('boş küme → kart yok; hata tek satır; bant thresholds ile; odaklı pod hep çizilir; problem açmaz', () => {
    expect(src).toContain('if (!data || pods.length === 0) return null;');
    expect(src).toContain('heap bandı okunamadı:');
    expect(src).toContain('thresholds={thresholds}');
    expect(src).toContain('[focused, ...pods.filter(p => p !== focused)].slice(0, LINES_MAX)');
    expect(src).toContain('rowActivation(() => setFocus(p.pod))');
    // v0.10.933 (tablo standardı T2) — odaklı satır tek seçili görünümle, satır içi bg3 yok.
    expect(src).toContain("className={focused?.pod === p.pod ? 'row-selected' : undefined}");
    expect(src).not.toContain("? 'var(--bg3)'");
    expect(src).toContain('sessiz · ');
    expect(src).toContain('Bu kart problem açmaz.');
    expect(src).toContain('gölge: problem AÇILIRDI'); // v0.10.891 — dedektör hükmü rozeti
    expect(src).toContain("data.mode !== 'off' && data.verdict?.wouldOpen"); // off'ta bayat rozet yok
    expect(src).toContain('fmtClock(data.verdict.at * 1000)'); // 24 sa kilidi
    expect(src).not.toContain('toLocaleTimeString');
    expect(src).toContain("data.mode === 'shadow' || data.mode === 'on'");
    expect(src).toContain("queryKey: ['svc-heap-baseline', service, from, to]");
  });
  it('Pods sekmesinde yalnız JVM ailesi', () => {
    expect(tab).toContain("runtimeFamily === 'jvm' && (");
  });
  it('bant cümlesi: baseline yokken dakika, varken 24s · n', () => {
    const d = { needBuckets: 15, bucketSec: 300, historyHours: 24 };
    const base = { pod: 'p', series: [], lastTs: 0, band: { status: 'ok', current: 60, median: 60, mad: 2, lower: 50, upper: 70, z: 0, n: 210, dwell: 3 } } as HeapBaselinePod;
    expect(heapBandSentence(base, d)).toBe('bant: ardışık 24s · n=210');
    expect(heapBandSentence({ ...base, band: { ...base.band, status: 'no_baseline', n: 4 } }, d)).toBe('baseline yok (< 75 dk veri)');
    expect(heapBandSentence(undefined, d)).toBe('');
    expect(Object.keys(HEAP_STATUS)).toEqual(['critical', 'deviating', 'ok', 'no_baseline']);
  });
});
