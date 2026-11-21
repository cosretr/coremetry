// useCriticalSegments — Dalga 3 (2026-11-21): kritik yol kesitlerini Web
// Worker'da hesaplar (lib/criticalSegments.worker.ts). Worker yoksa (jsdom /
// eski ortam) ya da hata verirse aynı saf fonksiyon eşzamanlı koşar — sonuç
// aynı, yalnız yer farklı. undefined = hesaplanıyor; trace değişince eski
// sonuç düşer, temizlenmiş efektin geç yanıtı yok sayılır.
import { useEffect, useState } from 'react';
import { computeCriticalSegments, segmentsFromWire, type CriticalSegments, type SegWire } from './criticalSegments';
import type { SpanRow } from './types';

const toSeg = (spans: readonly SpanRow[]) => computeCriticalSegments(spans.map(s => ({
  spanId: s.spanId, parentId: s.parentSpanId ?? '', startTime: s.startTime, endTime: s.endTime,
})));

export function useCriticalSegments(spans: readonly SpanRow[] | null | undefined): CriticalSegments | undefined {
  const [state, setState] = useState<{ key: readonly SpanRow[]; seg: CriticalSegments } | null>(null);
  useEffect(() => {
    if (!spans || spans.length === 0) return;
    if (typeof Worker === 'undefined') { setState({ key: spans, seg: toSeg(spans) }); return; }
    let w: Worker;
    try {
      w = new Worker(new URL('./criticalSegments.worker.ts', import.meta.url), { type: 'module' });
    } catch {
      setState({ key: spans, seg: toSeg(spans) });
      return;
    }
    let live = true;
    w.onmessage = (e: MessageEvent<{ wire: SegWire; ms: number }>) => {
      if (live) setState({ key: spans, seg: segmentsFromWire(e.data.wire) });
      w.terminate();
    };
    w.onerror = () => {
      if (live) setState({ key: spans, seg: toSeg(spans) });
      w.terminate();
    };
    const n = spans.length;
    const ids = new Array<string>(n), parents = new Array<string>(n);
    const start = new Float64Array(n), end = new Float64Array(n);
    for (let i = 0; i < n; i++) {
      const s = spans[i];
      ids[i] = s.spanId; parents[i] = s.parentSpanId ?? ''; start[i] = s.startTime; end[i] = s.endTime;
    }
    w.postMessage({ ids, parents, start, end }, [start.buffer, end.buffer]);
    return () => { live = false; w.terminate(); };
  }, [spans]);
  return state && state.key === spans ? state.seg : undefined;
}
