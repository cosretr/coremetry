// components/cosre/conversation/strings — v0.10.1647 (CoSRE v2 Faz 2b B1, plan
// §2.2). v2 sohbet kabuğunun TÜRKÇE sabitleri (coremetry/strings emsali):
// üretilen shadcn / ai-elements dosyalarının İngilizce metni sarmalayıcıda
// buradan kapatılır. v0.10.1650 (B2) — başlık eylemleri ve cevap notları v1'in
// metinleriyle aynı (plan §10: v2 v1'den yoksul görünmez).
export const COSRE_V2_STRINGS = {
  title: 'CoSRE',
  sheetLabel: 'CoSRE sohbeti',
  description: 'Bu sayfanın verisi hakkında CoSRE ile sohbet',
  threadLabel: 'CoSRE mesajları',
  fullPage: 'Tam sayfada aç ↗',
  fullPageTitle: "CoSRE'yi /cosre tam sayfasında aç",
  fullPageBusyTitle: 'Cevap bitince tam sayfada açılabilir',
  history: '🕘 Geçmiş',
  historyTitle: 'Konuşma geçmişi',
  clear: 'Temizle',
  clearTitle: 'Konuşmayı temizle ve yeni konuşma başlat',
  jumpBottom: '↓ En alta',
  jumpBottomTitle: 'En son mesaja in',
  jumpBottomLabel: 'En alta in',
  // agent-core S5 — onay kartı (ApprovalCard).
  approvalTitle: 'Onayın gerekiyor',
  approvalHint: 'CoSRE bu işlemi yapmak istiyor. Yalnız sen onaylarsan, gösterilen değerlerle çalışır.',
  approvalApprove: 'Onayla',
  approvalDeny: 'Reddet',
  approvalApproved: 'Onaylandı',
  approvalDenied: 'Yürütülmedi',
  approvalExpired: 'Süresi doldu — yürütülmedi',
  approvalExpiresIn: 'Geçerlilik sonu',
  // v0.10.1725 — "Takımını seç" kartı (TeamPickCard).
  teamPickTitle: 'Takımını seç',
  teamPickSubtitle: 'Seçimin hatırlanır, karşılama ekranındaki takımla aynıdır',
  teamPickSearch: 'Takım ara…',
  teamPickLoading: 'Aranıyor…',
  teamPickError: 'Takımlar okunamadı',
  teamPickEmpty: 'Eşleşme yok',
  teamPickUnscoped: 'Kapsamsız devam et',
  teamPickSubmit: 'Seç ve yeniden sor',
  teamPickSelected: '(seçili)',
  teamPickHeading: 'Takımlar',
} as const;

/** agent-core S5 — onay-kapılı araçların operatör dilindeki adı (yoksa ham ad). */
export const COSRE_APPROVAL_TOOL_LABELS: Readonly<Record<string, string>> = {
  add_problem_note: 'Probleme not ekle',
};
