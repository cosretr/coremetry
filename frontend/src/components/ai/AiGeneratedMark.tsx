import type { CSSProperties } from 'react';
import { useT } from '@/lib/i18n';

// AiGeneratedMark — wave 3 (Dynatrace "AI presence" ilkesi): modelin ürettiği
// HER içerik aynı küçük etiketi taşır — CoSRE cevabı, ✨ açıklama blokları,
// AI özetleri. Tek bileşen, tek metin (i18n `ai.generated*`); yüzeyler kendi
// rozetini çizmez. Salt sunum: veri, oy ya da davranış taşımaz. Satır-içi
// stil BİLİNÇLİ: çağıranların çoğu Tailwind kaynak listesinde değil
// (undefinedCssRefs), etiket her yüzeyde aynı görünmeli; boşluk `style` ile.
const BASE: CSSProperties = {
  display: 'inline-flex', alignItems: 'center', gap: 3, flexShrink: 0,
  padding: '0 5px', border: '1px solid var(--border)', borderRadius: 'var(--radius-xs)',
  fontSize: 10, lineHeight: '15px', fontWeight: 500, letterSpacing: '.02em',
  textTransform: 'none', color: 'var(--text3)', whiteSpace: 'nowrap', verticalAlign: 'middle',
};

export function AiGeneratedMark({ style }: { style?: CSSProperties }) {
  const t = useT();
  return (
    <span data-ai-generated="" title={t('ai.generated.title')} style={style ? { ...BASE, ...style } : BASE}>
      <span aria-hidden="true">✨</span>{t('ai.generated')}
    </span>
  );
}
