import { normalizePath } from './auth-paths';

// cosrePage — v0.10.1125 (operatör: "CoSRE kendi adresinde, yalnız sohbet").
//
// /cosre kimlikli ama KROMSUZ bir yüzey: Sidebar, duyuru şeridi, ⌘K, kısayollar,
// FAB'lı CopilotChat ve /api/events aboneliği mount edilmez — sayfanın kendisi
// tam boy sohbettir (pages/CoSRE.tsx → <CopilotChat variant="page"/>).
//
// Kiosk-çıplak daldan (lib/kioskMode.ts) bilinçli olarak AYRI: kiosk 401'de
// satır-içi "oturum bitti" kartı çizer; /cosre normal korumalı rota gibi
// /login'e düşer ve derin bağlantı dönüşü (postLoginRedirect / OIDC ?next=)
// operatörü yine /cosre'ye getirir. PUBLIC_PATHS'e de EKLENMEZ — giriş ister.
//
// Saf; AppShell (render dalı) okur. Tek yazılış — iki yerde iki liste olmasın.
export const COSRE_PATH = '/cosre';

export function isCosrePage(pathname: string): boolean {
  return normalizePath(pathname ?? '') === COSRE_PATH;
}
