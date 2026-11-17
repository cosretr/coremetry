import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Spinner } from '@/components/Spinner';
import { CheckboxField } from '@/components/coremetry/Field';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import type { CosreConversationSettings } from '@/lib/types';

// v0.10.2324 (docs/cosre/conversation-audit.md §7 karar 5) — CoSRE konuşma
// kalitesi anahtarları: Settings › CoSRE'de CosreBriefingLine'ın altında
// (operatör ekranı, yalnız Türkçe). Blob `cosre_conversation` üç alan taşır:
// `state` (A), v0.10.2327'den beri `verification` off | shadow (E1) ve
// v0.10.2331'den beri `style` (B) bağlı. state ve verification varsayılanı
// açık, style varsayılanı kapalı (sunucu varsayılanı); kapatmak geri alma
// anahtarıdır. Audit sunucuda (`settings.cosre_conversation.update`).
const KEY = ['ai', 'cosre-conversation'] as const;

export const CONVERSATION_STATE_HINT =
  'Açıkken CoSRE v2 her turda önceki turun servisini, zaman penceresini ve ortamını hatırlar, '
  + '"dün", "bu sabah", "geçen hafta" gibi ifadeleri pencereye çevirir ve önerdiği sonraki adımı '
  + '"bak" / "evet" ile yürütür. Kapalıyken v2 bu özellikten önceki gibi davranır.';

export const CONVERSATION_VERIFY_HINT =
  'Gölge kip: CoSRE v2 her cevaptaki sayıları, saatleri ve servis adlarını o turda okunan veriyle '
  + 'karşılaştırır ve yalnız sayar; cevap değişmez, gecikmez, işaretlenmez. Uyuşmayan tur kalite '
  + 'sinyali olarak kaydedilir. Kapalıyken hiçbir denetim koşmaz.';

type Change = { state: boolean } | { verification: 'off' | 'shadow' };

// v0.10.2331 (audit §6.1 B) — üslup anahtarı: varsayılan KAPALI; operatör
// incelemeden kullanıcılar görmez. Sonraki dilimler (C, D, F1) aynı anahtarın arkasında.
export const CONVERSATION_STYLE_HINT =
  'Açıkken CoSRE v2 cevabın uzunluğunu soru türüne göre ayarlar (durum sorusuna 1-2 cümle, hata avına 3-5 cümle; '
  + 'raporlar hariç). Kapalıyken cevaplar bugünkü gibidir. Değerlendirme koşusunda kullanıcılara dokunmadan açık / kapalı denenebilir.';

export function CosreConversationLine() {
  const qc = useQueryClient();
  const role = useAuthRole(); // sağlayıcısız ağaç (Settings zaten admin kapılı) → düzenlenebilir
  const canEdit = role === undefined || role === 'admin';
  const q = useQuery({ queryKey: KEY, queryFn: () => api.cosreConversationSettings(), staleTime: 30_000 });
  const m = useMutation({
    mutationFn: (c: Change) => ('state' in c
      ? api.putCosreConversationState(c.state)
      : api.putCosreConversationVerification(c.verification)),
    onSuccess: (d: CosreConversationSettings) => qc.setQueryData(KEY, d),
  });
  const sm = useMutation({ // v0.10.2331 — üslup
    mutationFn: (style: boolean) => api.putCosreConversationStyle(style),
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
  const pending = m.isPending ? m.variables : undefined;
  const on = pending && 'state' in pending ? pending.state : q.data.state;
  const verifyOn = pending && 'verification' in pending ? pending.verification !== 'off' : q.data.verification !== 'off';
  const styleOn = sm.isPending ? sm.variables === true : q.data.style;
  const saveErr = m.error ?? sm.error;
  return (
    <div className="mb-2 flex flex-wrap items-center gap-3" data-testid="cosre-conversation-line">
      <CheckboxField
        label="Konuşma durumu (CoSRE v2 devam soruları)"
        hint={CONVERSATION_STATE_HINT}
        checked={on}
        disabled={!canEdit || m.isPending}
        onCheckedChange={v => m.mutate({ state: v })} />
      <CheckboxField
        label="Cevap doğrulayıcısı (gölge, yalnız ölçer)"
        hint={CONVERSATION_VERIFY_HINT}
        checked={verifyOn}
        disabled={!canEdit || m.isPending}
        onCheckedChange={v => m.mutate({ verification: v ? 'shadow' : 'off' })} />
      <CheckboxField
        label="Üslup (CoSRE v2 cevap ölçüsü)"
        hint={CONVERSATION_STYLE_HINT}
        checked={styleOn}
        disabled={!canEdit || sm.isPending}
        onCheckedChange={v => sm.mutate(v)} />
      {!canEdit && <span className="field-hint">Yalnız admin değiştirebilir.</span>}
      {(m.isPending || sm.isPending) && <Spinner label="Kaydediliyor…" />}
      {(m.isError || sm.isError) && (
        <span role="alert" className="text-[length:var(--fs-sm)] text-(--err)">
          Kaydedilemedi{saveErr instanceof Error && saveErr.message ? `: ${saveErr.message}` : ''}
        </span>
      )}
    </div>
  );
}
