// strings — CoSRE v2 adım kartları + kapsam rozeti (araç seti dilim 5) metinleri.
import type { CosreScopeReason, CosreStopReason } from '@/lib/types';

export const EVIDENCE_STRINGS = {
  stepsTitle: 'Adımlar',
  calls: (n: number) => `${n} çağrı`,
  /** v0.10.1787 — sayfadaki katlı tek satır: "3 adım · 4,2 sn" (süre yoksa yalnız adım). */
  stepsLine: (n: number, took: string) => (took ? `${n} adım · ${took}` : `${n} adım`),
  rounds: (n: number) => `${n} tur`,
  stepsListLabel: 'Adımlar',
  skippedTitle: 'çalıştırılmadı',
  skippedMeta: 'atlandı',
  runningMeta: 'çalışıyor',
  errorMeta: 'okunamadı',
  errorPrefix: 'Okunamadı ≠ yok',
  reasonPrefix: 'Neden:',
  queryShow: (kind: string) => `Sorguyu göster (${kind})`,
  queryReadOnly: 'salt okunur',
  querySource: (s: string) => `kaynak: ${s}`,
  copy: 'Kopyala',
  copied: 'Kopyalandı',
  truncated: 'kısaltıldı',
  rowsTag: (n: number) => `${n} satır`,
  seriesTag: (n: number) => `${n} seri`,
  envTag: (e: string) => `env: ${e}`,
  more: (n: number, open: boolean) => (open ? `+${n} satır · satıra tıkla → sayfada aç` : `+${n} satır`),
  moreUnknown: 'daha fazlası sayfada',
  stopPrefix: 'Durdu:',
  inputsPrefix: 'Girdiler:',
  rawToggle: 'Ham çıktı',
  relevancePrompt: 'Bu kanıt cevap için:',
  badgeGuided: 'ön-yükleme',
  badgeGuidedTitle: 'Rehberli kip: model araç çağırmadı, kanıt sunucuda önceden toplandı',
  badgeIntent: 'niyet',
  badgeIntentTitle: 'Serbest soru tek JSON çağrısıyla kılavuz niyetine sınıflandırıldı; kanıt sunucuda toplandı (v0.10.172)',
  badgeDeadline: '⏱ bütçe aşıldı',
  badgeBudget: 'bütçe doldu',
  scopeNote: (label: string) => `Kapsam değişti: ${label}`,
  scopeAria: 'Sohbet kapsamı',
  scopeTrimmed: (n: number) => `+${n}`,
  okAria: 'tamam',
  skipAria: 'atlandı',
  errAria: 'hata',
  runAria: 'çalışıyor',
  /** v0.10.2363 (I1) — katlı başlıkta çalışan adım. */
  runningStep: (title: string) => `şimdi: ${title}`,
  /** v0.10.2368 (I2) — adım menüsü ve durdur-özetle. */
  stepMenuAria: (title: string) => `Adım seçenekleri: ${title}`,
  redirectContinue: 'Buradan devam et',
  redirectMetric: 'Bunun yerine metriğe bak',
  redirectTrace: "Bunun yerine trace'e bak",
  redirectLog: "Bunun yerine log'a bak",
  redirectSkip: 'Bu adımı atla',
  stopSummarize: 'Durdur ve özetle',
  /** Yönerge kullanıcı mesajı olarak görünür (karar 7). */
  redirectAsk: {
    continue: (t: string) => `${t} adımından devam et`,
    metric: (t: string) => `${t} yerine metriklere bak`,
    trace: (t: string) => `${t} yerine trace'lere bak`,
    log: (t: string) => `${t} yerine loglara bak`,
    skip: (t: string) => `${t} adımını atla`,
    summarize: () => 'Durdur ve özetle',
  },
} as const;

/** data-stop `reason` → tek satırlık etiket (8 neden). */
export const STOP_LABELS: Record<CosreStopReason, string> = {
  stop: 'cevap tamamlandı',
  round_cap: 'tur tavanı',
  call_cap: 'çağrı tavanı',
  cancelled: 'durduruldu',
  deadline: 'süre doldu',
  approval_pending: 'onay bekliyor',
  error: 'hata',
  token_budget: 'bağlam bütçesi doldu',
};

/** Yürütülmeyen çağrının sunucu gerekçesi (chat_loop_steps.go skipReason*) → cümle. */
export const SKIP_REASON_LABELS: Record<string, string> = {
  call_cap: 'çağrı bütçesi doldu',
  repeated: 'aynı çağrı bu konuşmada zaten yapıldı',
  scope: 'çağrı kapsam dışı',
  cancelled: 'istek durduruldu',
  unknown: 'araç bu katalogda yok',
  error: 'çağrı yürütülemedi',
  approval_required: 'onay bekliyor',
  approval_denied: 'onaylanmadı',
  approval_expired: 'onay süresi doldu',
};

/** Araç adı → insan dilinde başlık (bilinmeyen araç: adın kendisi, `_` → boşluk). */
export const TOOL_TITLES: Record<string, string> = {
  list_my_services: 'Servislerim',
  open_problems: 'Açık problemler',
  find_traces: "Trace'ler",
  search_logs: 'Log arama',
  explain_trace: 'Trace açıklaması',
  endpoint_metrics: 'Endpoint metrikleri',
  get_service_health: 'Servis sağlığı',
  get_operation_health: 'Operasyon sağlığı',
  service_health_sweep: 'Servis taraması',
  query_metrics: 'Metrik okuması',
  query_traces: 'Trace okuması',
  query_logs: 'Log okuması',
  list_services: 'Servis listesi',
  get_topology: 'Topoloji',
  compare_periods: 'Dönem karşılaştırması',
  compare_baseline: 'Ne değişti (taban karşılaştırması)',
  get_log_anomalies: 'ML anomalileri', // v0.10.1875
  explain_ml_event: 'ML olayı', // v0.10.1875
};

/** page_url bağlantısının etiketi (bilinmeyen araç: PAGE_LABEL_DEFAULT). */
export const PAGE_LABELS: Record<string, string> = {
  list_my_services: 'Servis listesinde aç',
  open_problems: "Problemler'de aç",
  find_traces: "Tümünü Traces'te aç",
  search_logs: "Loglar'da aç",
  explain_trace: "Trace'i aç",
  endpoint_metrics: "Endpoints'te aç (kaynak: metrik)",
  get_log_anomalies: "Anomaliler'de aç", // v0.10.1875
  explain_ml_event: 'Olayın sayfasını aç', // v0.10.1875
};
export const PAGE_LABEL_DEFAULT = 'Sayfada aç';

/** Kapsam rozeti — 8 neden (Go uistream.ScopeReasons). `team` ve `pattern` kapsamlı;
 *  `pattern`da ilk argüman kalıbın glob yazımıdır (scopeView). */
export const SCOPE_LABELS: Record<CosreScopeReason, { label: (team: string, n: number) => string; text: (team: string, n: number) => string }> = {
  team: {
    label: (team, n) => `Kapsam: ${team || 'takımın'} · ${n} servis`,
    text: team => `Bu soruda yalnız ${team || 'takımının'} servisleri arandı (sahip ya da SRE takımı).`,
  },
  no_identity: {
    label: () => 'Kapsamsız · kimlik yok',
    text: () => 'İstekte kullanıcı kimliği yok (servis hesabı ya da anahtar). Tüm servislerde arandı.',
  },
  no_team: {
    label: () => 'Kapsamsız · takımın tanımlı değil',
    text: () => 'Hesabında takım tanımlı değil. Tüm servislerde arandı.',
  },
  no_services: {
    label: team => `Kapsamsız · ${team || 'takım'} servis sahibi değil`,
    text: team => `${team || 'Takımın'} katalogda hiçbir servisin sahibi ya da SRE takımı değil. Tüm servislerde arandı.`,
  },
  disabled: {
    label: () => 'Kapsamsız · yönetici kapattı',
    text: () => 'Kullanıcı kapsamı ayarlarda kapalı. Tüm servislerde arandı.',
  },
  escape: {
    label: () => 'Tüm servisler · bu soru',
    text: () => 'Bu soruda açık bir kapsam (@ ya da / komutu) seçildi; takım kapsamı uygulanmadı. Sonraki soru yine takımına döner.',
  },
  error: {
    label: () => 'Kapsam okunamadı',
    text: () => 'Takım bilgisi okunamadı (okunamadı ≠ takımın yok). Tüm servislerde arandı.',
  },
  pattern: {
    label: (pattern, n) => `Kapsam: ${pattern || 'servis kalıbı'} · ${n} servis`,
    text: pattern => `Bu konuşmanın kapsamı adı ${pattern || 'yazdığın kalıba'} uyan servisler. Bırakmak için "tüm servisler" yaz.`,
  },
};
