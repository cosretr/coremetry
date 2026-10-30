// MessageList — v0.10.1650 (CoSRE v2 Faz 2b B2, plan §3 / §10). v2'nin CEVAP
// yolu: AI SDK mesajları v1 balonlarıyla çizilir — operatör aynı sohbeti
// görür, yalnız akış farklıdır.
//   • Asistan metni: visibleParts(m) — akışta reset edilen taslak GİZLİ, bitince
//     kalıcı silinmiş (2a çekirdeği).
//   • v0.10.1656 (B3): asistan mesajı lib/cosre/toChatTurn ile v1 ChatTurn'üne
//     katlanır ve v1 ChatBubble çizer — "Nasıl cevapladım" adım açılırı (akarken
//     açık, bitince kapalı), araç çipleri + şeffaflık paneli, bütçe özeti;
//     durdurma notu ve hata satırı da v1'inki.
//   • Bilinmeyen parça çizilmez (çökmez).
// Sunumsal: Chat örneği yok (katalog ve testler statik mesajla da çizer).
import { useMemo } from 'react';
import { ChatBubble } from '@/components/ai/ChatBubble';
import { AiThinking } from '@/components/ai/AiThinking';
import { chatErrorText } from '@/components/ai/chatErrorText';
import { messageText } from '@/lib/cosre/cosrePersist';
import { toChatTurn } from '@/lib/cosre/toChatTurn';
import type { CosreUIMessage } from '@/lib/cosre/resetReducer';
import type { ChatTurn } from '@/lib/types';

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
}

export function MessageList({ messages, visibleParts, status, error }: MessageListProps) {
  const busy = status === 'submitted' || status === 'streaming';
  const last = messages.at(-1);
  const errText = status === 'error' && error ? error.message : undefined;
  return (
    <>
      {messages.map((m, i) => {
        if (m.role === 'user') return <UserMessage key={m.id} m={m} />;
        if (m.role !== 'assistant') return null;
        const isLast = i === messages.length - 1;
        return <AssistantMessage key={m.id} m={m} visibleParts={visibleParts} live={isLast && busy} error={isLast ? errText : undefined} />;
      })}
      {/* İlk parça gelmeden (yalnız soru gitti): v1 bekleyiş göstergesi. */}
      {busy && last?.role !== 'assistant' && (
        <div className="cm-msg cm-msg--ai" data-cosre-thinking=""><AiThinking /></div>
      )}
      {/* Asistan mesajı oluşmadan gelen hata: balon yok, v1 dilinde satır. */}
      {errText && last?.role !== 'assistant' && <ErrorLine message={errText} />}
    </>
  );
}

function UserMessage({ m }: { m: CosreUIMessage }) {
  const turn = useMemo<ChatTurn>(() => ({ role: 'user', text: messageText(m) }), [m]);
  return <ChatBubble turn={turn} />;
}

function AssistantMessage({ m, visibleParts, live, error }: {
  m: CosreUIMessage; visibleParts: (m: CosreUIMessage) => Part[]; live: boolean; error?: string;
}) {
  // SDK akışta son mesajı her parçada yeni nesneyle değiştirir; bitmiş
  // mesajlar kimliğini korur → ChatBubble (memo) yalnız akan balonu çizer.
  const turn = useMemo(() => toChatTurn(m, visibleParts(m), { pending: live, error }), [m, visibleParts, live, error]);
  if (live && !turn.text && !turn.steps?.length) {
    return <div className="cm-msg cm-msg--ai" data-cosre-thinking=""><AiThinking /></div>;
  }
  return (
    <div data-cosre-message={m.id} className="contents">
      <ChatBubble turn={turn} />
    </div>
  );
}

function ErrorLine({ message }: { message: string }) {
  const ev = chatErrorText(message);
  return (
    <div className="cm-msg-note text-destructive" role="alert" title={ev.raw ?? undefined}>⚠ {ev.text}</div>
  );
}
