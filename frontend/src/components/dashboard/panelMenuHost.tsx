// panelMenuHost — v0.10.1998 (Dashboards Faz 3 dilim 3.4; audit §5.3 "Panel
// başlığı: tek ⋯").
//
// Sorun: grafik panellerinde İKİ ⋯ vardı — panonun PanelMenu'sü (düzenle /
// çoğalt / Explore) ve CorePanel'in kendi menüsü (tam ekran / CSV / sorgu).
// v0.9.1163'ün "panel başına tek ⋯" kuralı pano yüzeyinde delinmişti.
//
// Çözüm: pano çerçevesi eylemlerini bağlamla AŞAĞI verir. Grafik çizen
// bileşen (DashChart) eylemleri CorePanel `menuExtra`sına koyar ve
// çerçeveye "menüyü ben taşıyorum" der (claim); çerçeve o sürece kendi ⋯'ünü
// çizmez. Grafik yokken (yükleniyor / hata / veri yok, ya da stat / gauge /
// top-N / heatmap / markdown) çerçevenin ⋯'ü görünür — panel her durumda
// TEK menüye sahip, hata veren panel de düzenlenebilir.
//
// İki bağlam: `claim` kararlı (useCallback []), eylemler her render yeni
// dizi olabilir — claim efekti eylem değişiminde yeniden koşmaz (sayaç
// salınımı yok). menuExtra bir çizim girdisi değil (CorePanel sözleşmesi).
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react';
import type { PanelMenuAction } from '@/lib/chart/panelMenu';

const ActionsCtx = createContext<PanelMenuAction[] | undefined>(undefined);
const ClaimCtx = createContext<(() => () => void) | undefined>(undefined);

/** Çerçeve: eylemleri sağlar; `hosted` = içeride menüyü taşıyan bir grafik var. */
export function usePanelMenuFrame(): { hosted: boolean; claim: () => () => void } {
  const [n, setN] = useState(0);
  const claim = useCallback(() => {
    setN(x => x + 1);
    return () => setN(x => x - 1);
  }, []);
  return { hosted: n > 0, claim };
}

export function PanelMenuProvider({ actions, claim, children }: {
  actions: PanelMenuAction[];
  claim: () => () => void;
  children: ReactNode;
}) {
  return (
    <ClaimCtx.Provider value={claim}>
      <ActionsCtx.Provider value={actions}>{children}</ActionsCtx.Provider>
    </ClaimCtx.Provider>
  );
}

/** Grafik: çerçevenin eylemlerini alır ve menüyü üstlenir. Çerçeve dışında
 *  (Explore vb.) `undefined` döner ve hiçbir şey üstlenmez. */
export function usePanelMenuHost(): PanelMenuAction[] | undefined {
  const claim = useContext(ClaimCtx);
  useEffect(() => (claim ? claim() : undefined), [claim]);
  return useContext(ActionsCtx);
}
