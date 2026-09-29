import { useState, type ReactNode } from 'react';
import { Button } from '@/components/ui/Button';
import { IconSparkles } from '@/components/icons';
import { useSearchParams } from 'react-router-dom';
import { AI_QUICK_PARAM, formatAiParam, type AISubject } from '@/lib/aiSubject';
import { useAiSubject } from './useAiSubject';
import { useCopilotEnabled } from './useCopilotEnabled';

// AIExplainButton — TEK "✨ Explain" affordance'ı (v0.9.477, onaylı
// AI-drawer mockup'ı). Eskiden her yüzey kendi CopilotExplain'ini gömüyor,
// cevap butonun ALTINDA satır-içi bir panel olarak açılıyordu: aynı içerik
// sekiz farklı genişlikte, sayfayı iterek. Artık buton yalnız adrese
// `?ai=<kind>:<id>` yazar; içeriği AppShell'deki tek AIDrawer render eder.
//
// Korunanlar:
//   • copilot kapalıysa buton HİÇ görünmez (eski self-hide davranışı),
//   • ilk kullanıma dek nabız atar (v0.9.409, operatör isteği),
//   • Button atomu + variant="accent" (tek tasarım dili).
export function AIExplainButton({ subject, label, size, emphasis = 'normal', title, className, quick = false }: {
  subject: AISubject;
  // quick (v0.10.987, operatör "3 seçenek") — trace'in tek atışlık klasik
  // açıklaması: özneyle birlikte ?aiquick=1 yazılır. Aynı trace'in iki
  // düğmesi (CoSRE'ye sor / Hızlı açıkla) aynı özneyi taşır; "açık" sayımı
  // quick bayrağıyla ayrışır ki biri açıkken öteki tık kapatmasın, geçirsin.
  quick?: boolean;
  label?: ReactNode;
  // 'xs' (v0.9.1033): kart başlığındaki mini ✨ — ikon-only etiketle
  // kullanılır, satır yüksekliğini büyütmez.
  size?: 'xs' | 'sm' | 'md';
  // emphasis (v0.9.1166, operatör: "Explain trace butonu daha belirgin
  // olsun"). Ağırlık ARTIK ÇAĞRI YERİNDE beyan edilir, çünkü tek bir
  // varsayılan iki farklı işi taşıyamıyordu: kart başlığındaki ✨ satır
  // yüksekliğini büyütmemeli, sayfanın TEK ana eylemi ise göze çarpmalı.
  // Serbest `variant` prop'u AÇMIYORUZ — her yüzey kendi ağırlığını
  // seçerse atomun varlık nedeni (tek affordance görünümü) biter; iki
  // isimli basamak denetlenebilir kalır.
  //   normal → accent + sm (tüm mevcut çağrılar; davranış değişmedi)
  //   strong → primary + md (dolu aksan; grubun tek birincili olmalı)
  emphasis?: 'normal' | 'strong';
  title?: string;
  className?: string;
}) {
  const enabled = useCopilotEnabled();
  const [ai, setAi] = useAiSubject();
  const [sp] = useSearchParams();
  const quickOn = sp.get(AI_QUICK_PARAM) === '1';
  // Nabız "bu mount'ta hiç kullanılmadı" demek; çekmece bu özneyle
  // açıldıysa da susar (operatör zaten bulmuş).
  const [used, setUsed] = useState(false);
  if (enabled !== true) return null;

  const key = formatAiParam(subject);
  const open = ai !== null && formatAiParam(ai) === key && quickOn === quick;
  const quiet = used || open;

  const strong = emphasis === 'strong';
  return (
    <Button variant={strong ? 'primary' : 'accent'}
      size={size ?? (strong ? 'md' : 'sm')}
      aria-expanded={open}
      title={title ?? 'AI açıklamasını sağ çekmecede aç'}
      className={[quiet ? undefined : 'ai-attn', className].filter(Boolean).join(' ') || undefined}
      onClick={() => { setUsed(true); setAi(open ? null : subject, undefined, quick); }}>
      {label ?? <><IconSparkles /> <span>AI explain</span></>}
    </Button>
  );
}
