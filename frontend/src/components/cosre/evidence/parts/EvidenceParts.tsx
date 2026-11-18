// EvidenceParts — v0.10.2363 (investigation-design S5). MessageList'in kanıt
// bloklarını (exception / trace / logs / anomaly) çizdiği TEK statik giriş;
// her kart kendi tembel chunk'ında (v2 sohbet chunk'ı yalnız bu dağıtıcıyı
// taşır). v1 ChatBubble bu tipleri çizmez; bilinmeyen tip burada da çizilmez.
import { lazy, Suspense } from 'react';
import type { EvidencePart } from '@/lib/cosre/evidenceParts';

const ExceptionPartCard = lazy(() => import('./ExceptionPartCard').then(m => ({ default: m.ExceptionPartCard })));
const TracePartCard = lazy(() => import('./TracePartCard').then(m => ({ default: m.TracePartCard })));
const LogsPartCard = lazy(() => import('./LogsPartCard').then(m => ({ default: m.LogsPartCard })));
const AnomalyPartCard = lazy(() => import('./AnomalyPartCard').then(m => ({ default: m.AnomalyPartCard })));

function One({ part }: { part: EvidencePart }) {
  switch (part.type) {
    case 'exception': return <ExceptionPartCard p={part.payload} />;
    case 'trace': return <TracePartCard p={part.payload} />;
    case 'logs': return <LogsPartCard p={part.payload} />;
    case 'anomaly': return <AnomalyPartCard p={part.payload} />;
  }
}

export function EvidenceParts({ parts }: { parts: readonly EvidencePart[] }) {
  if (!parts.length) return null;
  return (
    <Suspense fallback={null}>
      {parts.map(p => <One key={p.id} part={p} />)}
    </Suspense>
  );
}
