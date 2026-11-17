// AiThinking — v0.10.1384: AI'ın "düşünüyor" hâlinin TEK göstergesi.
//
// Operatör: "CoSRE düşünüyor derken ve explain trace'teki dönme efektleri
// aynı değil." Sohbet balonu parıltılı metin, Explain çekmecesi dönen OTel
// halkası (LoaderMark), deploy satırı "✨ Thinking…", insight kartı genel
// Spinner kullanıyordu — dört yüzey, dört dil. Artık hepsi bu bileşen:
//   • ikon: kıvılcım (IconSparkles) NABIZ atar — dönmez. Dönen halka "sayfa
//     yükleniyor" demek; AI'ın bekleyişi yükleme değil, düşünme.
//   • etiket: soldan sağa parıltı (eski .cm-thinking'in dili).
//   • role="status" + aria-live="polite": ekran okuyucu bekleyişi duyar;
//     v0.10.2333 — CoSRE v2 etiketi ara durum cümlesine dönünce (F1) değişen
//     cümle de sessizce okunur.
//   • prefers-reduced-motion: nabız ve parıltı durur, düz metin kalır (CSS).
// Varyantlar: `inline` (düğme / satır içi), `row` (sohbet balonu, kart satırı),
// `block` (boş gövdenin ortası — Explain çekmecesi, AI özet gövdeleri).
// LoaderMark AI DIŞI yüklemeler için kalır (PageLoader). Yeni bir elle yazılmış
// AI bekleme göstergesi aiThinkingRatchet.test.ts'te düşer.
import type { ReactNode } from 'react';
import { IconSparkles } from '@/components/icons';

export type AiThinkingVariant = 'inline' | 'row' | 'block';

/** Varsayılan etiket (sohbet balonu). Yüzeyler "CoSRE düşünüyor…" geçebilir. */
export const AI_THINKING_LABEL = 'Düşünüyor…';

// Şablonla kurulan className tip kapısı taşımaz — sabit tablo.
const ROOT_CLASS: Record<AiThinkingVariant, string> = {
  inline: 'ai-thinking ai-thinking--inline',
  row: 'ai-thinking ai-thinking--row',
  block: 'ai-thinking ai-thinking--block',
};
const ICON_SIZE: Record<AiThinkingVariant, number> = { inline: 13, row: 14, block: 26 };

export function AiThinking({ label = AI_THINKING_LABEL, hint, variant = 'row', children }: {
  label?: string;
  /** İkinci satır (yalnız `block`'ta ayrı satır; diğerlerinde etiketin yanında soluk). */
  hint?: string;
  variant?: AiThinkingVariant;
  /** Göstergeye eşlik eden eylem (ör. "Durdur") — `block`'ta altta. */
  children?: ReactNode;
}) {
  const body = (
    <>
      <span className="ai-thinking__icon" aria-hidden="true"><IconSparkles size={ICON_SIZE[variant]} /></span>
      <span className="ai-thinking__text">
        <span className="ai-thinking__label">{label}</span>
        {hint && <span className="ai-thinking__hint">{hint}</span>}
      </span>
    </>
  );
  if (variant === 'block') {
    return (
      <div className={ROOT_CLASS.block} role="status" aria-live="polite">
        {body}
        {children}
      </div>
    );
  }
  return (
    <span className={ROOT_CLASS[variant]} role="status" aria-live="polite">
      {body}
      {children}
    </span>
  );
}
