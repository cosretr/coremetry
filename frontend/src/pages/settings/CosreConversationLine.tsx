import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCatalogs } from '@/lib/i18n';
import { Spinner } from '@/components/Spinner';
import { CheckboxField, TextareaField } from '@/components/coremetry/Field';
import { Button } from '@/components/shadcn/button';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { useAuthRole } from '@/components/AuthProvider';
import { api } from '@/lib/api';
import type { CosreConversationSettings } from '@/lib/types';

// v0.10.2324 (docs/cosre/conversation-audit.md §7 karar 5) — CoSRE konuşma
// kalitesi anahtarları: Settings › CoSRE'de CosreBriefingLine'ın altında
// (operatör ekranı, yalnız Türkçe). Blob `cosre_conversation` üç alan taşır:
// `state` (A), v0.10.2327'den beri `verification` off | shadow (E1; v0.10.2339'dan
// beri enforce da — üç seçenekli denetim, varsayılan gölge) ve
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

// v0.10.2339 (audit §6.1 E2, karar 6–7) — üç kip; varsayılan gölge. "zorla"nın tek satırlık açıklaması.
export const CONVERSATION_ENFORCE_HINT =
  'Zorla: o turda okunan veride bulunmayan sayı, saat ya da adın hemen ardına “(doğrulanmamış değer)” yazılır '
  + 've cümle doğrulanana dek taslakta görünmez; Derin kipte model önce bir kez yeniden yazar.';

type VerifyMode = CosreConversationSettings['verification'];
const VERIFY_OPTIONS: ReadonlyArray<{ value: VerifyMode; label: string }> = [
  { value: 'off', label: 'kapalı' },
  { value: 'shadow', label: 'gölge' },
  { value: 'enforce', label: 'zorla' },
];

type Change = { state: boolean } | { verification: VerifyMode };

// v0.10.2355 (docs/cosre/investigation-design.md S1, karar 25) — kanıt alanı:
// varsayılan kapalı. Bu sürümde yalnız kapsam kararını açar; sonraki sürümler
// (kanıt kartları, araştırma paketi) aynı seçimin arkasına gelir.
type EvidenceMode = NonNullable<CosreConversationSettings['evidence']>;
const EVIDENCE_OPTIONS: ReadonlyArray<{ value: EvidenceMode; label: string }> = [
  { value: 'off', label: 'kapalı' },
  { value: 'shadow', label: 'gölge' },
  { value: 'on', label: 'açık' },
];

export const CONVERSATION_EVIDENCE_HINT =
  'Kanıt: CoSRE v2 modelin kendi tahmin ettiği, kullanıcının kapsamında olmayan ve kullanıcının anmadığı bir servise '
  + 'gitmesini denetler. Gölge kip yalnız sayar (scope_would_refuse sinyali), cevap değişmez; açıkken o araç çağrısı '
  + 'koşmaz ve model kapsam dışı olduğunu söyler. Kullanıcının adını verdiği servis, ekrandaki servis ve konuşmanın '
  + 'servisi her zaman çalışır. Sonraki sürümlerde cevabın altındaki kanıt kartları ve hata araştırması da bu seçime bağlanır. '
  + 'Kapalıyken hiçbiri değerlendirilmez.';

// v0.10.2331 (audit §6.1 B) — üslup anahtarı: varsayılan KAPALI; operatör
// incelemeden kullanıcılar görmez. Sonraki dilimler (C, D, F1) aynı anahtarın arkasında.
// v0.10.2337 — C (örnekle üslup) ve F1 (ara cümle) da bu anahtarın arkasında; metin üçünü söyler.
export const CONVERSATION_STYLE_HINT =
  'Açıkken CoSRE v2 üç şeyi birlikte açar: cevap uzunluğu soru türüne göre (durum sorusuna 1-2 cümle, hata avına 3-5 cümle); '
  + 'örnek diyaloglarla meslektaş üslubu (özet kutusu, tablo ve kalıp madde yerine tam cümleler; sayılar yine yalnız araçlardan); '
  + 'araç çalışırken kısa ara durum cümlesi. Raporlar (servis raporu, kaynak incelemesi, trace açıklaması, rota incelemesi) bugünkü gibi kalır. '
  + 'Kapalıyken cevaplar bugünkü gibidir. Değerlendirme koşusunda kullanıcılara dokunmadan açık / kapalı denenebilir.';

// v0.10.2353 (interaction-audit §6 I5) — etkileşim anahtarı: varsayılan KAPALI.
export const CONVERSATION_INTERACTION_HINT =
  'Açıkken CoSRE v2, araçla doğrulanmış bir cevabın altına onaylı aksiyon kartı ekler: problem sayfasında '
  + '(editör ve admin) "probleme not", diğer durumlarda "paylaşım bağlantısı". Kart alanları doldurulmuş gelir, '
  + 'düzenlenebilir; Onayla\'ya basmadan hiçbir şey yazılmaz. Model aksiyon öneremez, kartı yalnız sunucu kuralı ekler. '
  + 'Kapalıyken kart çıkmaz ve onay ucu kapalıdır.';

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
  useCatalogs(); // v0.10.2421 — sabit Türkçe CoSRE metni TR kataloğu gelince yeniden çizilsin
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
  const em = useMutation({ // v0.10.2355 — kanıt alanı
    mutationFn: (e: EvidenceMode) => api.putCosreConversationEvidence(e),
    onSuccess: (d: CosreConversationSettings) => qc.setQueryData(KEY, d),
  });
  const sm = useMutation({ // v0.10.2331 — üslup
    mutationFn: (style: boolean) => api.putCosreConversationStyle(style),
    onSuccess: (d: CosreConversationSettings) => qc.setQueryData(KEY, d),
  });
  const im = useMutation({ // v0.10.2353 — I5 onaylı aksiyon kartı
    mutationFn: (on: boolean) => api.putCosreConversationInteraction(on ? 'on' : 'off'),
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
  const verify: VerifyMode = pending && 'verification' in pending ? pending.verification : q.data.verification;
  const styleOn = sm.isPending ? sm.variables === true : q.data.style;
  const interactionOn = im.isPending ? im.variables === true : q.data.interaction === 'on';
  const evidence: EvidenceMode = em.isPending && em.variables ? em.variables : (q.data.evidence ?? 'off');
  const saveErr = m.error ?? sm.error ?? im.error ?? em.error;
  return (
    <div className="mb-2 flex flex-col gap-2" data-testid="cosre-conversation-line">
      <div className="flex flex-wrap items-center gap-3">
        <CheckboxField
          label="Konuşma durumu (CoSRE v2 devam soruları)"
          hint={CONVERSATION_STATE_HINT}
          checked={on}
          disabled={!canEdit || m.isPending}
          onCheckedChange={v => m.mutate({ state: v })} />
        <div className="flex flex-col gap-1" data-testid="cosre-verify-mode" title={CONVERSATION_VERIFY_HINT}>
          <span className="text-[length:var(--fs-sm)] leading-tight text-(--text2)">Cevap doğrulayıcısı</span>
          <SegmentedControl
            aria-label="Cevap doğrulayıcısı"
            size="sm"
            activation="manual"
            value={verify}
            options={VERIFY_OPTIONS.map(o => ({ ...o, disabled: !canEdit || m.isPending }))}
            onChange={v => { if (v !== verify) m.mutate({ verification: v }); }} />
          <span className="field-hint">{CONVERSATION_ENFORCE_HINT}</span>
        </div>
        <CheckboxField
          label="Üslup (CoSRE v2: ölçü, örnekle üslup, ara cümle)"
          hint={CONVERSATION_STYLE_HINT}
          checked={styleOn}
          disabled={!canEdit || sm.isPending}
          onCheckedChange={v => sm.mutate(v)} />
        <CheckboxField
          label="Etkileşim (CoSRE v2: onaylı aksiyon kartı)"
          hint={CONVERSATION_INTERACTION_HINT}
          checked={interactionOn}
          disabled={!canEdit || im.isPending}
          onCheckedChange={v => im.mutate(v)} />
        <div className="flex flex-col gap-1" data-testid="cosre-evidence-mode" title={CONVERSATION_EVIDENCE_HINT}>
          <span className="text-[length:var(--fs-sm)] leading-tight text-(--text2)">Kanıt (kapsam kararı)</span>
          <SegmentedControl
            aria-label="Kanıt"
            size="sm"
            activation="manual"
            value={evidence}
            options={EVIDENCE_OPTIONS.map(o => ({ ...o, disabled: !canEdit || em.isPending }))}
            onChange={v => { if (v !== evidence) em.mutate(v); }} />
          <span className="field-hint">{CONVERSATION_EVIDENCE_HINT}</span>
        </div>
        {!canEdit && <span className="field-hint">Yalnız admin değiştirebilir.</span>}
        {(m.isPending || sm.isPending || im.isPending || em.isPending) && <Spinner label="Kaydediliyor…" />}
        {(m.isError || sm.isError || im.isError || em.isError) && (
          <span role="alert" className="text-[length:var(--fs-sm)] text-(--err)">
            Kaydedilemedi{saveErr instanceof Error && saveErr.message ? `: ${saveErr.message}` : ''}
          </span>
        )}
      </div>
      <BannedPhrasesEditor saved={q.data.bannedPhrases ?? []} canEdit={canEdit} />
    </div>
  );
}
