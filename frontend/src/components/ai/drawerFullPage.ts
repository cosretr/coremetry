// drawerFullPage — v0.10.1138: uygulama içi CoSRE çekmecesinden /cosre tam
// sayfasına geçişin SAF yarısı. Kimlik varsa ?chat=<id> (sayfa konuşmayı
// mevcut deep-link yoluyla yükler: CopilotChat chatParamRef), yoksa düz /cosre.
export function cosreFullPageHref(conversationId: string | null | undefined): string {
  return conversationId ? `/cosre?chat=${encodeURIComponent(conversationId)}` : '/cosre';
}
