// MessageTurnActions — v0.10.2472: CoSRE v2 sohbetinin tur eylemleri.
//   • RegenerateButton — her bitmiş asistan cevabının eylem satırında (Kopyala /
//     Not yanında) "Yeniden üret": aynı kullanıcı turu yeniden gider, bu cevabın
//     yerine yenisi gelir (SDK regenerate; attempt 'regenerate').
//   • EditableUserMessage — son kullanıcı mesajının altında "Düzenle": satır içi
//     düzenleyici; gönderince soru yerinde değişir, sonraki cevaplar düşer
//     (SDK sendMessage messageId; attempt 'edit').
// Metinler i18n kataloğundan (cosre.msg.*), TR + EN.
import { useState, type ReactNode } from 'react';
import '@/styles/cosreChat.css';
import { Button } from '@/components/shadcn/button';
import { Textarea } from '@/components/shadcn/textarea';
import { useT } from '@/lib/i18n';

export function RegenerateButton({ onRegenerate, disabled }: { onRegenerate: () => void; disabled?: boolean }) {
  const t = useT();
  return (
    <Button variant="ghost" size="xs" onClick={onRegenerate} disabled={disabled} data-cosre-regenerate=""
      title={t('cosre.msg.regenerate.title')}>
      {t('cosre.msg.regenerate')}
    </Button>
  );
}

export function EditableUserMessage({ text, onEdit, disabled, children }: {
  text: string; onEdit: (text: string) => void; disabled?: boolean; children: ReactNode;
}) {
  const t = useT();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(text);
  if (editing) {
    const submit = () => {
      const v = draft.trim();
      if (!v || disabled) return;
      setEditing(false);
      onEdit(v);
    };
    return (
      <form className="cm-msg cm-msg--user flex flex-col items-end gap-1.5" data-cosre-edit-form=""
        onSubmit={e => { e.preventDefault(); submit(); }}>
        <Textarea value={draft} onChange={e => setDraft(e.target.value)} rows={3} autoFocus
          aria-label={t('cosre.msg.edit.label')} className="w-full max-w-xl bg-field dark:bg-field"
          onKeyDown={e => {
            if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); submit(); }
            if (e.key === 'Escape') { e.preventDefault(); setEditing(false); setDraft(text); }
          }} />
        <div className="flex gap-1.5">
          <Button variant="outline" size="xs" type="button" onClick={() => { setEditing(false); setDraft(text); }}>
            {t('cosre.msg.edit.cancel')}
          </Button>
          <Button size="xs" type="submit" disabled={!draft.trim() || disabled}>{t('cosre.msg.edit.send')}</Button>
        </div>
      </form>
    );
  }
  return (
    <>
      {children}
      <div className="cm-msg cm-msg--user" data-cosre-edit-row="">
        <Button variant="ghost" size="xs" disabled={disabled} title={t('cosre.msg.edit.title')} data-cosre-edit=""
          onClick={() => { setDraft(text); setEditing(true); }}>
          {t('cosre.msg.edit')}
        </Button>
      </div>
    </>
  );
}
