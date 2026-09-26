// ExplainEvidence — v0.10.948 (CoSRE Faz B): "CoSRE'ye sor" cevap kartının
// iki deterministik alt satırı. İkisi de SUNUCUDAN gelir, modelin metninden
// asla türetilmez:
//   - ExplainEvidenceLinks: sunucunun gerçek kayıtlardan ve VAR OLAN rotalardan
//     kurduğu göreli kanıt linkleri (/trace, /logs, /service, /traces …).
//     Sohbet balonunun "Aç →" satırıyla aynı atom (.ai-links / .ai-link):
//     iç link SPA <Link>, http(s) yeni sekme.
//   - ExplainSourceFooter: cevap çerçevesinin `sources`u — her kaynak ve
//     durumu (ok dahil, nötr); ok dışındakiler (boş dahil) ayrıca "eksik veri" diye
//     sayılır. Erişilemeyen kaynak "boş" görünmez (Faz A sözleşmesi).
import { Link } from 'react-router-dom';
import type { AIAnswerLink, ExplainSourceStatus } from '@/lib/types';
import { evidenceLinks, isInternalHref, missingSources, sourceFooterItems } from './investigationSteps';

export function ExplainEvidenceLinks({ links }: { links?: AIAnswerLink[] }) {
  const ev = evidenceLinks(links);
  if (ev.length === 0) return null;
  return (
    <div className="ai-links" role="group" aria-label="Kanıt linkleri">
      <span className="ai-links__cap">Kanıt →</span>
      {ev.map(l => (isInternalHref(l.href) ? (
        <Link key={l.href} to={l.href} className="ai-link" title={l.href}>↗ {l.label}</Link>
      ) : (
        <a key={l.href} href={l.href} target="_blank" rel="noopener noreferrer" className="ai-link" title={l.href}>🔗 {l.label}</a>
      )))}
    </div>
  );
}

export function ExplainSourceFooter({ sources }: { sources?: ExplainSourceStatus[] }) {
  const items = sourceFooterItems(sources);
  if (items.length === 0) return null;
  const missing = missingSources(items);
  return (
    <div className="cx-sources" role="group" aria-label="Kaynak durumu">
      <span className="cx-verdict-k">Kaynak durumu</span>
      {items.map((it, k) => (
        <span key={k} className={`badge b-${it.tone}`} title={it.title}>{it.name} · {it.label}</span>
      ))}
      {missing.length > 0 && (
        <span className="field-hint" title="Bu kaynaklardan kanıt yok ya da eksik: cevap bu kaynakların sorularını cevapsız bırakır">
          · eksik veri: {missing.join(', ')}
        </span>
      )}
    </div>
  );
}
