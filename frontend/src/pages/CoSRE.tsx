import { useEffect } from 'react';
import { CopilotChat } from '@/components/CopilotChat';
import { useBranding } from '@/lib/branding';

// CoSRE — v0.10.1125: `/cosre` bağımsız sohbet sayfası (yer imine eklenebilir,
// tek başına açılabilir). Kabuk dalı AppShell'de (lib/cosrePage.ts): sidebar
// ve uygulama kromu yok. Sohbetin KENDİSİ çekmeceyle aynı bileşen —
// CopilotChat `variant="page"` (çatal yok; başlık + gövde tek yerde kurulur).
//
// Sekme başlığı "CoSRE"; favicon zaten /favicon.svg (CoSRE markası da o).
// Markalama (browserTitle) AppShell'de ilk yüklemede document.title'ı yazar —
// `branding` bağımlılığı başlığın o yazımdan SONRA yeniden "CoSRE" olmasını
// sağlar; sayfadan çıkınca önceki başlık geri gelir.
export default function CoSRE() {
  const branding = useBranding();
  const brandTitle = branding.browserTitle;
  useEffect(() => {
    document.title = 'CoSRE';
    // Çıkışta markanın başlığı (uygulamanın geri kalanı onu gösterir).
    return () => { if (brandTitle) document.title = brandTitle; };
  }, [brandTitle]);
  return <CopilotChat variant="page" />;
}
