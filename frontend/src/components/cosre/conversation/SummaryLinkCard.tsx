// SummaryLinkCard — v0.10.2101 (docs/cosre/experience-audit.md D4, operatör
// onayı 2026-11-13). Cevabın ALTINDA sade kart: başlık (servis / takım /
// kapsam), sunucunun deterministik tek cümlesi, en çok üç sayfa linki. Teknik
// kanıt yok; cümle modelden değil (api chat_summary_card.go). Linkler balonun
// çipleriyle aynı hedef kuralı: iç sayfa, sohbet bağlamı yeni sekme isterse
// yeni sekme (chatLinkTargetProps).
import { Link } from 'react-router-dom';
import { Button } from '@/components/shadcn/button';
import { chatLinkTargetProps, useChatLinkNewTab } from '@/components/ai/chatLinkTarget';
import type { CosreLinkCard } from '@/lib/types';
import { COSRE_V2_STRINGS as T } from './strings';

export function SummaryLinkCard({ card }: { card: CosreLinkCard }) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  return (
    <div className="cm-msg cm-msg--ai" data-cosre-linkcard="">
      <div className="rounded-md border border-border bg-card px-3 py-2 text-sm">
        <div className="font-medium">{card.title}</div>
        <p className="text-muted-foreground">{card.summary}</p>
        <div className="mt-2 flex flex-wrap gap-2" role="group" aria-label={T.linkCardLinks}>
          {card.links.map(l => (
            <Button key={l.href} asChild size="sm" variant="secondary">
              <Link to={l.href} title={l.href} {...linkProps}>{l.label}</Link>
            </Button>
          ))}
        </div>
      </div>
    </div>
  );
}
