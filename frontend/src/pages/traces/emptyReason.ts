// emptyReason.ts — v0.10.530: /traces boş-durum metninin karar noktası.
// Operator-reported (prod, 1 saatlik pencere): aramalı liste boş, 5 dk MV'de
// servis için span var → metin "ham veri TTL'i aştı" dedi. Pencere saklama
// süresinin içindeydi; arama metni o span'lerde geçmiyordu. "MV>0 ∧ liste
// boş" iki nedeni ayıramaz; ayıran, sunucunun yüklemsiz ham sayımı
// (emptyDiag.serviceSpans). Saf ve tablo-testli: yanlış dal yanlış tavsiye
// verir (Aggregate'e geç vs. aramayı değiştir) ve bunu hiçbir tip görmez.

export type TracesEmptyReason =
  | 'narrowed'    // arka uç pencereyi kaynak bütçesiyle daralttı — "bakılamadı"
  | 'predicate'   // ham span var, arama/çip hiçbirine uymuyor
  | 'aged'        // MV var, ham span bu pencerede YOK — saklama/ingest boşluğu
  | 'unmeasured'  // MV var, ham eşleşme yok, sunucu ham sayımı veremedi
  | 'generic';    // servis/arama yok ya da MV de boş — genel tavsiye

export interface TracesEmptyInput {
  narrowed: boolean;
  service: string;
  search: string;
  /** 5 dk MV'nin servis için gördüğü span; null = servis yok / okunamadı; undefined = yükleniyor. */
  mvSpans: number | null | undefined;
  /** Sunucunun yüklemsiz ham sayımı; undefined = ölçülmedi. */
  serviceSpans: number | undefined;
}

export function tracesEmptyReason(i: TracesEmptyInput): TracesEmptyReason {
  if (i.narrowed) return 'narrowed';
  if (!i.service || !i.search || typeof i.mvSpans !== 'number' || i.mvSpans <= 0) return 'generic';
  if (i.serviceSpans === undefined) return 'unmeasured';
  return i.serviceSpans > 0 ? 'predicate' : 'aged';
}

/**
 * v0.10.753 — `?traceId=` (Trace ID kutusu / derin bağlantı) ile gelen boş
 * listenin metni. Sunucu pencereyi trace'in gerçek zamanına çıpalar
 * (identity.traceId + windowFrom/To); hits=0 → son 90 günün trace
 * özetinde yok; hits>0 ama liste boş → bulundu, süzgeç/Root/hata eledi.
 * Saf: fmt zaman biçimleyicisi dışarıdan (tsLong).
 */
export function traceIdIdentityText(
  identity: { traceId?: boolean; hits: number; windowFromNs?: number; windowToNs?: number } | undefined,
  traceId: string,
  fmt: (ns: number) => string,
): string | null {
  if (!identity?.traceId) return null;
  const id = traceId.trim() || '(id)';
  if (identity.hits <= 0) {
    return `Trace ${id} son 90 günün trace özetinde (trace_summary_5m) bulunamadı — id'yi ve saklama süresini kontrol et; trace detay sayfası (id'yi Trace ID kutusuna yazıp Enter) 31 günlük ham taramayı da dener.`;
  }
  const win = identity.windowFromNs && identity.windowToNs
    ? ` Pencere trace zamanına çıpalandı: ${fmt(identity.windowFromNs)} → ${fmt(identity.windowToNs)}.`
    : '';
  return `Trace ${id} bulundu ama listeye girmedi: servis / süzgeç / Root / yalnız-hata seçimi onu eledi.${win}`;
}
