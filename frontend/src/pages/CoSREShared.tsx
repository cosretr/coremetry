// CoSREShared — v0.10.1167 — paylaşılan CoSRE sohbeti, SALT-OKUNUR.
// /cosre/shared/:id?t=<sır>. Bağlantıyı alan her kimlikli kullanıcı (viewer
// dahil) okur; yazma / devam ettirme yok. Yanlış ya da geri alınmış bağlantı
// 404 → "bulunamadı" (varlık doğrulanmaz).
import { useParams, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { PageShell } from '@/components/coremetry/PageShell';
import { Spinner, Empty } from '@/components/Spinner';
import { ChatMarkdown } from '@/components/ai/ChatBubble';
import { api } from '@/lib/api';
import { fmtDateTime } from '@/lib/utils';

export default function CoSREShared() {
  const { id = '' } = useParams();
  const [params] = useSearchParams();
  const token = params.get('t') ?? '';
  const q = useQuery({
    queryKey: ['ai-shared-conversation', id, token],
    queryFn: () => api.sharedAiConversation(id, token),
    enabled: !!id && !!token,
    retry: false,
  });
  return (
    <>
      <Topbar title="Paylaşılan CoSRE sohbeti" />
      <PageShell>
        {!id || !token ? (
          <Empty icon="🔗" title="Bağlantı eksik">Paylaşım bağlantısı kimlik ve sır taşımalı.</Empty>
        ) : q.isPending ? (
          <Spinner />
        ) : q.isError || !q.data ? (
          <Empty icon="🔒" title="Sohbet bulunamadı">Bağlantı geçersiz ya da paylaşım geri alınmış.</Empty>
        ) : q.data.messages.length === 0 ? (
          <Empty icon="💬" title="Sohbet boş" />
        ) : (
          <div data-readonly="true">
            <div style={{ marginBottom: 12 }}>
              <div style={{ fontSize: 16, fontWeight: 600 }}>{q.data.title}</div>
              <div style={{ fontSize: 12, color: 'var(--text3)' }}>
                Salt-okunur · son etkinlik {fmtDateTime(Math.floor(q.data.updatedAt / 1e6))}
              </div>
            </div>
            {q.data.messages.map((m, i) => (
              <div key={i}
                style={{ padding: '8px 12px', marginBottom: 8, borderRadius: 8, border: '1px solid var(--border)', background: m.role === 'user' ? 'var(--bg2)' : 'transparent' }}>
                <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 4 }}>{m.role === 'user' ? 'Soru' : 'CoSRE'}</div>
                <ChatMarkdown text={m.text} />
              </div>
            ))}
          </div>
        )}
      </PageShell>
    </>
  );
}
