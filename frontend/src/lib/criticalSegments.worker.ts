// criticalSegments.worker.ts — Dalga 3 (2026-11-21): kritik yol kesitleri ana
// iş parçacığının DIŞINDA (10k+ span'lik trace'te sıralama + yürüyüş UI'ı
// kilitlemesin). Uygulamanın ilk worker'ı; Vite `new Worker(new URL(…,
// import.meta.url), { type: 'module' })` sözdizimiyle ayrı chunk olarak
// paketler (ilk JS bütçesine girmez). Giriş: düz diziler; çıkış: SegWire
// (Float64Array/Uint32Array aktarılır — kopya yok).
import { computeCriticalSegments, segmentsToWire, type SegSpan } from './criticalSegments';

export interface SegRequest { ids: string[]; parents: string[]; start: Float64Array; end: Float64Array }

const ctx = self as unknown as {
  onmessage: ((e: MessageEvent<SegRequest>) => void) | null;
  postMessage: (msg: unknown, transfer: Transferable[]) => void;
};

ctx.onmessage = (e) => {
  const { ids, parents, start, end } = e.data;
  const t0 = performance.now();
  const spans: SegSpan[] = new Array(ids.length);
  for (let i = 0; i < ids.length; i++) {
    spans[i] = { spanId: ids[i], parentId: parents[i], startTime: start[i], endTime: end[i] };
  }
  const wire = segmentsToWire(computeCriticalSegments(spans));
  ctx.postMessage({ wire, ms: performance.now() - t0 }, [wire.seg.buffer, wire.off.buffer]);
};
