// CosrePageLayout — v0.10.1785 (CoSRE v2 tam sayfası, mockup A, operatör onayı
// 2026-11-05). /cosre bayrak açıkken (cosreV2) CosreChatV2 variant="page" bu
// kabuğu çizer: üst şerit (☰ · CoSRE · kapsam pili · Coremetry'yi aç · tema),
// solda mevcut CosreSidebar (v1 sayfasıyla aynı bileşen, veri useChatHistory),
// ortada CosreChatV2'nin gövdesi (Sheet'siz). Sunumsal: state çağıranda.
// v1 sayfası (CopilotChat variant="page") bu dosyayı KULLANMAZ — bayrak
// kapalıyken yol birebir eskisi.
// components/ui'den HİÇBİR şey içe aktarılmaz (uiImportRatchet, ui/ donmuş).
import { useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import type { AiConversationSummary } from '@/lib/types';
import { Button } from '@/components/shadcn/button';
import { AiMark } from '@/components/ai/CopilotFab';
import { CosreSidebar } from '@/components/ai/CosreSidebar';
import { ChatLinkNewTabContext } from '@/components/ai/chatLinkTarget';
import { readSidebarCollapsed, writeSidebarCollapsed } from '@/components/ai/chatHistoryGroups';
import type { ChatHistory } from '@/components/ai/useChatHistory';
import { ThemeToggle } from '@/components/ThemeToggle';
import { COSRE_PAGE_STRINGS as T } from './strings';

/** Telefon eşiği — CosreSidebar'ın çekmece kipi (globals.css @media 640px) ile aynı. */
const NARROW_MQ = '(max-width: 640px)';

export interface CosrePageLayoutProps {
  /** Üst şeritte kapsam pili (son cevabın data-scope'u); yoksa çizilmez. */
  headerBadge?: ReactNode;
  history: ChatHistory;
  activeId: string | null;
  onNew(): void;
  onOpen(t: AiConversationSummary): void;
  onDelete(t: AiConversationSummary): void;
  /** Gövde: mesajlar + composer (CosreChatV2 bodyNode). */
  children: ReactNode;
}

export function CosrePageLayout({ headerBadge, history, activeId, onNew, onOpen, onDelete, children }: CosrePageLayoutProps) {
  const [collapsed, setCollapsed] = useState<boolean>(() => readSidebarCollapsed());
  const [mobileOpen, setMobileOpen] = useState(false);
  const toggle = () => {
    const narrow = typeof window.matchMedia === 'function' && window.matchMedia(NARROW_MQ).matches;
    if (narrow) { setMobileOpen(v => !v); return; }
    setCollapsed(v => { writeSidebarCollapsed(!v); return !v; });
  };
  return (
    <ChatLinkNewTabContext.Provider value>
      <div className="cosre-page cosre-root cosre-root--page" data-cosre-v2="">
        <header className="cosre-page__head cosre-head">
          <Button variant="ghost" size="sm" className="cosre-page__menu" onClick={toggle}
            aria-label={collapsed ? T.sideShow : T.sideHide}
            aria-controls="cosre-side" aria-expanded={mobileOpen || !collapsed}>☰</Button>
          <span className="cosre-head__brand">
            <AiMark size={20} />
            <span className="cosre-head__title">{T.title}</span>
          </span>
          <span className="flex-1" />
          {headerBadge}
          <Link to="/" className="cosre-page__home" title={T.homeTitle}>{T.home}</Link>
          <ThemeToggle />
        </header>
        <div className="cosre-page__main">
          <CosreSidebar threads={history.threads} error={history.error} activeId={activeId}
            collapsed={collapsed} mobileOpen={mobileOpen} groupBy="day"
            onNew={() => { onNew(); setMobileOpen(false); }}
            onOpen={t => { setMobileOpen(false); onOpen(t); }}
            onDelete={onDelete}
            onShare={t => void history.share(t)}
            onCloseMobile={() => setMobileOpen(false)} />
          <main className="cosre-page__body">
            {children}
          </main>
        </div>
      </div>
    </ChatLinkNewTabContext.Provider>
  );
}
