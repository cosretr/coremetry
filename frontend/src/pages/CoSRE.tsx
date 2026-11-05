import { lazy, Suspense, useEffect } from 'react';
import { CopilotChat } from '@/components/CopilotChat';
import { Spinner } from '@/components/Spinner';
import { useCopilotConfig } from '@/components/ai/useCopilotEnabled';
import { setDocumentTitleOverride } from '@/lib/branding';

// v0.10.1785 — CoSRE v2 tam sayfası (mockup A): bayrak açıkken ayrı tembel chunk.
const CosrePageV2 = lazy(() => import('@/components/cosre/page/CosrePageV2'));

// CoSRE — v0.10.1125: `/cosre` bağımsız sohbet sayfası (yer imine eklenebilir,
// tek başına açılabilir). Kabuk dalı AppShell'de (lib/cosrePage.ts): sidebar
// ve uygulama kromu yok. Sohbetin KENDİSİ çekmeceyle aynı bileşen —
// CopilotChat `variant="page"` (çatal yok; başlık + gövde tek yerde kurulur).
//
// Sekme başlığı "CoSRE"; favicon zaten /favicon.svg (CoSRE markası da o).
// v0.10.1340 — başlık lib/branding.ts'in TAM geçersiz kılmasıyla yazılır:
// markalama fetch'i sayfa açıldıktan SONRA çözülse de (applyBranding) "CoSRE"
// kalır — eski `document.title = 'CoSRE'` + brandTitle bağımlılığı varsayılan
// markada yeniden koşmuyor, sekme "Coremetry"ye dönüyordu. Çıkışta geçersiz
// kılma kalkar → sayfa/marka bileşimi (Topbar'lı sayfa kendi başlığını yazar).
//
// v0.10.1785 — `cosreV2 === true` (ve copilot açık) → v2 sayfası
// (components/cosre/page/CosrePageV2: CosreChatV2 variant="page"). Bayrak
// kapalı / copilot kapalı → v1 yolu birebir. Yapılandırma bilinmeden hiçbir
// gerçekleme mount edilmez (yanlış sohbet istek atmasın); bekleme durumu v1'in
// yükleniyor görünümüyle aynı.
export default function CoSRE() {
  useEffect(() => {
    setDocumentTitleOverride('CoSRE');
    return () => setDocumentTitleOverride(null);
  }, []);
  const cfg = useCopilotConfig(true);
  if (cfg === null) return <CosrePageLoading />;
  if (cfg.enabled && cfg.cosreV2 === true) {
    return (
      <Suspense fallback={<CosrePageLoading />}>
        <CosrePageV2 />
      </Suspense>
    );
  }
  return <CopilotChat variant="page" />;
}

function CosrePageLoading() {
  return (
    <div className="cosre-page cosre-root cosre-root--page">
      <div className="cosre-page__state"><Spinner label="CoSRE yükleniyor…" /></div>
    </div>
  );
}
