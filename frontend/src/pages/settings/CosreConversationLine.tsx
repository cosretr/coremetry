import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Spinner } from '@/components/Spinner';
import { CheckboxField, TextareaField } from '@/components/coremetry/Field';
import { Button } from '@/components/shadcn/button';
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
// v0.10.2337 — C (örnekle üslup) ve F1 (ara cümle) da bu anahtarın arkasında; metin üçünü söyler.
export const CONVERSATION_STYLE_HINT =
  'Açıkken CoSRE v2 üç şeyi birlikte açar: cevap uzunluğu soru türüne göre (durum sorusuna 1-2 cümle, hata avına 3-5 cümle); '
  + 'örnek diyaloglarla meslektaş üslubu (özet kutusu, tablo ve kalıp madde yerine tam cümleler; sayılar yine yalnız araçlardan); '
  + 'araç çalışırken kısa ara durum cümlesi. Raporlar (servis raporu, kaynak incelemesi, trace açıklaması, rota incelemesi) bugünkü gibi kalır. '
  + 'Kapalıyken cevaplar bugünkü gibidir. Değerlendirme koşusunda kullanıcılara dokunmadan açık / kapalı denenebilir.';

// v0.10.2338 (audit §6.1 D) — cevap dikişinin kalıp listesi: varsayılan liste
// sunucuda (evalrubric, tek kaynak); buradaki ifadeler onu GENİŞLETİR. Sunucu
// tavanları: en çok 40 ifade, her biri ≤80 bayt (chstore.NormalizeCosreBannedPhrases).
export const BANNED_PHRASES_MAX = 40;
export const BANNED_PHRASE_MAX_BYTES = 80;

export const BANNED_PHRASES_HINT =
  'Satır başına bir ifade. Üslup açıkken cevabın ilk cümlesi bu ifadelerle başlıyorsa ya da son cümlesi bunları içeriyorsa '
  + 'cümle silinir (sayı, saat ya da servis adı taşıyan cümle hiç silinmez). Varsayılan liste (selam, tabii, umarım yardımcı olmuştur …) '
  + 'her zaman uygulanır; buraya yazılanlar ona eklenir.';

/** Satırlar → kırpılmış, boşluğu tekleştirilmiş, büyük/küçük harfe bakmadan tekil liste (sunucu kuralının aynası). */
export function parseBannedPhrases(text: string): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const line of text.split('\n')) {
    const p = line.trim().split(/\s+/).filter(Boolean).join(' ');
    const k = p.toLocaleLowerCase('tr');
    if (!p || seen.has(k)) continue;
    seen.add(k);
    out.push(p);
  }
  return out;
}

export function bannedPhrasesError(list: string[]): string | undefined {
  if (list.length > BANNED_PHRASES_MAX) return `En çok ${BANNED_PHRASES_MAX} ifade (${list.length} yazıldı).`;
  const enc = new TextEncoder();
  const long = list.find(p => enc.encode(p).length > BANNED_PHRASE_MAX_BYTES);
  return long ? `“${long.slice(0, 24)}…” ${BANNED_PHRASE_MAX_BYTES} bayttan uzun.` : undefined;
}

function BannedPhrasesEditor({ saved, canEdit }: { saved: string[]; canEdit: boolean }) {
  const qc = useQueryClient();
  const [draft, setDraft] = useState<string | null>(null); // null = düzenlenmedi (sunucu değeri)
  const text = draft ?? saved.join('\n');
  const list = parseBannedPhrases(text);
  const invalid = bannedPhrasesError(list);
  const m = useMutation({
    mutationFn: (p: string[]) => api.putCosreConversationBannedPhrases(p),
    onSuccess: (d: CosreConversationSettings) => { qc.setQueryData(KEY, d); setDraft(null); },
  });
  const saveErr = m.error instanceof Error && m.error.message ? `Kaydedilemedi: ${m.error.message}` : m.isError ? 'Kaydedilemedi' : undefined;
  return (
    <div className="flex w-full max-w-[520px] flex-col gap-1" data-testid="cosre-banned-phrases">
      <TextareaField
        label="Ek kalıp ifadeleri (üslup)"
        hint={BANNED_PHRASES_HINT}
        error={invalid ?? saveErr}
        rows={3}
        value={text}
        placeholder="Sorunuzu inceledim"
        disabled={!canEdit || m.isPending}
        onChange={e => setDraft(e.target.value)} />
      {canEdit && (
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" disabled={draft === null || !!invalid || m.isPending} onClick={() => m.mutate(list)}>
            Kaydet
          </Button>
          <span className="field-hint">{list.length} / {BANNED_PHRASES_MAX}</span>
        </div>
      )}
    </div>
  );
}

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
    <div className="mb-2 flex flex-col gap-2" data-testid="cosre-conversation-line">
      <div className="flex flex-wrap items-center gap-3">
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
          label="Üslup (CoSRE v2: ölçü, örnekle üslup, ara cümle)"
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
      <BannedPhrasesEditor saved={q.data.bannedPhrases ?? []} canEdit={canEdit} />
    </div>
  );
}
