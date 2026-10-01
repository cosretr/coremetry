// argocdForm — v0.10.974 — Ayarlar › Argo CD sekmesinin SAF form çekirdeği
// (operatör onayı 2026-09-27, "Onay rollout için önerin"; mockup
// argocd-canvas Main / States; sunucu internal/argocd/{settings,put}.go).
//
// Neden ayrı ve saf: sekme dört tablo, satır içi form ve keşif taşıyor;
// hangi alanın PUT'a nasıl gideceği (tokenRef/clearTokenRef), hata yolunun
// hangi taslak satırına düştüğü ve istemci doğrulaması sunucuyla AYNI kuralı
// koşmalı. Bu kararlar bileşende dağılırsa test edilemez; burada tablo-güdümlü
// vitest'le çivili (argocdForm.test.ts).
//
// Sözleşmeler:
//   • Taslak GET `settings`ten (SAKLANAN) kurulur, `resolved`tan DEĞİL:
//     uygulanan değer (kelepçe + varsayılan) geri yazılırsa operatörün boş
//     bıraktığı alan sabit bir sayıya dönerdi. Eksik diziler = [], hub'da
//     eksik injectClusterLabel = true.
//   • PUT gövdesi TASLAK sırasıyla gider (tablo görünümü sıralı olsa bile);
//     sunucunun `instances[N]` / `hubs[N]` yolu taslak dizisine geri eşlenir.
//   • Token: yazılan ref → tokenRef; kayıtlı satır dokunulmamış → "" (sunucu
//     korur); "Referansı kaldır" → "" + clearTokenRef; YENİ satırın boş ref'i
//     → clearTokenRef (aynı kimlikli silinmiş satırın ref'ini miras almasın).
//   • pins[] yüklendiği gibi geri gider (düzenleyici yok; v0.10.985'ten beri
//     metrik işçisinin eşleyicisi pinleri match_method='manual' kopyalar).
//   • apiUrl önizlemesi Go NormalizeAPIURL'ün ELLE ayrıştırılmış aynası:
//     `new URL()` :443'ü düşürür ve yolu yeniden kodlar — sunucunun
//     kaydedeceği biçim o olmazdı.
//   • v0.10.978 — iyimser ön koşul: her PUT GET'te görülen `updatedAt`i
//     `expectedUpdatedAt` olarak taşır (kayıtsız blob = 0); 409 stale'de
//     sekme yeniden GET'ler ve mergeDraft ile üç yönlü birleştirir (alan /
//     satır başına: kullanıcı değiştirdi VE sunucu değiştirmedi → kullanıcının;
//     yoksa sunucunun, düzenleme "atıldı" sayılır). Karar: ~40 satırlık saf
//     birleştirme, düz "düzenlemeleri at" yerine — tablo düzenlemesi pahalı.
import type {
  ArgoCDBound, ArgoCDInstanceInput, ArgoCDPin, ArgoCDSettings, ArgoCDSettingsInput,
  ArgoCDTokenStatus, ThanosClusterSnapshot,
} from '@/lib/types';

// ── Remote Cluster yüzü ────────────────────────────────────────────────────

/** v0.10.974 — Thanos anlık görüntüsünün sekmenin gördüğü dar yüzü
 *  (devre dışılar dahil; id'siz satır atılır). `label` = Argo sorgularına
 *  eklenecek küme etiketi (`cluster="hub-1"`), etiket yoksa ''. */
export interface RemoteCluster { id: string; name: string; enabled: boolean; label: string }

export function remoteClustersFrom(clusters: ThanosClusterSnapshot[] | undefined | null): RemoteCluster[] {
  const out: RemoteCluster[] = [];
  for (const c of clusters ?? []) {
    const id = (c.id ?? '').trim();
    if (!id) continue;
    const ln = (c.thanosLabelName ?? '').trim();
    const lv = (c.thanosLabelValue ?? '').trim() || c.name;
    out.push({ id, name: c.name || id, enabled: !!c.enabled, label: ln ? `${ln}="${lv}"` : '' });
  }
  return out;
}

export function hubName(clusterId: string, clusters: RemoteCluster[]): string {
  return clusters.find(c => c.id === clusterId)?.name ?? clusterId;
}

// ── Taslak modeli ──────────────────────────────────────────────────────────

export type Origin = 'saved' | 'new';

export interface HubDraft { key: string; clusterId: string; inject: boolean }

export interface InstanceDraft {
  /** İstemci anahtarı — sıralama/yeniden adlandırmada sabit (React key, hata eşlemesi). */
  key: string;
  origin: Origin;
  /** Kayıtlı satırın GET'teki kimliği ('' = yeni satır). */
  savedId: string;
  id: string;
  hubClusterId: string;
  name: string;
  hubNamespace: string;
  metricsJob: string;
  apiUrl: string;
  /** Kayıtlı satırın SAKLI referansı (gösterim; secret değil). Yeni satırda ''. */
  storedRef: string;
  /** Yazılan YENİ referans; '' = kayıtlı korunur (yeni satırda: referans yok). */
  tokenInput: string;
  /** "Referansı kaldır" — Kaydet'te clearTokenRef. */
  clearToken: boolean;
  insecureSkipVerify: boolean;
  enabled: boolean;
  discovered: boolean;
  appsAnyNamespace: boolean;
}

export const ADV_NUM_KEYS = [
  'apiWorker.rps', 'apiWorker.burst', 'apiWorker.maxConcurrent',
  'classification.windowMin', 'classification.outOfBandLookbackMin',
  'reader.maxSeries', 'reader.maxBodyMiB', 'reader.timeoutS',
  'intervals.metricsS', 'intervals.inventoryMin', 'intervals.mapperMin', 'intervals.classifierReevalH',
  'mapping.nameConfidence', 'mapping.namespaceConfidence',
] as const;
export type AdvNumKey = typeof ADV_NUM_KEYS[number];
export type MetricsOnlyMode = '' | 'estimate' | 'unknown';
/** v0.10.974 — ETKİN kip: '' sunucuda `estimate`e çözülür (settings.go
 *  MetricsOnlyEstimate); sayaç ve "Gelişmiş" özeti ham dizgeyi değil bunu
 *  karşılaştırır — mockup'ta da `estimate` varsayılandır, değişiklik sayılmaz. */
export const effMode = (m: MetricsOnlyMode): 'estimate' | 'unknown' => (m === 'unknown' ? 'unknown' : 'estimate');

export interface Draft {
  enabled: boolean;
  /** v0.10.983 — argocd-metrics işçisi (`metricsWorker.enabled`); yalnız `enabled` açıkken anlamlı. */
  metricsWorker: boolean;
  hubs: HubDraft[];
  instances: InstanceDraft[];
  envList: string[];
  /** Kutudaki METİN ('' = varsayılan; ondalık virgül kabul). */
  adv: Record<AdvNumKey, string>;
  metricsOnlyMode: MetricsOnlyMode;
}

let seq = 0;
/** Yeni satır anahtarı (sayfa ömrü boyunca tekil). */
export function newKey(prefix: string): string { seq += 1; return `${prefix}${seq}`; }

function readNum(s: ArgoCDSettings, k: AdvNumKey): number | undefined {
  switch (k) {
    case 'apiWorker.rps': return s.apiWorker?.rps;
    case 'apiWorker.burst': return s.apiWorker?.burst;
    case 'apiWorker.maxConcurrent': return s.apiWorker?.maxConcurrent;
    case 'classification.windowMin': return s.classification?.windowMin;
    case 'classification.outOfBandLookbackMin': return s.classification?.outOfBandLookbackMin;
    case 'reader.maxSeries': return s.reader?.maxSeries;
    case 'reader.maxBodyMiB': return s.reader?.maxBodyMiB;
    case 'reader.timeoutS': return s.reader?.timeoutS;
    case 'intervals.metricsS': return s.intervals?.metricsS;
    case 'intervals.inventoryMin': return s.intervals?.inventoryMin;
    case 'intervals.mapperMin': return s.intervals?.mapperMin;
    case 'intervals.classifierReevalH': return s.intervals?.classifierReevalH;
    case 'mapping.nameConfidence': return s.mapping?.nameConfidence;
    case 'mapping.namespaceConfidence': return s.mapping?.namespaceConfidence;
  }
}

/** Sayıyı kutuya: 0/yok → '' (varsayılan), ondalık virgülle. */
export function numToInput(v: number | undefined | null): string {
  return v === undefined || v === null || v === 0 ? '' : String(v).replace('.', ',');
}

/** Kutudan sayıya: '' → null (varsayılan), virgül = ondalık; çöp → NaN. */
export function parseAdvInput(raw: string): number | null {
  const t = raw.trim().replace(',', '.');
  if (t === '') return null;
  // Number('1e3') / Number('0x10') / Number(' ') kabul ederdi; kutu düz ondalık alır.
  if (!/^-?\d+(\.\d+)?$/.test(t)) return NaN;
  return Number(t);
}

/** v0.10.974 — GET `settings` (SAKLANAN) → taslak. */
export function draftFromSettings(s: ArgoCDSettings): Draft {
  const adv = {} as Record<AdvNumKey, string>;
  for (const k of ADV_NUM_KEYS) adv[k] = numToInput(readNum(s, k));
  const mode = s.classification?.metricsOnlyMode ?? '';
  return {
    enabled: !!s.enabled,
    metricsWorker: !!s.metricsWorker?.enabled,
    hubs: (s.hubs ?? []).map(h => ({ key: `h:${h.clusterId}`, clusterId: h.clusterId, inject: h.injectClusterLabel ?? true })),
    instances: (s.instances ?? []).map(i => ({
      key: `s:${i.id}`, origin: 'saved' as const, savedId: i.id,
      id: i.id, hubClusterId: i.hubClusterId ?? '', name: i.name ?? '', hubNamespace: i.hubNamespace ?? '',
      metricsJob: i.metricsJob ?? '', apiUrl: i.apiUrl ?? '',
      storedRef: i.tokenRef ?? '', tokenInput: '', clearToken: false,
      insecureSkipVerify: !!i.insecureSkipVerify, enabled: !!i.enabled,
      discovered: !!i.discovered, appsAnyNamespace: !!i.appsAnyNamespace,
    })),
    envList: [...(s.envList ?? [])],
    adv,
    metricsOnlyMode: mode === 'estimate' || mode === 'unknown' ? mode : '',
  };
}

/** Tek satırın PUT biçimi (token kuralları dosya başlığında). */
export function instanceInput(i: InstanceDraft): ArgoCDInstanceInput {
  const typed = i.tokenInput.trim();
  const out: ArgoCDInstanceInput = {
    id: i.id.trim(), hubClusterId: i.hubClusterId, name: i.name.trim(), hubNamespace: i.hubNamespace.trim(),
    metricsJob: i.metricsJob.trim(), apiUrl: i.apiUrl.trim(), tokenRef: typed,
    insecureSkipVerify: i.insecureSkipVerify, enabled: i.enabled, discovered: i.discovered,
    appsAnyNamespace: i.appsAnyNamespace,
  };
  if (!typed && (i.origin === 'new' || i.clearToken)) out.clearTokenRef = true;
  return out;
}

function advNum(d: Draft, k: AdvNumKey): number {
  const v = parseAdvInput(d.adv[k]);
  return v === null || !Number.isFinite(v) ? 0 : v;
}

/** v0.10.974 — taslak → PUT gövdesi: TASLAK sırası, her bölüm, pins aynen.
 *  v0.10.978 — `expectedUpdatedAt`: GET'te görülen damga (kayıtsız blob 0);
 *  sunucu tutmazsa 409 stale. Her zaman gider — "gönderilmedi" yalnız API/token
 *  çağıranların yoludur. */
export function toPutBody(d: Draft, pins: ArgoCDPin[], expectedUpdatedAt: number): ArgoCDSettingsInput {
  return {
    expectedUpdatedAt,
    enabled: d.enabled,
    // v0.10.983 — sunucu da kapalı entegrasyonda işçi bayrağını kapatır (Validate).
    metricsWorker: { enabled: d.enabled && d.metricsWorker },
    hubs: d.hubs.map(h => ({ clusterId: h.clusterId, injectClusterLabel: h.inject })),
    envList: [...d.envList],
    instances: d.instances.map(instanceInput),
    apiWorker: { rps: advNum(d, 'apiWorker.rps'), burst: advNum(d, 'apiWorker.burst'), maxConcurrent: advNum(d, 'apiWorker.maxConcurrent') },
    classification: {
      windowMin: advNum(d, 'classification.windowMin'),
      outOfBandLookbackMin: advNum(d, 'classification.outOfBandLookbackMin'),
      metricsOnlyMode: d.metricsOnlyMode,
    },
    pins,
    reader: { maxSeries: advNum(d, 'reader.maxSeries'), maxBodyMiB: advNum(d, 'reader.maxBodyMiB'), timeoutS: advNum(d, 'reader.timeoutS') },
    intervals: {
      metricsS: advNum(d, 'intervals.metricsS'), inventoryMin: advNum(d, 'intervals.inventoryMin'),
      mapperMin: advNum(d, 'intervals.mapperMin'), classifierReevalH: advNum(d, 'intervals.classifierReevalH'),
    },
    mapping: { nameConfidence: advNum(d, 'mapping.nameConfidence'), namespaceConfidence: advNum(d, 'mapping.namespaceConfidence') },
  };
}

// ── Türkçe biçim yardımcıları ──────────────────────────────────────────────

/** tr-TR sayı: binlik ".", ondalık "," (1184 → "1.184", 0.5 → "0,5"). */
export function fmtTr(n: number): string {
  const neg = n < 0;
  const [int, dec] = String(Math.abs(n)).split('.');
  const grouped = int.replace(/\B(?=(\d{3})+(?!\d))/g, '.');
  return (neg ? '-' : '') + grouped + (dec ? `,${dec}` : '');
}

const UNIT_LOC: Record<string, string> = { '0': 'da', '1': 'de', '2': 'de', '3': 'te', '4': 'te', '5': 'te', '6': 'da', '7': 'de', '8': 'de', '9': 'da' };
// on, yirmi, otuz, kırk, elli, altmış, yetmiş, seksen, doksan
const TENS_LOC: Record<string, string> = { '1': 'da', '2': 'de', '3': 'da', '4': 'ta', '5': 'de', '6': 'ta', '7': 'te', '8': 'de', '9': 'da' };

/**
 * v0.10.974 — kimlik için bulunma eki, kesme işaretiyle: hub-1 → "'de",
 * hub-3 → "'te", cluster-a → "'da", platform-gitops → "'ta". Sayı sonu
 * okunuşa göre (1 bir, 3 üç, 10 on, 40 kırk, 100 yüz); harf sonu son ünlüye
 * (kalın a ı o u → a) ve sert ünsüze (f s t k ç ş h p → t) göre. Kendi
 * uygulaması: pages/trace/trSuffix.ts izlenmeyen ve başka iş akışının.
 */
export function trLocative(word: string): string {
  const w = word.trim().toLocaleLowerCase('tr');
  const digits = /(\d+)$/.exec(w)?.[1];
  if (digits) {
    const last = digits[digits.length - 1];
    if (last !== '0' || digits.length === 1) return `'${UNIT_LOC[last]}`;
    const tens = digits[digits.length - 2];
    if (tens !== '0') return `'${TENS_LOC[tens]}`;
    return "'de"; // yüz, bin
  }
  const vowels = w.match(/[aeıioöuü]/g);
  const back = vowels ? /[aıou]/.test(vowels[vowels.length - 1]) : false;
  const hard = /[fstkçşhp]$/.test(w);
  return `'${hard ? 't' : 'd'}${back ? 'a' : 'e'}`;
}

// Üçüncü tekil iyelik (sayı sonrası, kesmeyle): bir→biri, iki→ikisi, üç→üçü …
const UNIT_POSS: Record<string, string> = { '0': 'ı', '1': 'i', '2': 'si', '3': 'ü', '4': 'ü', '5': 'i', '6': 'sı', '7': 'si', '8': 'i', '9': 'u' };
// on→onu, yirmi→yirmisi, otuz→otuzu, kırk→kırkı, elli→ellisi, altmış→altmışı, yetmiş→yetmişi, seksen→sekseni, doksan→doksanı
const TENS_POSS: Record<string, string> = { '1': 'u', '2': 'si', '3': 'u', '4': 'ı', '5': 'si', '6': 'ı', '7': 'i', '8': 'i', '9': 'ı' };

/**
 * v0.10.974 — sayı için üçüncü tekil iyelik eki, kesmeyle: 1 → "'i", 2 →
 * "'si", 4 → "'ü", 6 → "'sı", 10 → "'u", 40 → "'ı", 100 → "'ü", 1000 → "'i"
 * (States (c) "4 instance · 2'si hatalı"). Okunuş trLocative'le aynı
 * basamak mantığıyla: son basamak; o sıfırsa onlar, sonra yüz, sonra bin.
 */
export function trPossessive3(n: number): string {
  const digits = String(Math.abs(Math.trunc(n)));
  const last = digits[digits.length - 1];
  if (last !== '0' || digits.length === 1) return `'${UNIT_POSS[last]}`;
  const tens = digits[digits.length - 2];
  if (tens !== '0') return `'${TENS_POSS[tens]}`;
  if (digits.length >= 3 && digits[digits.length - 3] !== '0') return "'ü"; // yüz
  return "'i"; // bin
}

// ── apiUrl — Go NormalizeAPIURL aynası ─────────────────────────────────────

/** Go hasControl aynası: U+0000–U+001F ya da U+007F (satır sonu dahil). */
export function hasControl(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x20 || c === 0x7f) return true;
  }
  return false;
}

const URL_RE = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/([^/?#]*)([^?#]*)(\?[^#]*)?(#.*)?$/;

// v0.10.974 — Go url.EscapedPath aynası (NormalizeAPIURL yolu `u.EscapedPath()`
// ile yazar). Ham yol validEncoded ise AYNEN korunur (%41, [ ] ! ' ( ) * dahil);
// değilse yol çözülür ve shouldEscape(encodePath) ile yeniden kodlanır — o
// dalda mevcut %XX de çözülür (%41 → A, %2F → /) ve alt-ayraçlar kodlanır
// (! → %21). Beklenen değerler Go'nun gerçek çıktısından (argocdForm.test.ts).
const PATH_VALID_ENC = /^[A-Za-z0-9\-_.~$&+,/:;=@!'()*[\]%]*$/;
const PATH_KEEP_BYTE = /^[A-Za-z0-9\-_.~$&+,/:;=@]$/;
const UTF8 = new TextEncoder();

function goEscapedPath(p: string): string {
  if (PATH_VALID_ENC.test(p)) return p;
  const bytes: number[] = [];
  for (let i = 0; i < p.length;) {
    if (p[i] === '%') { bytes.push(parseInt(p.slice(i + 1, i + 3), 16)); i += 3; continue; }
    const ch = String.fromCodePoint(p.codePointAt(i) ?? 0);
    bytes.push(...UTF8.encode(ch));
    i += ch.length;
  }
  return bytes.map(b => (b < 0x80 && PATH_KEEP_BYTE.test(String.fromCharCode(b))
    ? String.fromCharCode(b)
    : `%${b.toString(16).toUpperCase().padStart(2, '0')}`)).join('');
}

/**
 * v0.10.974 — Argo CD API adresi (instance başına; asla türetilmez). Boş →
 * ayarlanmamış (hata yok). Kurallar Go ile aynı: http/https, host şart,
 * userinfo/sorgu/parça yasak, şema+host küçük harf, sondaki "/" atılır, yol
 * ve büyük/küçük harfi KORUNUR (yalnız Go EscapedPath kodlaması), varsayılan
 * port EKLENMEZ. Mesajlar mockup'tan.
 */
export function normalizeApiUrl(raw: string): { value: string; error: string } {
  const s = raw.trim();
  if (!s) return { value: '', error: '' };
  // v0.10.974 — Go yalnız ' ' ve '\t' reddeder (+ hasControl); U+00A0 gibi
  // Unicode boşluk yolda %C2%A0 olarak kabul edilir — istemci sunucudan SIKI olamaz.
  if (/[ \t]/.test(s)) return { value: '', error: 'Adres boşluk içeremez.' };
  if (hasControl(s)) return { value: '', error: 'Adres kontrol karakteri içeremez.' };
  const m = URL_RE.exec(s);
  if (!m) {
    const host = s.replace(/:\d+$/, '').replace(/\/+$/, '');
    return { value: '', error: `“${s}” bir http(s) adresi değil. Argo CD CLI'ın --server biçimi (host:port) burada geçmez; tarayıcıda açtığınız adresi girin, ör. https://${host}` };
  }
  const sc = m[1].toLowerCase();
  if (sc !== 'http' && sc !== 'https') return { value: '', error: `http:// ya da https:// ile başlamalı (girilen: ${sc}://).` };
  const authority = m[2];
  if (authority.includes('@')) return { value: '', error: 'Kullanıcı bilgisi (user:pass@) içeremez; token tokenRef ile verilir.' };
  // v0.10.974 — köşeli parantezsiz host'ta port SON ':'tan ayrılır (Go 1.25,
  // urlstrictcolons=0: "https://a:b:443" kabul; clusterArgoFields.parseHost ile aynı kural).
  const br = /^(\[[^\]]*\])(:.*)?$/.exec(authority);
  const lastColon = authority.lastIndexOf(':');
  const host = br ? br[1] : lastColon < 0 ? authority : authority.slice(0, lastColon);
  const port = br ? (br[2] ?? '') : lastColon < 0 ? '' : authority.slice(lastColon);
  if (!/^(:\d*)?$/.test(port)) return { value: '', error: 'URL ayrıştırılamadı: port yalnız rakam olabilir.' };
  if (!host || host === '[]') return { value: '', error: 'Host zorunlu.' };
  // Go url.Parse boş parçayı ("…/#") yok sayar; yalnız dolu parça ya da "?" reddedilir.
  if (m[4] !== undefined || (m[5] !== undefined && m[5] !== '#')) return { value: '', error: 'Sorgu (?) ya da parça (#) içeremez.' };
  if (/%(?![0-9A-Fa-f]{2})/.test(authority + m[3])) return { value: '', error: 'URL ayrıştırılamadı: geçersiz % kodlaması.' };
  return { value: `${sc}://${authority.toLowerCase()}${goEscapedPath(m[3]).replace(/\/+$/, '')}`, error: '' };
}

// ── tokenRef ───────────────────────────────────────────────────────────────

/** Go secretref.InvalidMessage (+ nokta). */
export const TOKEN_REF_INVALID = 'tokenRef `env:NAME` ya da `file:/path` biçiminde olmalı (düz token saklanmaz).';

/** secretref.Valid aynası: `env:NAME` (POSIX) | `file:/mutlak/yol` (RE2 \s = [\t\n\f\r ]). */
export function validTokenRef(ref: string): boolean {
  return /^env:[A-Za-z_][A-Za-z0-9_]*$/.test(ref) || /^file:\/[^\t\n\f\r ]+$/.test(ref);
}

/** Tablodaki Token hücresinin hâli. */
export type TokenState = 'none' | 'pending' | 'resolved' | 'unresolved' | 'stored';

/** Satırın Kaydet'te geçerli olacak referansı (yazılan > kayıtlı; kaldırılıyorsa ''). */
export function effectiveRef(i: InstanceDraft): string {
  const typed = i.tokenInput.trim();
  if (typed) return typed;
  return i.clearToken ? '' : i.storedRef;
}

export function tokenState(i: InstanceDraft, tokens: Record<string, ArgoCDTokenStatus>): TokenState {
  const ref = effectiveRef(i);
  if (!ref) return 'none';
  const typed = i.tokenInput.trim();
  if (i.origin === 'new' || (typed && typed !== i.storedRef)) return 'pending';
  const st = tokens[i.savedId];
  if (!st || st.tokenRef !== ref) return 'stored';
  return st.resolved ? 'resolved' : 'unresolved';
}

/** Token sıralaması: çözülemedi < kayıtlı < yok. */
export function tokenRank(s: TokenState): number {
  return s === 'unresolved' ? 0 : s === 'none' ? 2 : 1;
}

// ── Değişiklik sayacı ──────────────────────────────────────────────────────

function sameInstance(a: InstanceDraft, b: InstanceDraft): boolean {
  return a.id === b.id && a.hubClusterId === b.hubClusterId && a.name === b.name && a.hubNamespace === b.hubNamespace
    && a.metricsJob === b.metricsJob && a.apiUrl === b.apiUrl && a.insecureSkipVerify === b.insecureSkipVerify
    && a.enabled === b.enabled && a.appsAnyNamespace === b.appsAnyNamespace
    && !b.tokenInput.trim() && !b.clearToken;
}

/** Kayıtlı satır bu taslakta değişti mi (tokenRef yazımı / kaldırma dahil). */
export function instanceChanged(cur: InstanceDraft, base: Draft): boolean {
  if (cur.origin !== 'saved') return false;
  const b = base.instances.find(x => x.key === cur.key);
  return !b || !sameInstance(b, cur);
}

/** v0.10.974 — kaydedilmemiş değişikliklerin kısa cümleleri (mockup changes()). */
export function diffPhrases(d: Draft, base: Draft, clusters: RemoteCluster[]): string[] {
  const out: string[] = [];
  const hn = (id: string) => hubName(id, clusters);
  if (d.enabled !== base.enabled) out.push(d.enabled ? 'Argo CD açıldı' : 'Argo CD kapatıldı');
  // v0.10.983 — metrik işçisinin AYRI bayrağı (etkin değer: entegrasyon açık VE işçi açık).
  const mw = d.enabled && d.metricsWorker, bmw = base.enabled && base.metricsWorker;
  if (mw !== bmw) out.push(mw ? 'metrik işçisi açıldı' : 'metrik işçisi kapatıldı');
  for (const h of d.hubs) {
    const b = base.hubs.find(x => x.clusterId === h.clusterId);
    if (!b) out.push(`${hn(h.clusterId)} hub olarak eklendi`);
    else if (b.inject !== h.inject) out.push(`${hn(h.clusterId)}: küme etiketi ${h.inject ? 'eklenecek' : 'eklenmeyecek'}`);
  }
  for (const b of base.hubs) if (!d.hubs.some(x => x.clusterId === b.clusterId)) out.push(`${hn(b.clusterId)} hub listesinden çıkarıldı`);
  for (const i of d.instances) {
    if (i.origin === 'new') out.push(`${i.id} eklendi`);
    else if (instanceChanged(i, base)) out.push(`${i.id} değişti`);
  }
  for (const b of base.instances) if (!d.instances.some(x => x.key === b.key)) out.push(`${b.id} kaldırıldı`);
  for (const e of d.envList) if (!base.envList.includes(e)) out.push(`ortam ${e} eklendi`);
  for (const e of base.envList) if (!d.envList.includes(e)) out.push(`ortam ${e} çıkarıldı`);
  for (const k of ADV_NUM_KEYS) if (d.adv[k].trim() !== base.adv[k].trim()) out.push(`${k} değişti`);
  if (effMode(d.metricsOnlyMode) !== effMode(base.metricsOnlyMode)) out.push('classification.metricsOnlyMode değişti');
  return out;
}

/** Sayaç satırı: "3 kaydedilmemiş değişiklik: a, b, c …" | "Kayıtlı ayarlarla aynı." */
export function dirtyText(phrases: string[], bufferDirty: boolean): string {
  const head = phrases.length
    ? `${phrases.length} kaydedilmemiş değişiklik: ${phrases.slice(0, 3).join(', ')}${phrases.length > 3 ? ' …' : ''}`
    : 'Kayıtlı ayarlarla aynı.';
  return head + (bufferDirty ? ' Açık formda tabloya uygulanmamış değişiklik var.' : '');
}

// ── Gelişmiş alanlar ───────────────────────────────────────────────────────

export interface AdvField { key: AdvNumKey; label: string; decimal?: boolean }
export interface AdvGroup { title: string; fields: AdvField[]; mode?: boolean }

/** Mockup'ın beş grubu; `mode` grubu tokenRef-yokken seçimini de taşır. */
export const ADV_GROUPS: AdvGroup[] = [
  { title: 'API işçisi · instance başına', fields: [
    { key: 'apiWorker.rps', label: 'İstek / sn', decimal: true },
    { key: 'apiWorker.burst', label: 'Burst' },
    { key: 'apiWorker.maxConcurrent', label: 'Eşzamanlı istek' },
  ] },
  { title: 'Sınıflandırma', mode: true, fields: [
    { key: 'classification.windowMin', label: 'Eşleşme penceresi (± dk)' },
    { key: 'classification.outOfBandLookbackMin', label: 'out_of_band geriye bakış (dk)' },
  ] },
  { title: 'Hub okuyucu', fields: [
    { key: 'reader.maxSeries', label: 'En çok seri' },
    { key: 'reader.maxBodyMiB', label: 'En büyük gövde (MiB)' },
    { key: 'reader.timeoutS', label: 'Zaman aşımı (sn)' },
  ] },
  { title: 'Aralıklar', fields: [
    { key: 'intervals.metricsS', label: 'Metrik turu (sn)' },
    { key: 'intervals.inventoryMin', label: 'Tam envanter (dk)' },
    { key: 'intervals.mapperMin', label: 'Eşleyici (dk)' },
    { key: 'intervals.classifierReevalH', label: 'Yeniden sınıflandırma (sa)' },
  ] },
  { title: 'Eşleme güveni (1–100)', fields: [
    { key: 'mapping.nameConfidence', label: 'Ad eşleşmesi (tahmini)' },
    { key: 'mapping.namespaceConfidence', label: 'Namespace eşleşmesi (zayıf)' },
  ] },
];

export function isDecimalKey(k: AdvNumKey): boolean { return k === 'apiWorker.rps'; }

/** Etkin değer: boş/çöp → varsayılan (bounds.default). */
export function advEffective(adv: Draft['adv'], k: AdvNumKey, bounds: Record<string, ArgoCDBound>): number {
  const def = bounds[k]?.default ?? 0;
  const v = parseAdvInput(adv[k]);
  return v === null || !Number.isFinite(v) ? def : v;
}

/** Sunucunun Normalized'ı gibi: ≤0 → varsayılan, aralık dışı → sınır. */
function advClamped(adv: Draft['adv'], k: AdvNumKey, bounds: Record<string, ArgoCDBound>): number {
  const b = bounds[k];
  const v = advEffective(adv, k, bounds);
  if (!b) return v;
  return v <= 0 ? b.default : Math.min(b.max, Math.max(b.min, v));
}

/** v0.10.974 — tek gelişmiş alanın hatası ('' = geçerli); çapraz alanlar ETKİN değerlerle. */
export function advError(adv: Draft['adv'], k: AdvNumKey, bounds: Record<string, ArgoCDBound>): string {
  const b = bounds[k];
  if (!b) return '';
  const raw = adv[k].trim();
  const def = fmtTr(b.default);
  if (raw !== '') {
    const n = parseAdvInput(raw);
    const rng = `${fmtTr(b.min)}–${fmtTr(b.max)}`;
    if (n === null || !Number.isFinite(n) || (!isDecimalKey(k) && Math.round(n) !== n)) {
      return `${isDecimalKey(k) ? 'Sayı' : 'Tam sayı'} olmalı: ${rng} (boş = ${def}).`;
    }
    if (n < b.min || n > b.max) return `${rng} olmalı (boş = ${def}); girilen ${fmtTr(n)}.`;
  }
  const eff = advEffective(adv, k, bounds);
  if (k === 'classification.outOfBandLookbackMin') {
    const w = advClamped(adv, 'classification.windowMin', bounds);
    if (eff < w) {
      return raw === ''
        ? `Boş = ${def} dk; eşleşme penceresi ${w} dk olduğu için geçersiz — en az ${w} girin ya da pencereyi küçültün.`
        : `Eşleşme penceresinden (${w} dk) kısa olamaz — en az ${w} girin ya da pencereyi küçültün.`;
    }
  }
  if (k === 'mapping.namespaceConfidence') {
    const nc = advClamped(adv, 'mapping.nameConfidence', bounds);
    if (eff >= nc) {
      if (nc <= 1) return 'Ad eşleşmesi güveni 1 iken geçerli değer yok — ad eşleşmesi güvenini artırın.';
      return raw === ''
        ? `Boş = ${def}; ad eşleşmesi güveni ${nc} olduğu için geçersiz — en çok ${nc - 1} girin ya da ad eşleşmesi güvenini artırın.`
        : `Ad eşleşmesi güveninden (${nc}) küçük olmalı — en çok ${nc - 1} girin.`;
    }
  }
  return '';
}

/** Gelişmiş alanın DOM id'si (`reader.timeoutS` → `acd-adv-reader-timeoutS`). */
export function advInputId(k: string): string {
  return `acd-adv-${k.replace(/\./g, '-')}`;
}

/** Kapalı "Gelişmiş" başlığının özet satırı. */
export function advSummary(d: Draft, bounds: Record<string, ArgoCDBound>): string {
  const changed = ADV_NUM_KEYS.filter(k => d.adv[k].trim() !== '').length + (effMode(d.metricsOnlyMode) !== 'estimate' ? 1 : 0);
  const e = (k: AdvNumKey) => fmtTr(advEffective(d.adv, k, bounds));
  return `${changed ? `${changed} değer değişti · ` : 'Varsayılanlar · '}API ${e('apiWorker.rps')} istek/sn, burst ${e('apiWorker.burst')} · okuyucu ≤ ${e('reader.maxSeries')} seri, ${e('reader.maxBodyMiB')} MiB, ${e('reader.timeoutS')} sn · metrik turu ${e('intervals.metricsS')} sn`;
}

// ── Doğrulama ──────────────────────────────────────────────────────────────

export const ID_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
export const NS_RE = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/;
export const ENV_RE = /^[a-z0-9]{1,32}$/;
export const MAX_ENVS = 50;
const MAX_NAME = 128;
const MAX_JOB = 256;

/** Hatanın düştüğü yer (satır içi hata satırı / alan / kutu). */
export type IssueTarget =
  | { kind: 'hub'; key: string }
  | { kind: 'hubs' }
  | { kind: 'instance'; key: string; field: string }
  | { kind: 'env' }
  | { kind: 'adv'; key: string }
  // v0.10.974 — `classification.metricsOnlyMode` (seçim kutusu acd-adv-mode) ve
  // `pins[…]` (salt okunur not; düzenleyici yok, pinler API'den) — ikisinin de gidilecek yeri var.
  | { kind: 'advMode' }
  | { kind: 'pins' }
  | { kind: 'form' }
  // Gidilecek yeri olmayan yol (ör. `instances` tavanı, bayat `instances[N]`):
  // kutuda bağlantı DEĞİL düz metin.
  | { kind: 'other' };

/** Kaydet hatası: sunucu tarzı alan yolu + tam metin + kutudaki kısa özet. */
export interface Issue { path: string; message: string; short: string; target: IssueTarget }

export interface ValidateCtx {
  clusters: RemoteCluster[];
  bounds: Record<string, ArgoCDBound>;
  /** GET'teki (saklanan) instance'lar — BE3 aynası için yuvalar. */
  saved: InstanceDraft[];
}

/** Satır içi formun alan hataları (mockup validateBuf metinleri). */
export interface BufferErrors { id?: string; hub?: string; ns?: string; name?: string; job?: string; url?: string; ref?: string }

/** Formun düzenlediği satır (taslağa "Tabloya uygula" ile yazılır). */
export interface InstanceBuffer {
  key: string;
  /** Taslakta henüz yok ("Instance ekle"). */
  isNew: boolean;
  /** Taslakta var ama kaydedilmemiş (kimlik değiştirilebilir). */
  unsaved: boolean;
  origId: string;
  id: string;
  name: string;
  hubClusterId: string;
  hubNamespace: string;
  metricsJob: string;
  apiUrl: string;
  storedRef: string;
  tokenInput: string;
  clearToken: boolean;
  enabled: boolean;
  insecureSkipVerify: boolean;
  appsAnyNamespace: boolean;
  discovered: boolean;
  savedId: string;
  origin: Origin;
}

export function bufferFrom(i: InstanceDraft): InstanceBuffer {
  return {
    key: i.key, isNew: false, unsaved: i.origin === 'new', origId: i.id,
    id: i.id, name: i.name, hubClusterId: i.hubClusterId, hubNamespace: i.hubNamespace, metricsJob: i.metricsJob,
    apiUrl: i.apiUrl, storedRef: i.storedRef, tokenInput: i.tokenInput, clearToken: i.clearToken,
    enabled: i.enabled, insecureSkipVerify: i.insecureSkipVerify, appsAnyNamespace: i.appsAnyNamespace,
    discovered: i.discovered, savedId: i.savedId, origin: i.origin,
  };
}

/** "Instance ekle" — boş yeni satır (hub = ilk hub). */
export function blankBuffer(hubClusterId: string): InstanceBuffer {
  return {
    key: newKey('n:'), isNew: true, unsaved: true, origId: '',
    id: '', name: '', hubClusterId, hubNamespace: '', metricsJob: '', apiUrl: '',
    storedRef: '', tokenInput: '', clearToken: false, enabled: true, insecureSkipVerify: false,
    appsAnyNamespace: false, discovered: false, savedId: '', origin: 'new',
  };
}

/** Açık formda tabloya uygulanmamış değişiklik var mı (boş yeni satır sayılmaz). */
export function bufferDirty(b: InstanceBuffer | null, instances: InstanceDraft[]): boolean {
  if (!b) return false;
  if (b.isNew) return !!(b.id.trim() || b.name.trim() || b.hubNamespace.trim() || b.metricsJob.trim() || b.apiUrl.trim() || b.tokenInput.trim());
  const cur = instances.find(x => x.key === b.key);
  if (!cur) return true;
  const c = bufferFrom(cur);
  return c.id !== b.id || c.name !== b.name || c.hubClusterId !== b.hubClusterId || c.hubNamespace !== b.hubNamespace
    || c.metricsJob !== b.metricsJob || c.apiUrl !== b.apiUrl || c.tokenInput !== b.tokenInput || c.clearToken !== b.clearToken
    || c.enabled !== b.enabled || c.insecureSkipVerify !== b.insecureSkipVerify || c.appsAnyNamespace !== b.appsAnyNamespace;
}

/** v0.10.974 — satır içi formun doğrulaması (mockup validateBuf). */
export function validateBuffer(b: InstanceBuffer, instances: InstanceDraft[], hubs: HubDraft[], clusters: RemoteCluster[]): BufferErrors {
  const err: BufferErrors = {};
  const others = instances.filter(x => x.key !== b.key);
  const id = b.id.trim();
  const ns = b.hubNamespace.trim();
  const hn = hubName(b.hubClusterId, clusters);
  if (!ID_RE.test(id)) {
    err.id = id ? `“${id}” geçersiz: küçük harf, rakam ve tire; en çok 63 karakter (DNS etiketi).` : 'Kimlik zorunlu: küçük harf, rakam ve tire.';
  } else {
    const dup = others.find(x => x.id === id);
    if (dup) {
      const dn = hubName(dup.hubClusterId, clusters);
      err.id = `“${id}” ${dn}${trLocative(dn)}ki bir instance'ta zaten var. Kimlik hub'lar arasında da tekil olmalı; ör. ${id}-${hn}.`;
    }
  }
  if (!hubs.some(h => h.clusterId === b.hubClusterId)) err.hub = `Hub “${hn}” listede değil — listeden bir hub seçin.`;
  if (!NS_RE.test(ns)) {
    err.ns = ns ? `“${ns}” geçerli bir Kubernetes namespace adı değil.` : "Namespace zorunlu: Argo CD'nin çalıştığı namespace.";
  } else {
    const dupNs = others.find(x => x.hubClusterId === b.hubClusterId && x.hubNamespace === ns);
    if (dupNs) err.ns = `“${ns}” bu hub'da zaten “${dupNs.id}” instance'ına ait (bir hub'da namespace başına tek Argo CD).`;
  }
  const name = b.name.trim();
  if (name.length > MAX_NAME || hasControl(name)) err.name = `Görünen ad en çok ${MAX_NAME} karakter; kontrol karakteri içeremez.`;
  const job = b.metricsJob.trim();
  if (job.length > MAX_JOB || hasControl(job)) err.job = `metricsJob en çok ${MAX_JOB} karakter; kontrol karakteri içeremez.`;
  const url = normalizeApiUrl(b.apiUrl);
  if (url.error) err.url = url.error;
  const typed = b.tokenInput.trim();
  if (typed && !validTokenRef(typed)) err.ref = TOKEN_REF_INVALID;
  return err;
}

/** Form alanı değişince o alanın (uygula anındaki) hatası düşer; kalanlar durur. */
export function pruneBufferErrors(prev: BufferErrors, a: InstanceBuffer, b: InstanceBuffer): BufferErrors {
  const out: BufferErrors = { ...prev };
  if (a.id !== b.id) delete out.id;
  if (a.hubClusterId !== b.hubClusterId) { delete out.hub; delete out.ns; }
  if (a.hubNamespace !== b.hubNamespace) delete out.ns;
  if (a.name !== b.name) delete out.name;
  if (a.metricsJob !== b.metricsJob) delete out.job;
  if (a.apiUrl !== b.apiUrl) delete out.url;
  if (a.tokenInput !== b.tokenInput || a.clearToken !== b.clearToken) delete out.ref;
  return out;
}

/** "Tabloya uygula": form → taslak satırı (URL normalleşmiş, alanlar kırpılmış). */
export function applyBuffer(b: InstanceBuffer): InstanceDraft {
  const typed = b.tokenInput.trim();
  return {
    key: b.key, origin: b.origin, savedId: b.savedId,
    id: b.id.trim(), hubClusterId: b.hubClusterId, name: b.name.trim(), hubNamespace: b.hubNamespace.trim(),
    metricsJob: b.metricsJob.trim(), apiUrl: normalizeApiUrl(b.apiUrl).value,
    storedRef: b.storedRef, tokenInput: typed, clearToken: typed ? false : b.clearToken,
    insecureSkipVerify: b.insecureSkipVerify, enabled: b.enabled, discovered: b.discovered,
    appsAnyNamespace: b.appsAnyNamespace,
  };
}

/** v0.10.974 — Kaydet öncesi istemci denetimi: TÜM kurallar, her sorun ayrı (sunucu ilk hatayı döndürür). */
export function validateDraft(d: Draft, ctx: ValidateCtx): Issue[] {
  const out: Issue[] = [];
  const hn = (id: string) => hubName(id, ctx.clusters);
  if (d.enabled && d.hubs.length === 0) {
    out.push({ path: 'hubs', short: 'Argo CD açıkken hub yok', target: { kind: 'hubs' },
      message: "Argo CD açıkken en az bir etkin hub gerekli — Hub'lar bölümünden bir Remote Cluster seçin ya da entegrasyonu kapatın." });
  }
  d.hubs.forEach((h, ix) => {
    const rc = ctx.clusters.find(c => c.id === h.clusterId);
    const path = `hubs[${ix}].clusterId`;
    if (!rc) {
      out.push({ path, short: 'Remote Cluster kaydı yok', target: { kind: 'hub', key: h.key },
        message: `“${h.clusterId}” artık bir Remote Cluster kaydı değil (silinmiş). Bu hub satırını kaldırın; Argo bu kümede çalışıyorsa kaydı Ayarlar › Remote clusters'ta yeniden ekleyip hub olarak seçin.` });
    }
    // v0.10.1009 — devre dışı kayıt artık sorun DEĞİL: pasif hub (aktif/pasif
    // çift). Kaydı engellemez, işçiler onu taramaz; satır bunu bilgi olarak yazar.
  });
  // Tek şart: açıkken en az bir hub ETKİN olmalı (sunucu canonicalHubs ile aynı).
  if (d.enabled && d.hubs.length > 0 && !d.hubs.some(h => ctx.clusters.find(c => c.id === h.clusterId)?.enabled)) {
    out.push({ path: 'hubs', short: 'etkin hub yok', target: { kind: 'hubs' },
      message: "Argo CD açıkken en az bir hub'ın Remote Cluster kaydı etkin olmalı — listedeki hub'ların hepsi devre dışı (pasif). Bir kaydı Ayarlar › Remote clusters'ta etkinleştirin ya da entegrasyonu kapatın." });
  }
  if (d.envList.length > MAX_ENVS) {
    out.push({ path: 'envList', short: `en çok ${MAX_ENVS} ortam`, target: { kind: 'env' }, message: `En çok ${MAX_ENVS} ortam.` });
  }
  d.envList.forEach((e, ix) => {
    if (!ENV_RE.test(e)) {
      out.push({ path: `envList[${ix}]`, short: 'geçersiz ortam jetonu', target: { kind: 'env' },
        message: `“${e}” geçersiz: tek jeton olmalı — küçük harf ve rakam, en çok 32; tire ad ayrıştırmayı bozar.` });
    }
  });

  const hubSet = new Set(d.hubs.map(h => h.clusterId));
  const ids = new Map<string, number>();
  const slots = new Map<string, number>();
  const draftIds = new Set(d.instances.map(i => i.id.trim()));
  const savedIds = new Set(ctx.saved.map(s => s.id));
  const savedSlot = new Map<string, string>();
  for (const s of ctx.saved) savedSlot.set(`${s.hubClusterId}\u0000${s.hubNamespace}`, s.id);
  d.instances.forEach((i, ix) => {
    const p = (f: string) => `instances[${ix}].${f}`;
    const t = (field: string): IssueTarget => ({ kind: 'instance', key: i.key, field });
    const id = i.id.trim();
    const ns = i.hubNamespace.trim();
    if (!ID_RE.test(id)) {
      out.push({ path: p('id'), short: 'geçersiz kimlik', target: t('id'),
        message: id ? `“${id}” geçersiz: küçük harf, rakam ve tire; en çok 63 karakter (DNS etiketi).` : 'Kimlik zorunlu: küçük harf, rakam ve tire.' });
    } else if (ids.has(id)) {
      const first = d.instances[ids.get(id)!];
      const fn = hn(first.hubClusterId);
      out.push({ path: p('id'), short: "hub'lar arasında tekrar ediyor", target: t('id'),
        message: `“${id}” ${fn}${trLocative(fn)}ki instance'ta da var. Kimlik hub'lar arasında tekil olmalı (ClickHouse instance_id: durum ve eşleme satırları bu kimliğe bağlı). Bu satıra ayrı bir kimlik verin, ör. ${id}-${hn(i.hubClusterId)}. Aynı namespace iki hub'da serbesttir.` });
    } else {
      ids.set(id, ix);
    }
    if (!hubSet.has(i.hubClusterId)) {
      out.push({ path: p('hubClusterId'), short: 'hub listede değil', target: t('hubClusterId'),
        message: `${id || 'instance'} için hub “${hn(i.hubClusterId)}” artık listede değil — instance'ı başka hub'a taşıyın ya da hub'ı geri ekleyin.` });
    }
    const name = i.name.trim();
    if (name.length > MAX_NAME || hasControl(name)) {
      out.push({ path: p('name'), short: 'ad çok uzun ya da kontrol karakteri', target: t('name'), message: `Görünen ad en çok ${MAX_NAME} karakter; kontrol karakteri içeremez.` });
    }
    if (!NS_RE.test(ns)) {
      out.push({ path: p('hubNamespace'), short: 'geçersiz namespace', target: t('hubNamespace'),
        message: ns ? `“${ns}” geçerli bir Kubernetes namespace adı değil.` : "Namespace zorunlu: Argo CD'nin çalıştığı namespace." });
    } else {
      const slot = `${i.hubClusterId}\u0000${ns}`;
      const prev = slots.get(slot);
      if (prev !== undefined) {
        out.push({ path: p('hubNamespace'), short: "bu hub'da namespace tekrar ediyor", target: t('hubNamespace'),
          message: `“${ns}” bu hub'da zaten “${d.instances[prev].id}” instance'ına ait (bir hub'da namespace başına tek Argo CD).` });
      } else {
        slots.set(slot, ix);
      }
      // BE3 aynası: yeni kimlik, kayıtlı kimliği bu taslakta kaldırılmış bir yuvayı alıyor.
      const owner = savedSlot.get(slot);
      if (id && owner && !savedIds.has(id) && !draftIds.has(owner)) {
        out.push({ path: p('id'), short: 'kayıtlı kimlik değiştirilemez', target: t('id'),
          message: `“${id}” kayıtlı “${owner}” instance'ının yerini alıyor (${hn(i.hubClusterId)}/${ns}): kayıtlı kimlik değiştirilemez (ClickHouse instance_id: durum ve eşleme satırları bu kimliğe bağlı). Önce “${owner}” satırını kaldırıp kaydedin, sonra “${id}” ile ekleyin.` });
      }
    }
    const job = i.metricsJob.trim();
    if (job.length > MAX_JOB || hasControl(job)) {
      out.push({ path: p('metricsJob'), short: 'metricsJob çok uzun ya da kontrol karakteri', target: t('metricsJob'), message: `metricsJob en çok ${MAX_JOB} karakter; kontrol karakteri içeremez.` });
    }
    const url = normalizeApiUrl(i.apiUrl);
    if (url.error) {
      out.push({ path: p('apiUrl'), short: /bir http\(s\) adresi değil/.test(url.error) ? 'http(s) adresi değil' : 'geçersiz API URL', target: t('apiUrl'), message: url.error });
    }
    const typed = i.tokenInput.trim();
    if (typed && !validTokenRef(typed)) {
      out.push({ path: p('tokenRef'), short: 'geçersiz tokenRef biçimi', target: t('tokenRef'), message: TOKEN_REF_INVALID });
    }
  });

  for (const k of ADV_NUM_KEYS) {
    const e = advError(d.adv, k, ctx.bounds);
    if (e) out.push({ path: k, short: e, target: { kind: 'adv', key: k }, message: e });
  }
  return out;
}

// ── Sunucu hatası ──────────────────────────────────────────────────────────

/** `updatedAt` — v0.10.978 — 409 stale gövdesindeki KAYITLI damga (ns; sayı değilse yok). */
export interface ArgoHttpError { status: number; error: string; field?: string; errorType?: string; updatedAt?: number }

/**
 * v0.10.974 — api.request'in `Error("HTTP <kod>: <gövde>")`ı → alanlar. Gövde
 * `{error, field}` (PUT 400), `{error, errorType}` (keşif) ya da
 * `{error, errorType: 'stale', updatedAt}` (PUT 409, v0.10.978) olabilir;
 * `error` "<field>: " önekiyle gelir — önek atılır (son söz sunucunun, ama
 * yol zaten ayrı gösteriliyor). JSON olmayan gövde metin olarak kalır; HTTP
 * olmayan hata (zaman aşımı, ağ) status 0.
 */
export function parseArgoHttpError(err: unknown): ArgoHttpError {
  const msg = err instanceof Error ? err.message : String(err);
  const m = /^HTTP (\d+):\s*([\s\S]*)$/.exec(msg);
  if (!m) return { status: 0, error: msg };
  const status = Number(m[1]);
  const body = m[2].trim();
  let error = body;
  let field: string | undefined;
  let errorType: string | undefined;
  let updatedAt: number | undefined;
  try {
    const j: unknown = JSON.parse(body);
    if (j && typeof j === 'object') {
      const r = j as Record<string, unknown>;
      if (typeof r.error === 'string') error = r.error;
      if (typeof r.field === 'string' && r.field) field = r.field;
      if (typeof r.errorType === 'string' && r.errorType) errorType = r.errorType;
      if (typeof r.updatedAt === 'number' && Number.isFinite(r.updatedAt)) updatedAt = r.updatedAt;
    }
  } catch { /* düz metin gövde */ }
  if (field && error.startsWith(`${field}: `)) error = error.slice(field.length + 2);
  return { status, error, ...(field ? { field } : {}), ...(errorType ? { errorType } : {}), ...(updatedAt !== undefined ? { updatedAt } : {}) };
}

// ── v0.10.978 — 409 sonrası üç yönlü birleştirme ──────────────────────────
//
// base = kullanıcının yüklediği taslak, draft = düzenlediği, fresh = sunucunun
// şimdiki. Alan ya da satır başına: kullanıcı değiştirdi (base≠draft) VE sunucu
// değiştirmedi (base=fresh) → kullanıcının değeri KORUNUR (kept); kullanıcı
// değiştirdi ama sunucu da değiştirdi → sunucunun değeri, düzenleme ATILIR
// (dropped); kullanıcı dokunmadı → sunucunun. Satırlar hub'da clusterId,
// instance'ta id ile eşlenir (anahtarlar `s:<id>` iki tarafta aynı); sıra
// sunucununki, kullanıcının yeni satırları sonda. Sunucunun sildiği satırdaki
// düzenleme ve sunucunun da eklediği id'li yeni satır atılır (sunucu kazanır).

export interface MergeResult { draft: Draft; kept: number; dropped: number }
type Tally = { kept: number; dropped: number };

/** Anahtar sırasından bağımsız yapısal eşitlik (applyBuffer satırı farklı sırada kurar). */
function same(a: unknown, b: unknown): boolean {
  const canon = (v: unknown): unknown => (v && typeof v === 'object' && !Array.isArray(v)
    ? Object.fromEntries(Object.entries(v as Record<string, unknown>).sort(([x], [y]) => (x < y ? -1 : x > y ? 1 : 0)).map(([k, x]) => [k, canon(x)]))
    : Array.isArray(v) ? v.map(canon) : v);
  return JSON.stringify(canon(a)) === JSON.stringify(canon(b));
}

function pick<T>(b: T, d: T, f: T, t: Tally): T {
  if (same(b, d)) return f;
  if (same(b, f)) { t.kept += 1; return d; }
  t.dropped += 1;
  return f;
}

function mergeRows<T>(base: T[], draft: T[], fresh: T[], idOf: (r: T) => string, t: Tally): T[] {
  const by = (rows: T[]) => new Map(rows.map(r => [idOf(r), r]));
  const b = by(base), d = by(draft);
  const out: T[] = [];
  const freshIds = new Set<string>();
  for (const fr of fresh) {
    const id = idOf(fr);
    freshIds.add(id);
    const br = b.get(id), dr = d.get(id);
    if (!br) { if (dr) t.dropped += 1; out.push(fr); continue; }           // sunucu ekledi (aynı id'li yeni satır düşer)
    if (!dr) { if (same(br, fr)) { t.kept += 1; continue; } t.dropped += 1; out.push(fr); continue; } // kullanıcı sildi
    out.push(pick(br, dr, fr, t));
  }
  for (const dr of draft) {
    const id = idOf(dr);
    if (freshIds.has(id)) continue;
    const br = b.get(id);
    if (!br) { t.kept += 1; out.push(dr); }                                   // kullanıcı ekledi
    else if (!same(br, dr)) t.dropped += 1;                                   // sunucu sildi, kullanıcı düzenlemişti
  }
  return out;
}

export function mergeDraft(base: Draft, draft: Draft, fresh: Draft): MergeResult {
  const t: Tally = { kept: 0, dropped: 0 };
  const adv = { ...fresh.adv };
  for (const k of ADV_NUM_KEYS) adv[k] = pick(base.adv[k], draft.adv[k], fresh.adv[k], t);
  return {
    draft: {
      enabled: pick(base.enabled, draft.enabled, fresh.enabled, t),
      metricsWorker: pick(base.metricsWorker, draft.metricsWorker, fresh.metricsWorker, t),
      hubs: mergeRows(base.hubs, draft.hubs, fresh.hubs, h => h.clusterId, t),
      instances: mergeRows(base.instances, draft.instances, fresh.instances, i => i.id, t),
      envList: pick(base.envList, draft.envList, fresh.envList, t),
      adv,
      metricsOnlyMode: pick(base.metricsOnlyMode, draft.metricsOnlyMode, fresh.metricsOnlyMode, t),
    },
    ...t,
  };
}

/** Yeniden yükleme kutusunun metni (kaç düzenleme korundu / atıldı).
 *  v0.10.978 — hub/instance satırı BÜTÜN olarak birleşir (applyBuffer satırı
 *  bütün kurar; token üçlüsü tokenInput/clearToken/storedRef tek mantıksal
 *  alan) — metin alan YA DA satır der; alan başına birleştirme sunucunun token
 *  değişikliğini kullanıcının yeni ref'iyle sessizce ezerdi. */
export function reloadText(m: MergeResult): string {
  if (!m.kept && !m.dropped) return 'Yeniden yüklendi — kayıtlı ayar güncel; kaydedilmemiş düzenleme yoktu.';
  const parts = [
    m.kept ? `${m.kept} düzenlemeniz korundu` : '',
    m.dropped ? `${m.dropped} düzenlemeniz sunucuda da değişen alana ya da satıra dokunduğu için atıldı` : '',
  ].filter(Boolean);
  return `Yeniden yüklendi — ${parts.join('; ')}.${m.kept ? ' Korunanları kaydetmek için Kaydet.' : ''}`;
}

/** Sunucu alan yolu → taslak hedefi (`instances[N]`/`hubs[N]` TASLAK dizisine). */
export function targetForPath(path: string, d: Draft): IssueTarget {
  const inst = /^instances\[(\d+)\](?:\.(\w+))?/.exec(path);
  if (inst) {
    const row = d.instances[Number(inst[1])];
    return row ? { kind: 'instance', key: row.key, field: inst[2] ?? '' } : { kind: 'other' };
  }
  const hub = /^hubs\[(\d+)\]/.exec(path);
  if (hub) {
    const row = d.hubs[Number(hub[1])];
    return row ? { kind: 'hub', key: row.key } : { kind: 'hubs' };
  }
  if (path === 'hubs') return { kind: 'hubs' };
  if (/^envList/.test(path)) return { kind: 'env' };
  if ((ADV_NUM_KEYS as readonly string[]).includes(path)) return { kind: 'adv', key: path };
  if (path === 'classification.metricsOnlyMode') return { kind: 'advMode' };
  if (/^pins(\[|$)/.test(path)) return { kind: 'pins' };
  return { kind: 'other' };
}

/** 400 gövdesi → tek Issue (kısa özet = sunucu metni). */
export function issueFromServer(p: ArgoHttpError, d: Draft): Issue {
  const path = p.field ?? '';
  return { path, message: p.error, short: p.error, target: path ? targetForPath(path, d) : { kind: 'other' } };
}

/** Satır hatalarının form alanına eşlenmesi (form açılınca alanın altında). */
export function bufferErrorsFromIssues(issues: Issue[], key: string): BufferErrors {
  const out: BufferErrors = {};
  for (const is of issues) {
    if (is.target.kind !== 'instance' || is.target.key !== key) continue;
    switch (is.target.field) {
      case 'id': out.id ??= is.message; break;
      case 'hubClusterId': out.hub ??= is.message; break;
      case 'hubNamespace': out.ns ??= is.message; break;
      case 'name': out.name ??= is.message; break;
      case 'metricsJob': out.job ??= is.message; break;
      case 'apiUrl': out.url ??= is.message; break;
      case 'tokenRef': case 'clearTokenRef': out.ref ??= is.message; break;
    }
  }
  return out;
}

/** Satır anahtarı → DOM id parçası (`s:team-a` → `s_team-a`). */
export function domKey(key: string): string {
  return key.replace(/[^A-Za-z0-9_-]/g, '_');
}

const TR_MONTHS = ['Oca', 'Şub', 'Mar', 'Nis', 'May', 'Haz', 'Tem', 'Ağu', 'Eyl', 'Eki', 'Kas', 'Ara'];

/** "20:41" — 24 saat, tarayıcı yerelinden bağımsız (lib/utils fmtClock ailesi). */
export function fmtHourMinute(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** "son kayıt 26 Eyl 20:41" — updatedAt nanosaniye. */
export function savedAtText(updatedAtNs: number | undefined): string {
  if (!updatedAtNs) return '';
  const d = new Date(Math.floor(updatedAtNs / 1e6));
  return `son kayıt ${d.getDate()} ${TR_MONTHS[d.getMonth()]} ${fmtHourMinute(d)}`;
}
