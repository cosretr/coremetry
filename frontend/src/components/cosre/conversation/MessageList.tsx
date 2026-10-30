// MessageList — v0.10.1650 (CoSRE v2 Faz 2b B2, plan §3 / §10). v2'nin CEVAP
// yolu: AI SDK mesajları v1 balonlarıyla çizilir — operatör aynı sohbeti
// görür, yalnız akış farklıdır.
//   • Asistan metni: visibleParts(m) — akışta reset edilen taslak GİZLİ, bitince
//     kalıcı silinmiş (2a çekirdeği).
//   • v0.10.1656 (B3): asistan mesajı lib/cosre/toChatTurn ile v1 ChatTurn'üne
//     katlanır ve v1 ChatBubble çizer — "Nasıl cevapladım" adım açılırı (akarken
//     açık, bitince kapalı), araç çipleri + şeffaflık paneli, bütçe özeti;
//     durdurma notu ve hata satırı da v1'inki.
//   • v0.10.1659 (B4): exchangeId + cevabın kanıt çipleri (data-evidence-summary);
//     açılan her araç kanıtının altında İlgili / İlgisiz, eylem satırında Not
//     (components/ai/EvidenceRelevance → cosre_signals). 👍/👎 yok.
//   • v0.10.1660 (B5): veri blokları (data-block-* 13 tür → v1 kartları), Aç →
//     link çipleri (data-links + link blokları), Kaynaklar listesi (source-*),
//     kod kilidi (codeRead), takip çipleri (v1 FollowupChips, son cevap),
//     hata satırı + "↺ Yeniden dene" (regenerate), durdurma notu — hepsi v1'in.
//   • Bilinmeyen parça çizilmez (çökmez).
// Sunumsal: Chat örneği yok (katalog ve testler statik mesajla da çizer).
import { useMemo } from 'react';
import { ChatBubble, ChatBubbleSlotsContext, type ChatBubbleSlots } from '@/components/ai/ChatBubble';
import { MessageNote, RelevanceToggle } from '@/components/ai/EvidenceRelevance';
import { AiThinking } from '@/components/ai/AiThinking';
import { FollowupChips } from '@/components/ai/FollowupChips';
import type { ChatSendOpts } from '@/components/ai/useChatThread';
import { FOLLOWUPS } from '@/components/ai/drawerChat';
import { Button } from '@/components/shadcn/button';
import { chatErrorText } from '@/components/ai/chatErrorText';
import { toChatTurn, userTurn } from '@/lib/cosre/toChatTurn';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';
import type { ChatExplainAsk, ChatTurn } from '@/lib/types';

/** useChat durumu (ai ChatStatus ile aynı küme; tip importu katalogdan bağımsız). */
export type CosreChatStatus = 'submitted' | 'streaming' | 'ready' | 'error';
/** v2 sohbet mesajı (katalog / testler lib/cosre'yi doğrudan anmasın diye). */
export type CosreChatMessage = CosreUIMessage;
type Part = CosreUIMessage['parts'][number];

export interface MessageListProps {
  messages: readonly CosreUIMessage[];
  visibleParts: (m: CosreUIMessage) => Part[];
  status: CosreChatStatus;
  error?: Error;
  /** v0.10.1660 (B5) — takip çipi tıkı (v1 askChip: metin + çipin kapsamı). Yoksa çip çizilmez. */
  onAsk?: (text: string, opts?: ChatSendOpts) => void;
  /** v0.10.1660 (B5) — hatalı son turda "↺ Yeniden dene" (SDK regenerate). */
  onRetry?: () => void;
}

export function MessageList({ messages, visibleParts, status, error, onAsk, onRetry }: MessageListProps) {
  const busy = status === 'submitted' || status === 'streaming';
  const last = messages.at(-1);
  const errText = status === 'error' && error ? error.message : undefined;
  return (
    <>
      {messages.map((m, i) => {
        if (m.role === 'user') return <UserMessage key={m.id} m={m} />;
        if (m.role !== 'assistant') return null;
        const isLast = i === messages.length - 1;
        const prev = messages[i - 1];
        return <AssistantMessage key={m.id} m={m} visibleParts={visibleParts} live={isLast && busy} error={isLast ? errText : undefined}
          explainAsk={prev?.role === 'user' ? prev.metadata?.explainAsk : undefined}
          followups={isLast && status === 'ready' ? onAsk : undefined} onRetry={isLast && errText ? onRetry : undefined} />;
      })}
      {/* İlk parça gelmeden (yalnız soru gitti): v1 bekleyiş göstergesi. */}
      {busy && last?.role !== 'assistant' && (last?.metadata?.explainAsk
        // v0.10.1672 (B7) — açıklama turu: v1 gibi baştan künyeli bekleyen balon (ExplainBody yükleniyor).
        ? <ChatBubble turn={{ role: 'assistant', text: '', pending: true, explain: { kind: last.metadata.explainAsk.kind, id: last.metadata.explainAsk.id } }} />
        : <div className="cm-msg cm-msg--ai" data-cosre-thinking=""><AiThinking /></div>
      )}
      {/* Asistan mesajı oluşmadan gelen hata: balon yok, v1 dilinde satır. */}
      {errText && last?.role !== 'assistant' && <ErrorLine message={errText} onRetry={onRetry} />}
    </>
  );
}

function UserMessage({ m }: { m: CosreUIMessage }) {
  const turn = useMemo<ChatTurn>(() => userTurn(m), [m]); // v0.10.1672 (B7) — açıklama niyeti dahil
  return <ChatBubble turn={turn} />;
}

function AssistantMessage({ m, visibleParts, live, error, followups, onRetry, explainAsk }: {
  m: CosreUIMessage; visibleParts: (m: CosreUIMessage) => Part[]; live: boolean; error?: string;
  /** Son, bitmiş cevap: takip çiplerinin sahibi (v1 showFollowups). */
  followups?: (text: string, opts?: ChatSendOpts) => void;
  onRetry?: () => void;
  /** v0.10.1672 (B7) — önceki kullanıcı turunun açıklama niyeti (geçici künye). */
  explainAsk?: ChatExplainAsk;
}) {
  // SDK akışta son mesajı her parçada yeni nesneyle değiştirir; bitmiş
  // mesajlar kimliğini korur → ChatBubble (memo) yalnız akan balonu çizer.
  const turn = useMemo(() => toChatTurn(m, visibleParts(m), { pending: live, error, explainAsk }), [m, visibleParts, live, error, explainAsk]);
  const xid = live ? undefined : turn.exchangeId;
  const slots = useMemo<ChatBubbleSlots>(() => ({
    stepExtra: d => <RelevanceToggle exchangeId={xid} stepId={`s${d.i}`} />,
    actionsExtra: <MessageNote exchangeId={xid} />,
  }), [xid]);
  // v1 showFollowups: bitmiş, hatasız, metinli son cevap.
  const showFu = !!followups && !turn.error && !!turn.text;
  // Açıklama turu baştan ExplainBody çizer (v1: bekleyen künye, kendi yükleniyor'u).
  if (live && !turn.text && !turn.steps?.length && !turn.explain) {
    return <div className="cm-msg cm-msg--ai" data-cosre-thinking=""><AiThinking /></div>;
  }
  return (
    <div data-cosre-message={m.id} className="contents">
      <ChatBubbleSlotsContext.Provider value={slots}>
        <ChatBubble turn={turn} onRetry={onRetry} shownFollowups={showFu ? turn.followups : undefined} />
      </ChatBubbleSlotsContext.Provider>
      {showFu && (
        <div className="cm-followups" data-cosre-followups="">
          <FollowupChips followups={turn.followups} suggestions={turn.suggestions} fallback={FOLLOWUPS}
            exchangeId={turn.exchangeId} onAsk={followups} />
        </div>
      )}
    </div>
  );
}

function ErrorLine({ message, onRetry }: { message: string; onRetry?: () => void }) {
  const ev = chatErrorText(message);
  return (
    <>
      <div className="cm-msg-note text-destructive" role="alert" title={ev.raw ?? undefined}>⚠ {ev.text}</div>
      {onRetry && <Button variant="secondary" size="sm" onClick={onRetry}>↺ Yeniden dene</Button>}
    </>
  );
}
