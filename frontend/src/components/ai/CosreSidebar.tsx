import { useMemo, useState } from 'react';
import type { AiConversationSummary } from '@/lib/types';
import { Button } from '@/components/ui/Button';
import { IconButton } from '@/components/ui/IconButton';
import { SearchField } from '@/components/ui/SearchField';
import { Modal } from '@/components/ui/Modal';
import { Empty, Spinner } from '@/components/Spinner';
import { useAuthRole } from '@/components/AuthProvider';
import { groupConversations, groupConversationsByDay, sidebarTitle } from './chatHistoryGroups';
import { CosreRulesPanel } from './CosreRulesPanel';

// CosreSidebar — v0.10.1137 (operatör: "geçmiş Claude'daki gibi solda"): /cosre
// sayfasının kalıcı sol kenar çubuğu. Veri, olay ve silme yolu "🕘 Geçmiş"
// bölümüyle AYNI (CopilotChat threads/openThread/removeThread → GET/DELETE
// /api/ai/conversations); yeniden adlandırma ucu yok, o yüzden menüde yalnız
// silme var (yeni uç icat edilmedi). Çekmece (uygulama içi) kompakt Geçmiş
// düğmesini korur — kenar çubuğu yalnız sayfa kipinde.
//
// Daraltma tercihi localStorage'da (chatHistoryGroups read/write, try/catch);
// telefon genişliğinde (≤640px) çubuk ekran dışı çekmecedir — başlıktaki ☰
// açar, örtü / seçim / Esc kapatır.
//
// v0.10.1142 (operatör: "geçmiş yazısı çok küçük, gerçek bir sohbet asistanı
// gibi") — tipografi .cosre-root'taki --cosre-fs-* ölçeğinden: satır başlığı
// 14px (uzunsa ellipsis + title ipucu), grup başlığı 12px soluk meta ve
// kaydırırken yapışkan, satır yüksekliği --cosre-row-h (38px). Kurallar
// globals.css "CoSRE tip ölçeği" bölümünde.
export function CosreSidebar({
  threads, error, activeId, collapsed, mobileOpen, onNew, onOpen, onDelete, onShare, onCloseMobile, nowMs, groupBy = 'recency', timeZone,
}: {
  threads: AiConversationSummary[] | undefined;
  error?: string;
  activeId: string | null;
  collapsed: boolean;
  mobileOpen: boolean;
  onNew: () => void;
  onOpen: (t: AiConversationSummary) => void;
  onDelete: (t: AiConversationSummary) => void;
  /** v0.10.1167 — salt-okunur paylaşım bağlantısı (verilmezse düğme yok). */
  onShare?: (t: AiConversationSummary) => void;
  onCloseMobile: () => void;
  /** test dikişi — gruplamanın "şimdi"si */
  nowMs?: number;
  /** v0.10.1788 — 'day': Bugün / Dün / tarih (CoSRE v2 sayfası); varsayılan v1 kovaları. */
  groupBy?: 'recency' | 'day';
  /** test dikişi — gün grubunun saat dilimi (varsayılan tarayıcı = operatör dilimi). */
  timeZone?: string;
}) {
  const [q, setQ] = useState('');
  // v0.10.1289 — "Kurallar" modalı (her rol; viewer salt-okunur). Panel ve
  // okuması YALNIZ modal açıkken bağlanır (aç-üzerine-getir).
  const [rulesOpen, setRulesOpen] = useState(false);
  const role = useAuthRole();
  const canEditRules = role === 'admin' || role === 'editor';
  const groups = useMemo(() => {
    const now = nowMs ?? Date.now();
    return groupBy === 'day' ? groupConversationsByDay(threads, now, q, timeZone) : groupConversations(threads, now, q);
  }, [threads, nowMs, q, groupBy, timeZone]);
  const cls = ['cosre-side', collapsed ? 'is-collapsed' : '', mobileOpen ? 'is-open' : ''].filter(Boolean).join(' ');
  return (
    <>
      {mobileOpen && <div className="cosre-side__scrim" aria-hidden="true" onClick={onCloseMobile} />}
      <nav className={cls} aria-label="Konuşmalar" id="cosre-side"
        onKeyDown={e => { if (e.key === 'Escape' && mobileOpen) { e.stopPropagation(); onCloseMobile(); } }}>
        <div className="cosre-side__top">
          <Button variant="secondary" size="sm" className="cosre-side__new" onClick={onNew}
            title="Ekranı boşalt, yeni bir konuşma başlat">+ Yeni sohbet</Button>
          {mobileOpen && (
            <IconButton variant="ghost" size="sm" aria-label="Konuşma listesini kapat" icon="✕" onClick={onCloseMobile} />
          )}
        </div>
        <div className="cosre-side__search">
          <SearchField value={q} onChange={setQ} placeholder="Konuşmalarda ara…" aria-label="Konuşma başlıklarında ara" />
        </div>
        <div className="cosre-side__list">
          {threads === undefined && <div className="cosre-side__state"><Spinner label="Konuşmalar yükleniyor…" /></div>}
          {error && <div className="cosre-side__state"><span className="badge b-err" title={error}>Geçmiş okunamadı</span></div>}
          {threads !== undefined && !error && groups.length === 0 && (
            <Empty icon="🕘" compact title={q ? 'Eşleşen konuşma yok' : 'Kayıtlı konuşma yok'}>
              {q ? 'Başka bir sözcükle ara.' : 'Bir soru sorduğunda konuşma otomatik saklanır.'}
            </Empty>
          )}
          {groups.map(g => (
            <section key={g.label} className="cosre-side__group" aria-label={g.label}>
              <h2 className="cosre-side__gh">{g.label}</h2>
              <ul>
                {g.items.map(t => (
                  <li key={t.id} className={t.id === activeId ? 'cosre-side__item is-active' : 'cosre-side__item'}>
                    <Button variant="ghost" size="sm" className="cosre-side__open"
                      aria-current={t.id === activeId ? 'true' : undefined}
                      onClick={() => onOpen(t)} title={t.title}>
                      <span className="cosre-side__title">{sidebarTitle(t.title)}</span>
                    </Button>
                    {onShare && (
                      <Button variant="ghost" size="xs" className="cosre-side__del"
                        onClick={() => onShare(t)} aria-label={`${t.title} konuşmasını paylaş`} title="Salt-okunur bağlantıyı kopyala">🔗</Button>
                    )}
                    <Button variant="ghost-danger" size="xs" className="cosre-side__del"
                      onClick={() => onDelete(t)} aria-label={`${t.title} konuşmasını sil`} title="Konuşmayı sil">✕</Button>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
        <div className="cosre-side__foot">
          <Button variant="ghost" size="sm" className="cosre-side__rules" onClick={() => setRulesOpen(true)}
            title="CoSRE'nin hatırladığı operatör kuralları">🧠 Kurallar</Button>
        </div>
      </nav>
      <Modal open={rulesOpen} onClose={() => setRulesOpen(false)} title="CoSRE kuralları" size="lg">
        {rulesOpen && <CosreRulesPanel canEdit={canEditRules} storageKey="cosre-page-rules" />}
      </Modal>
    </>
  );
}
