// PartShell — v0.10.2363 (investigation-design S5). Dört kanıt kartının ortak
// kabuğu: SummaryLinkCard'ın kart dili (başlık satırı + gövde + "Sayfada aç"
// linki). Link hedef kuralı balonun çipleriyle aynı (chatLinkTargetProps).
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { chatLinkTargetProps, useChatLinkNewTab } from '@/components/ai/chatLinkTarget';
import { EVIDENCE_PART_STRINGS as T } from './strings';

export function PartShell({ kind, title, meta, href, children }: {
  kind: string; title: ReactNode; meta?: ReactNode; href: string; children?: ReactNode;
}) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  return (
    <div className="cm-msg cm-msg--ai" data-cosre-evidence-part={kind}>
      <div className="rounded-md border border-border bg-card px-3 py-2 text-sm">
        <div className="flex flex-wrap items-baseline gap-x-2">
          <span className="min-w-0 font-medium break-words">{title}</span>
          {meta && <span className="text-muted-foreground">{meta}</span>}
        </div>
        {children}
        <div className="mt-1">
          <Link to={href} title={href} data-part-href="" {...linkProps}>{T.open}</Link>
        </div>
      </div>
    </div>
  );
}
