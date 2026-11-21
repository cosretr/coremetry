// pageStarters — wave 3 (CoSRE yan paneli, Datadog Bits "page context +
// starter prompts" emsali). SAF: sayfa bağlamı (lib/pageContext — URL'den
// çözülmüş varlık: servis / endpoint / pod / trace / problem) → o sayfa
// türüne 2–4 hazır soru. Varlık adı bağlamın KENDİSİNDEN gelir (ad eşleme
// yok); soru zaten her turda giden `context.page` ile birlikte yorumlanır.
// Bağlamı olmayan sayfa → []: karşılama yalnız genel sekmeleri gösterir.
// Trace / exception "açıkla" çipi ayrıca (lib/chatContext contextStarter).
import type { PageContext } from '@/lib/types';

export interface PageStarter { id: string; question: string }

/** Sayfa başına tavan (karşılamanın "Araştır" sekmesi ≤4 çip taşır). */
export const PAGE_STARTER_MAX = 4;

type T = (key: string) => string;
const fill = (s: string, vars: Record<string, string>) =>
  s.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? vars[k] : m));

export function pageStarters(page: PageContext, t: T): PageStarter[] {
  const q = (id: string, vars: Record<string, string> = {}): PageStarter =>
    ({ id, question: fill(t(`cosre.starter.${id}`), vars) });
  const svc = page.service ?? '';
  const out: PageStarter[] = [];
  switch (page.page) {
    case 'service': case 'service-backtrace':
      if (svc) out.push(q('service.slow', { svc }), q('service.errors', { svc }), q('service.deploy', { svc }));
      break;
    case 'endpoint':
      if (page.operation) {
        const op = svc ? `${svc} ${page.operation}` : page.operation;
        out.push(q('endpoint.latency', { op }), q('endpoint.errors', { op }));
      }
      break;
    case 'pod': case 'entity':
      if (page.pod) out.push(q('pod.health', { pod: page.pod }), q('pod.restarts', { pod: page.pod }));
      break;
    case 'trace':
      if (page.traceId) out.push(q('trace.slowest'));
      break;
    case 'problems': case 'anomalies': case 'inbox': case 'incident':
      if (page.problemId) out.push(q('problem.impact'));
      break;
    case 'logs':
      out.push(q('logs.errors'), q('logs.spike'));
      break;
    case 'endpoints':
      out.push(q('endpoints.worst'), q('endpoints.errors'));
      break;
    case 'services':
      out.push(q('services.worst'));
      break;
    case 'traces':
      out.push(q('traces.slow'));
      break;
    default:
      break;
  }
  return out.slice(0, PAGE_STARTER_MAX);
}
