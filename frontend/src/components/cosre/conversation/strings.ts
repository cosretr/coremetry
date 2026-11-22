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
  // v0.10.2459 — panel varsayılan overlay; "Sabitle" içeriği daraltıp yanına oturtur (≥1280 px).
  dock: '📌 Sabitle',
  dockTitle: 'Paneli sayfanın yanına sabitle (içerik daralır); tekrar tıkla: sayfanın üstünde aç',
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
  /** v0.10.2102 — soru kartında seçilen cevabın işareti (ekran okuyucu). */
  questionChosen: 'seçildi',
  /** v0.10.2101 — özet kartının link grubu (ekran okuyucu). */
  linkCardLinks: 'İlgili sayfalar',
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
  // v0.10.1737 — "Endpoint seç" kartı (EndpointPickCard).
  endpointPickTitle: 'Endpoint seç',
  endpointPickSubtitle: 'Hata oranı ve p95\'e göre sıralı. Arama sunucuda.',
  endpointPickSearch: 'Route ara…',
  endpointPickLoading: 'Aranıyor…',
  endpointPickError: "Endpoint'ler okunamadı",
  endpointPickEmpty: 'Eşleşme yok',
  endpointPickColRoute: 'Route',
  endpointPickColRps: 'req/s',
  endpointPickColP95: 'p95',
  endpointPickColErr: 'hata',
  endpointPickSummarize: 'Hepsini özetle',
  endpointPickSubmit: 'İncele',
  endpointPickSelected: '(seçili)',
  // v0.10.2325 — konuşma bağlamı satırı (data-state; salt okunur).
  stateLineLabel: 'Konuşma bağlamı',
  stateLineTitle: 'CoSRE devam sorularında bu kapsamı kullanır; soruda başka servis ya da zaman yazarsan o geçerli olur.',
  // v0.10.2335 — kullanıcının kendi eş adları (CosreAliasPanel).
  aliases: 'Eş adlarım',
  aliasesButtonTitle: 'CoSRE\'nin senin için hatırladığı eş adlar',
  aliasesTitle: 'Eş adlarım',
  aliasesIntro: 'Düzelttiğinde kaydettiğin ifadeler. Yalnız senin sohbetlerinde geçerli.',
  aliasesLoading: 'Yükleniyor…',
  aliasesError: 'Eş adlar okunamadı',
  aliasesEmpty: 'Henüz kayıtlı eş ad yok.',
  aliasesNot: 'değil',
  aliasesStale: 'bayat',
  aliasesStaleTitle: 'Bu servis artık katalogda yok; CoSRE bu eşlemeyi kullanmıyor.',
  aliasesDelete: 'Sil',
  aliasesDeleteError: 'Silinemedi',
  aliasesDeleteTitle: 'Eş ad silinsin mi?',
  // v0.10.2353 — onaylı aksiyon kartı (ActionCard; interaction-audit I5).
  actionNoteTitle: 'Probleme not ekle',
  actionShareTitle: 'Paylaşım bağlantısı',
  actionFieldTitle: 'Başlık',
  actionFieldService: 'Servis',
  actionFieldWindow: 'Pencere',
  actionFieldSummary: 'Özet',
  actionEvidence: 'Kanıt',
  actionConfirm: 'Onayla',
  actionCancel: 'İptal',
  actionCopy: 'Metni kopyala',
  actionCancelled: 'Tamam, hiçbir şey yazılmadı.',
  actionHint: 'Onayla\'ya basmadan hiçbir şey yazılmaz.',
  actionDone: 'Tamamlandı.',
  actionExpired: 'Kartın süresi doldu — soruyu yeniden sor.',
  actionNeedsSave: 'Önce konuşma kaydedilmeli; kaydedildikten sonra yeniden onayla.',
  actionFailed: 'Yapılamadı',
  actionBadResult: 'beklenmeyen cevap',
  actionOpenLink: 'Bağlantıyı aç',
  actionOpenProblem: 'Problemi aç',
} as const;

/** agent-core S5 — onay-kapılı araçların operatör dilindeki adı (yoksa ham ad). */
export const COSRE_APPROVAL_TOOL_LABELS: Readonly<Record<string, string>> = {
  add_problem_note: 'Probleme not ekle',
};
