// volumeSeries.ts — /traces üst histogramının SAF konfig katmanı (v0.9.843).
//
// Neden ayrı dosya: "hangi seri hangi eksende" bir DÜZEN kararıdır ve
// v0.9.843'te operatör isteğiyle bir kez TAKAS edildi (süre sağdan sola,
// span sayısı soldan sağa — Grafana düzeni). Bir daha sessizce geri
// dönmesin diye eşleme tablo-testli; VolumeChart.tsx TimeChart→uPlot'a
// bağlı olduğundan node ortamındaki vitest onu import edemezdi, bu modül
// ise saf (tip dışında hiçbir çizim bağımlılığı yok).
//
// EKSEN SÖZLEŞMESİ (v0.9.843): SÜRE hep SOL eksende, SAYI hep SAĞ eksende.
// TimeChart sol ekseni fmtLeft ile, sağ ekseni fmtRight ile biçimlendirir —
// yani takasla birlikte biçimlendirici de taşınmak ZORUNDA (sol: ms/s,
// sağ: TimeChart'ın fmtAxisTick kısaltması "30.9M"). VolumeChart bu yüzden
// fmtLeft={fmtVolumeDuration} geçer ve fmtRight vermez.

import type { SpanMetricSeries } from '@/lib/types';
import type { TimeChartSeries } from '@/components/charts/TimeChart';
import { statusColor } from '@/lib/statusColor';
import { STRIP_STAT_DEFAULT, stripStatLabel, type StripStat } from './stripStat';

/** Süre ekseni formatı (v0.9.73; v0.9.843'e dek SAĞ eksendeydi, artık SOL):
 *  <1000ms ms, aksi s. "3100 ms" gibi okunmaz büyük değerleri "3.1s" yapar;
 *  küçük gecikmelerde tam sayı ms okunur. Değer+birim şablonu → her birim
 *  dalı testli (v0.6.36 birim-karışımı disiplini). */
export function fmtVolumeDuration(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(v >= 10000 ? 0 : 1)}s`;
  return `${v.toFixed(v < 10 ? 1 : 0)}ms`;
}

export interface VolumeChartConfig {
  /** unix saniye, artan — paylaşılan x ekseni. */
  times: number[];
  series: TimeChartSeries[];
  /** bucket genişliği (dakika), başlık ipucu için. */
  bucketMin: number;
}

/** buildVolumeSeries — count/errors/p50 serilerini TimeChart konfigine çevirir.
 *  Çizim sırası = üst üste binme sırası: tam bar (accent) önce, hata payı
 *  (kırmızı) üstüne — böylece her barın DİBİNDE okunur; p50 çizgisi en son. */
/** v0.10.268 — çubuk etiketi: servis seçiliyken giriş span'i = istek ≈ trace
 *  ("traces"); servissiz pencerede her hop sayılır ("requests"). SAF. */
export function volumeUnitLabel(serviceScoped: boolean): string {
  return serviceScoped ? 'traces' : 'requests';
}

/** v0.10.513 — üçüncü seri artık seçilebilir istatistik (p50/p95/p99,
 *  varsayılan p95, stripStat.ts); seri anahtarı `rt`, etiket istatistiği söyler. */
export function buildVolumeSeries(
  count: SpanMetricSeries[] | null,
  errors: SpanMetricSeries[] | null,
  latency: SpanMetricSeries[] | null,
  unit: string = 'traces',
  stat: StripStat = STRIP_STAT_DEFAULT,
): VolumeChartConfig {
  const cPts = count?.[0]?.points ?? [];
  if (!cPts.length) return { times: [], series: [], bucketMin: 1 };
  const eMap = new Map((errors?.[0]?.points ?? []).map(p => [p.time, p.value]));
  const pMap = new Map((latency?.[0]?.points ?? []).map(p => [p.time, p.value]));
  const t = cPts.map(p => Math.round(p.time / 1e9)); // ns → unix sec
  const total = cPts.map(p => p.value);
  const err = cPts.map(p => Math.min(eMap.get(p.time) ?? 0, p.value));
  // v0.9.73 — p50 GAP'li: örnek olmayan (ya da 0 dönen) bucket'ta null →
  // çizgi tabana çakmaz, gerçek boşluk gösterir. Eski `?? 0` her boş
  // bucket'ı 0ms'e çekip sahte iniş-çıkış üretiyordu.
  const p50d: (number | null)[] = cPts.map(p => {
    const v = pMap.get(p.time);
    return v && v > 0 ? v : null;
  });
  const dt = t.length > 1 ? Math.round((t[1] - t[0]) / 60) : 1;
  // v0.10.322 (operatör: "Süre grafiği de çok zigzaglı olmasın") — medyan
  // çizgisi 5 kovalık merkezli hareketli ortalama (2 dk kovada 10 dk).
  // GAP'ler korunur (null merkez null kalır); kenarlarda mevcut komşularla.
  // Yoğun seride (≥20 kova) noktalar kapalı — nokta işaretleri zikzağın
  // görsel yarısıydı. Başlıktaki <STAT> MAX ham kovadan okunur; etiket
  // yumuşatmayı söyler.
  const p50s = smoothCentered(p50d, RT_SMOOTH_WINDOW);
  // v0.10.656 (operatör: "süre solda, adet sağda") — SÜRE çizgisi SOL
  // eksende, SAYIM çubukları SAĞ eksende (v0.9.843 Grafana düzeni geri;
  // v0.10.268'in Dynatrace takası geri alındı). Biçimlendirici eksenle
  // birlikte taşınır (VolumeChart fmtLeft). Tek kaynak: aşağıdaki axis alanı. Seriler giriş span'ı (server/consumer) kapsamlı
  // (Traces.tsx şerit filtresi) — istek ≈ trace.
  const series: TimeChartSeries[] = [
    // v0.9.843 — bar'lar SAĞ eksende (span sayısı).
    { key: 'total', label: unit, data: total, color: 'var(--accent)', type: 'bar', axis: 'right' },
    { key: 'error', label: 'error ' + unit, data: err, color: statusColor('error'), type: 'bar', axis: 'right' },
    // v0.9.73 — kalın çizgi + nokta: seyrek p50 örnekleri artık okunur.
    // v0.9.843 — süre SOL eksende (Grafana düzeni).
    { key: 'rt', label: `response time (${stripStatLabel(stat)})` /* v0.10.663 (operatör): "5-kova ort." ibaresi kalktı; yumuşatma penceresi ipucuda değil, davranışta */, data: p50s, color: 'var(--orange)', type: 'line', axis: 'left', width: 2, pointsShow: t.length < 20 },
  ];
  return { times: t, series, bucketMin: Math.max(1, dt) };
}


/** v0.10.322 — yanıt-süresi çizgisi yumuşatma penceresi (kova sayısı, tek).
 *  v0.10.513'e dek P50_SMOOTH_WINDOW; çizgi artık seçilebilir istatistik. */
export const RT_SMOOTH_WINDOW = 5;

/**
 * smoothCentered — merkezli hareketli ortalama, null-farkında. Merkez null
 * ise null (GAP korunur); değilse penceredeki null olmayan komşuların
 * ortalaması. window ≤ 1 → aynı dizi. SAF.
 */
export function smoothCentered(data: (number | null)[], window: number): (number | null)[] {
  if (window <= 1 || data.length < 3) return data;
  const half = Math.floor(window / 2);
  return data.map((v, i) => {
    if (v == null) return null;
    let sum = 0; let n = 0;
    for (let j = Math.max(0, i - half); j <= Math.min(data.length - 1, i + half); j++) {
      const x = data[j];
      if (x != null) { sum += x; n++; }
    }
    return n ? sum / n : v;
  });
}

// ── v0.10.323 (operatör, prod: db.statement ~ … filtresinde şerit boş) ──
// Şerit v0.10.268'den beri GİRİŞ span'ı (server/consumer) kapsamlı: istek ≈
// trace. Ama operatörün filtresi tablo tarafında TRACE düzeyinde uygulanır
// ("trace'in HERHANGİ bir span'i eşleşir"), şeritte ise aynı span'de AND'lenir.
// db.statement / messaging.* gibi alanlar giriş span'ında YAŞAMAZ → şerit
// "sıfır", tablo dolu. Çözüm dürüst ve ucuz: filtre giriş span'ında
// yaşamayan bir alanı hedefliyorsa (ya da serbest metin araması varsa) şerit
// EŞLEŞEN SPAN'LERİ sayar (kind kısıtı kalkar) ve bunu etikette söyler:
// birim "spans", medyan o span'lerin süresi (db.statement için = sorgunun
// kendi gecikmesi). Trace düzeyine çevirmek (aday id kümesi) prod ölçeğinde
// ikinci bir tam tarama olurdu; bilinçli yapılmadı.
export type StripScope = 'entry' | 'spans';

// v0.10.730 (operator-reported, prod: "operation name olarak bir sorgu
// seçince Traces'taki histogram hiç gelmiyor" — name = "INSERT
// paku01_prd.AHS_DOSYA", tablo dolu, şerit 0) — `name` / `span.name`
// ENTRY_KEYS'ten ÇIKARILDI.
//
// Neden yanlıştı: liste bu anahtarları HER span'de eşler, giriş span'ı ise
// yalnız server/consumer. Bir span ADI her kind'da yaşar — DB istemci
// span'inin adı "INSERT <tablo>", Kafka üreticisininki topic. Ad giriş
// span'ine AİT OLDUĞUNU garanti etmez; ettiğini varsaymak v0.10.323'ün
// db.statement olayının birebir ikizini üretti (kind kısıtı AND'lenince
// eşleşme sıfır, grafik boş, tablo dolu).
//
// Kabul edilen bedel: adı giriş span'ine ait olan durumda (GET /api/x)
// şerit artık o adı taşıyan TÜM span'leri sayar — çağıranın istemci
// span'i aynı adı taşıyorsa sayı bir miktar büyür. Servis seçiliyken
// (yaygın hâl) ikisi aynı serviste olmadığı için fark yok; boş grafik
// ise her hâlde toplam kayıptı. Kapsam etikette görünür ("spans") ve
// ipucu neyi saydığını yazar.
//
// v0.10.1006 (operator-reported, prod: `function_code = …` çipinde tablo dolu,
// şerit "No traces in view to bucket", 0 istek) — `channel_code` /
// `function_code` / `function_id` ENTRY_KEYS'ten ÇIKARILDI.
//
// Neden yanlıştı: bu iş kimliklerini giriş span'i TAŞIMIYOR. Prod'da fonksiyon
// kodunu yalnız log-yayın span'leri (MQ producer) taşıyor — operatörün
// trace'inde 12 span'in 4'ü; kök HTTP span'inde yok. Üçü aynı yayın mesajının
// alanları, aynı span'lerde yaşıyorlar. Giriş span'inde olduklarını VARSAYMAK
// kind kısıtını AND'leyip grafiği boşaltıyordu — db.statement (v0.10.323) ve
// name (v0.10.730) olaylarının üçüncü ikizi.
//
// Kabul edilen bedel aynı: şerit bu çiplerde eşleşen SPAN'leri sayar (bir
// trace birkaç yayın span'i taşıyorsa sayı trace sayısından büyük), birim
// "spans" ve ipucu neyi saydığını yazar. Boş grafik toplam kayıptı.
/** Giriş span'ında yaşayan anahtarlar / önekler — bunlar şeridi giriş kapsamında tutar. */
const ENTRY_KEYS = new Set(['service.name', 'kind', 'status_code', 'status', 'cluster', 'deployment.environment',
  'span.kind']);
const ENTRY_PREFIXES = ['http.', 'url.', 'server.', 'k8s.', 'resource.', 'host.', 'service.', 'deployment.', 'telemetry.', 'process.', 'os.', 'container.', 'cloud.'];

export function isEntrySpanKey(key: string): boolean {
  const k = key.trim().toLowerCase();
  if (ENTRY_KEYS.has(k)) return true;
  return ENTRY_PREFIXES.some(p => k.startsWith(p));
}

/** stripScope — filtreler + serbest metin → şerit kapsamı. SAF. */
export function stripScope(filters: { k: string }[], search: string): StripScope {
  if (search.trim()) return 'spans';
  return filters.every(f => isEntrySpanKey(f.k)) ? 'entry' : 'spans';
}

/**
 * stripRootOnly — v0.10.1008 (operator-reported, prod: `function_code` çipi +
 * Root → liste de şerit de boş). Şeridin Root bayrağı "kök span" yüklemini
 * çiple AYNI span'de AND'ler; çip giriş span'inde yaşamıyorsa (spans kapsamı)
 * eşleşme sıfırdır. O kapsamda şerit zaten eşleşen span'leri sayıyor — Root
 * bayrağı gönderilmez (liste kökü trace düzeyinde ayrıca doğrular). Giriş
 * kapsamında bayrak aynen gider (v0.10.484). SAF.
 */
export function stripRootOnly(scope: StripScope, rootOnly: boolean): boolean {
  return scope === 'entry' && rootOnly;
}

/**
 * volumeEmptyNote — v0.10.1011: şerit boşken yazılan cümle. SAF.
 *
 * Operator-reported (prod): `function_code` çipi + Errors → liste dolu (v0.10.1010,
 * hata trace düzeyinde), şerit "No traces in view to bucket". Şerit spans
 * kapsamında Errors'u çiple AYNI span'de arar ("süzgece uyan span hatalı mı");
 * fonksiyon kodunu taşıyan span'ler hata vermediği için sayım GERÇEKTEN sıfır.
 * Hata başka span'de — bunu grafiğe çevirmek trace düzeyinde bir kesişim serisi
 * ister (pahalı; bilinçli yapılmadı). Çıplak "No traces" ise listeyle çelişen
 * bir yalandı: boş durum artık nedenini ve ne yapılacağını söyler.
 *
 * v0.10.1082 (operator-reported, prod: "Error seçildiğinde histogram gelmiyor")
 * — Errors + span-düzeyi çip artık /api/traces/error-histogram'dan okunur ve
 * listeyle AYNI kümeyi sayar (Traces.tsx errorStripEligible); o yolda boş şerit
 * = boş liste, cümle YALAN olurdu. Cümle yalnız DOĞRU olduğu hâlde kalır:
 * metric-batch yolu (span düzeyi — ör. serbest metin araması + Errors), birim
 * "spans", Errors açık VE liste dolu. Liste boşken neden "başka span" olsun.
 */
export const VOLUME_EMPTY_DEFAULT = 'No traces in view to bucket.';
export function volumeEmptyNote(unit: string, hasError: boolean, listNonEmpty: boolean, errorStrip: boolean): string {
  if (!errorStrip && listNonEmpty && unit === 'spans' && hasError) {
    return "Süzgece uyan span'lerin hiçbiri hatalı değil — hata aynı trace'in başka bir span'inde. Liste trace düzeyinde eşleşir; grafiği görmek için Errors'u kaldırın.";
  }
  return VOLUME_EMPTY_DEFAULT;
}

/** errorStripUnit — v0.10.1082: Errors şeridinin birimi sunucu kipinden. SAF. */
export function errorStripUnit(mode: 'span' | 'trace'): string {
  return mode === 'trace' ? 'traces' : 'spans';
}

/** errorStripHint — v0.10.1082: Errors şeridinin neyi saydığı (başlık ipucu). SAF.
 *  v0.10.1101 — scopeOnly: çipsiz Errors (yalnız servis / ortam / küme). */
export function errorStripHint(mode: 'span' | 'trace', capped: boolean, scopeOnly = false): string {
  const base = scopeOnly
    ? "Errors: kapsamdaki TÜM hatalı span'ler sayılır (giriş, istemci, iç) — listenin yüklemiyle aynı (liste bu span'leri taşıyan trace'leri gösterir; liste tavanlıysa yalnız en yeni trace'leri)."
    : mode === 'trace'
      ? "Errors + çip: çipe uyan span'ler hatasız, hata aynı trace'in başka span'inde — liste trace düzeyinde eşleşir; şerit listenin trace'lerini başlangıç zamanına göre sayar."
      : "Errors + çip: çipe uyan hatalı span'ler sayılır — listeyle aynı küme (liste bu span'leri taşıyan trace'leri gösterir).";
  return capped ? base + ' Aday kümesi tavana çarptı: en yeni trace\'ler sayıldı.' : base;
}

/** Şerit başlığı sayıları (TOTAL · ERRORS · ERR RATE · <stat> AVG). */
export interface StripHeaderStats { total: number; err: number; errRate: number; rtAvg: number }

/**
 * stripHeaderStats — SAF (v0.10.1101; önce Traces.tsx'te satır içiydi): başlık
 * sayıları ŞERİDİN KENDİ cevabından — count / errors / rt aynı uçtan (Errors
 * şeridinde /api/traces/error-histogram, diğerlerinde metric-batch). Başlık ile
 * çubuklar ayrı kaynaktan okuyamaz; Errors şeridinde ikisi de listenin kümesi.
 */
export function stripHeaderStats(vol: {
  count: SpanMetricSeries[] | null; errors: SpanMetricSeries[] | null; rt: SpanMetricSeries[] | null;
} | null | undefined): StripHeaderStats {
  const cPts = vol?.count?.[0]?.points ?? [];
  const eMap = new Map((vol?.errors?.[0]?.points ?? []).map(p => [p.time, p.value]));
  let total = 0, err = 0;
  for (const p of cPts) { total += p.value; err += eMap.get(p.time) ?? 0; }
  const rtAvg = weightedStatAvg(vol?.count ?? null, vol?.rt ?? null);
  return { total, err, errRate: total > 0 ? (err / total) * 100 : 0, rtAvg };
}

/** volumeUnitFor — birim etiketi: spans kapsamında "spans", değilse eski kural. */
export function volumeUnitFor(serviceScoped: boolean, scope: StripScope): string {
  return scope === 'spans' ? 'spans' : volumeUnitLabel(serviceScoped);
}

/** volumeHint — başlık ipucu (title); kapsam neyi saydığını söyler. */
export function volumeHint(unit: string): string {
  return unit === 'spans'
    ? 'Filtre ya da arama giriş span\'ı dışındaki bir alanı hedefliyor (ör. db.statement): eşleşen SPAN\'ler sayılır, medyan o span\'lerin süresi. Tablo yine trace düzeyinde eşleşir.'
    : 'Giriş span\'leri (server/consumer) sayılır: servis seçiliyken istek = trace; servissiz pencerede her hop bir kez sayılır.';
}

/**
 * weightedStatAvg — v0.10.660 (operatör: "P95 max yerine seçilen aralıktaki
 * ortalama"). Seçili istatistiğin (p50/p95/p99) kova serisinin İSTEK-AĞIRLIKLI
 * ortalaması: Σ(stat_i × istek_i) / Σ istek_i. Düz kova ortalaması değil:
 * 3 isteklik gece kovası ile 3M isteklik gündüz kovası eşit sayılmasın.
 * İstatistiği olmayan kova ya da 0 istek ağırlıksız; toplam ağırlık 0 → 0.
 */
export function weightedStatAvg(
  count: SpanMetricSeries[] | null | undefined,
  stat: SpanMetricSeries[] | null | undefined,
): number {
  const sMap = new Map((stat?.[0]?.points ?? []).map(p => [p.time, p.value]));
  let num = 0, den = 0;
  for (const c of count?.[0]?.points ?? []) {
    const v = sMap.get(c.time);
    if (v == null || !Number.isFinite(v) || !(c.value > 0)) continue;
    num += v * c.value;
    den += c.value;
  }
  return den > 0 ? num / den : 0;
}

// ── v0.10.738 (operatör: Problems'tan pivot sonrası pencereyi 6 sa / 24 sa
// yapınca "histogram tek bir bar çıkıyor") ──
// Sebep tasarım gereği: 35 dakikalık bir hata patlaması 24 saatlik pencerede
// 10-30 dk'lık kovalara düşer → 1-2 bar. Kova genişliği piksel bütçesinden
// (v0.9.715 "barlar çok küçülmüş"), inceltmek o kararı geri alırdı. Çözüm
// dürüst bir affordance: veri pencerenin küçük bir kısmına sıkışmışsa şerit
// "veriye sığdır" der ve tık pencereyi o aralığa (sürükle-seçim gibi, zoom
// yığınına) daraltır. SAF.
export interface DataExtent {
  fromSec: number;
  toSec: number;
  /** Sıfır olmayan kovaların pencereye oranı (0..1). */
  fraction: number;
}

export function dataExtent(
  count: SpanMetricSeries[] | null | undefined,
  windowFromSec: number,
  windowToSec: number,
  maxFraction = 0.25,
): DataExtent | null {
  const pts = (count?.[0]?.points ?? []).filter(p => p.value > 0);
  if (pts.length === 0 || windowToSec <= windowFromSec) return null;
  const all = count?.[0]?.points ?? [];
  const stepSec = all.length >= 2 ? Math.max(1, (all[1].time - all[0].time) / 1e9) : 60;
  const first = Math.min(...pts.map(p => p.time)) / 1e9;
  const last = Math.max(...pts.map(p => p.time)) / 1e9 + stepSec;
  const fraction = (last - first) / (windowToSec - windowFromSec);
  if (fraction >= maxFraction) return null;
  const pad = Math.max(stepSec, (last - first) * 0.1);
  return {
    fromSec: Math.max(windowFromSec, first - pad),
    toSec: Math.min(windowToSec, last + pad),
    fraction,
  };
}
