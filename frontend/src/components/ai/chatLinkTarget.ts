import { createContext, useContext } from 'react';

// chatLinkTarget — v0.10.1125 (/cosre bağımsız sohbet sayfası).
//
// /cosre kromsuz: sidebar yok, uygulama kabuğu yok. Cevabın içindeki iç
// linkler (trace/servis/problem çipleri, kanıt kartı, trace listesi, mdLite
// trace id'leri) orada AYNI sekmede gezseydi operatör sohbetten kopardı —
// sayfa bir sohbet penceresi olarak yer imine eklenmek için var. Bu yüzden
// /cosre'de iç linkler YENİ SEKMEDE açılır (target=_blank rel=noopener);
// href'ler aynen aynı-köken uygulama yolu kalır. Çekmecede (uygulama
// içinde) davranış değişmez: bağlam yoksa `false`.
export const ChatLinkNewTabContext = createContext(false);

export function useChatLinkNewTab(): boolean {
  return useContext(ChatLinkNewTabContext);
}

/** <Link>/<a>'ya yayılacak nitelikler: yeni sekme kipinde target+rel, değilse boş. */
export function chatLinkTargetProps(newTab: boolean): { target?: '_blank'; rel?: string } {
  // v0.10.1137 inceleme — iç linkte de Referer gitmesin (sohbet URL'si ?chat= taşır).
  return newTab ? { target: '_blank', rel: 'noopener noreferrer' } : {};
}
