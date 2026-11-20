// ProblemInsightStrip.tsx — v0.10.562 (CoSRE Faz 5b; operatör mockup onayı).
// Problem detayının ilk ekranının ÜSTÜNDE tek satır: şüpheli · ilk anomali ·
// rollout · daha önce. Deterministik (LLM yok), sormadan dolar; hücre
// bilinmiyorsa '—'. Tek-varlık skor şeridi kuralı (detay sayfası) ile uyumlu.
import { Link } from 'react-router';
import { serviceHref } from '@/lib/serviceHref';
import { useProblemInsight } from '@/lib/queries/problems';
import { insightCells } from './problemInsight';
import { useLang } from '@/lib/i18n'; // v0.10.2290

export function ProblemInsightStrip({ problemId }: { problemId: string }) {
  const q = useProblemInsight(problemId);
  const lang = useLang();
  if (q.isPending) return <div className="ins-strip ins-strip--pending" aria-busy="true">insight…</div>;
  if (q.isError || !q.data) return null; // şerit ek bilgi: hata sayfayı bozmaz
  const cells = insightCells(q.data, s => serviceHref(s), lang);
  return (
    <div className="ins-strip" role="group" aria-label="Problem insight" title={q.data.note || undefined}>
      {cells.map(c => (
        <span key={c.key} className={`ins-cell${c.tone ? ` ins-cell--${c.tone}` : ''}`}>
          <span className="ins-label">{c.label}</span>
          {c.href ? <Link to={c.href} className="ins-val">{c.text}</Link> : <span className="ins-val">{c.text}</span>}
        </span>
      ))}
    </div>
  );
}
