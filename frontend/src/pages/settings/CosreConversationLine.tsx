import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Spinner } from '@/components/Spinner';
import { CheckboxField } from '@/components/coremetry/Field';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import type { CosreConversationSettings } from '@/lib/types';

// v0.10.2324 (docs/cosre/conversation-audit.md §7 karar 5) — CoSRE konuşma
// durumu anahtarı: Settings › CoSRE'de CosreBriefingLine'ın altında TEK aç/kapa
// (operatör ekranı, yalnız Türkçe). Blob `cosre_conversation` üç alan taşır;
// bu sürümde yalnız `state` bağlı, `style` / `verification` ayrılmış ve
// gösterilmez. Varsayılan AÇIK (sunucu varsayılanı); kapatmak geri alma
// anahtarıdır. Audit sunucuda (`settings.cosre_conversation.update`).
const KEY = ['ai', 'cosre-conversation'] as const;

export const CONVERSATION_STATE_HINT =
  'Açıkken CoSRE v2 her turda önceki turun servisini, zaman penceresini ve ortamını hatırlar, '
  + '"dün", "bu sabah", "geçen hafta" gibi ifadeleri pencereye çevirir ve önerdiği sonraki adımı '
  + '"bak" / "evet" ile yürütür. Kapalıyken v2 bu özellikten önceki gibi davranır.';

export function CosreConversationLine() {
  const qc = useQueryClient();
  const role = useAuthRole(); // sağlayıcısız ağaç (Settings zaten admin kapılı) → düzenlenebilir
  const canEdit = role === undefined || role === 'admin';
  const q = useQuery({ queryKey: KEY, queryFn: () => api.cosreConversationSettings(), staleTime: 30_000 });
  const m = useMutation({
    mutationFn: (state: boolean) => api.putCosreConversationState(state),
    onSuccess: (d: CosreConversationSettings) => qc.setQueryData(KEY, d),
  });
  if (q.isPending) return <div className="mb-2"><Spinner label="Konuşma durumu ayarı okunuyor…" /></div>;
  if (q.isError) {
    return (
      <div className="mb-2" data-testid="cosre-conversation-line">
        <span className="field-hint" title={q.error instanceof Error ? q.error.message : ''}>Konuşma durumu: ayar okunamadı</span>
      </div>
    );
  }
  const on = m.isPending ? m.variables === true : q.data.state;
  return (
    <div className="mb-2 flex flex-wrap items-center gap-3" data-testid="cosre-conversation-line">
      <CheckboxField
        label="Konuşma durumu (CoSRE v2 devam soruları)"
        hint={CONVERSATION_STATE_HINT}
        checked={on}
        disabled={!canEdit || m.isPending}
        onCheckedChange={v => m.mutate(v)} />
      {!canEdit && <span className="field-hint">Yalnız admin değiştirebilir.</span>}
      {m.isPending && <Spinner label="Kaydediliyor…" />}
      {m.isError && (
        <span role="alert" className="text-[length:var(--fs-sm)] text-(--err)">
          Kaydedilemedi{m.error instanceof Error && m.error.message ? `: ${m.error.message}` : ''}
        </span>
      )}
    </div>
  );
}
