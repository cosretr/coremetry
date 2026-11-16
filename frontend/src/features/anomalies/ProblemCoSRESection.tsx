// ProblemCoSRESection — v0.10.1168 — problem detayında bu probleme bağlı
// CoSRE sohbetleri (çağıranın KENDİ konuşmaları; bağ konuşmanın bağlamından
// türer ya da elle kurulur — internal/api/ai_conversation_link.go). Satır
// /cosre?chat=<id> ile konuşmayı açar. Kapalı başlar, açılınca okunur.
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { Spinner, Empty } from '@/components/Spinner';
import { tsRel } from '@/lib/utils';
import { Sect } from './detailSections';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2284 — metinler arayüz dilinde (TR aynı)

export function ProblemCoSRESection({ problemId }: { problemId: string }) {
  const lang = useLang();
  return (
    <Sect title={t('pd.cosre', lang)} sub={t('pd.cosre.sub', lang)} collapsible defaultOpen={false}>
      <ConversationList problemId={problemId} />
      <NoteList problemId={problemId} />
    </Sect>
  );
}

function ConversationList({ problemId }: { problemId: string }) {
  const lang = useLang();
  const q = useQuery({
    queryKey: ['problem-conversations', problemId],
    queryFn: () => api.problemConversations(problemId),
    staleTime: 30_000,
  });
  if (q.isPending) return <Spinner />;
  if (q.isError) return <Empty icon="⚠" title={t('pd.cosre.err', lang)} compact>{q.error instanceof Error ? q.error.message : ''}</Empty>;
  if (!q.data?.length) {
    return <Empty icon="💬" title={t('pd.cosre.empty', lang)} compact>{t('pd.cosre.emptyBody', lang)}</Empty>;
  }
  return (
    <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 4 }}>
      {q.data.map(c => (
        <li key={c.id} style={{ display: 'flex', gap: 8, alignItems: 'baseline', fontSize: 13 }}>
          <Link to={`/cosre?chat=${encodeURIComponent(c.id)}`}>{c.title}</Link>
          <span style={{ color: 'var(--text3)', fontSize: 11 }}>{tsRel(c.updatedAt)} · {tf('pd.cosre.msgs', lang, { n: c.messages, c: c.messages })}</span>
        </li>
      ))}
    </ul>
  );
}

// v0.10.1169 — CoSRE'den kaydedilen bulgular (ekipçe görünür notlar).
function NoteList({ problemId }: { problemId: string }) {
  const lang = useLang();
  const q = useQuery({
    queryKey: ['problem-notes', problemId],
    queryFn: () => api.problemNotes(problemId),
    staleTime: 30_000,
  });
  if (q.isPending) return <Spinner />;
  if (q.isError) return <Empty icon="⚠" title={t('pd.cosre.notesErr', lang)} compact>{q.error instanceof Error ? q.error.message : ''}</Empty>;
  if (!q.data?.length) return null;
  return (
    <div style={{ marginTop: 10 }}>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginBottom: 4 }}>{t('pd.cosre.notes', lang)}</div>
      <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: 6 }}>
        {q.data.map(n => (
          <li key={n.id} style={{ fontSize: 12.5, borderLeft: '2px solid var(--border)', paddingLeft: 8 }}>
            <div style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{n.text}</div>
            <div style={{ color: 'var(--text3)', fontSize: 11 }}>{n.by ? `${n.by} · ` : ''}{tsRel(n.at)}</div>
          </li>
        ))}
      </ul>
    </div>
  );
}
