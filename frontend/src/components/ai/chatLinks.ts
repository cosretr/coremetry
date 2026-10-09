// chatLinks — v0.10.1137: sohbet cevabındaki bağlantıların GÜVENLİK kararı (SAF).
//
// Operatör (prod): wiki cevabındaki Jenkins adresi düz metin çiziliyordu;
// balon v0.9.1148'den beri bilinçli olarak hiç link basmıyordu. Link açmanın
// riski prompt-injection: wiki sayfasına gömülü bir talimat modele "şu adrese
// ?q=<gizli> ekle" dedirtebilir ve tıklanır bir link veriyi sızdırır.
//
// Kural (her biri ayrı test, chatLinks.test.ts):
//   relative  — kök-göreli uygulama yolu ("/service?…") ya da aynı köken:
//               tıklanır, SPA içinde gezilir.
//   allowed   — http(s) VE (a) normalize edilmiş adresi sunucunun cevapla
//               yolladığı `allowedLinks` listesinde (modele verilen kaynak
//               bağlamında gerçekten geçen URL'ler + çip/kaynak href'leri), ya
//               da (c) host'u bu cevabın KAYNAK/ÇİP host'larından biri. Host
//               kümesi bilinçli olarak yalnız sunucunun ürettiği kaynak ref'i ve
//               çip href'lerinden kurulur — bağlam metnindeki her URL'nin
//               host'u değil; aksi hâlde enjekte edilmiş bir sayfanın andığı
//               saldırgan host'una modelin uydurduğu sorgu dizesi geçerdi.
//   unverified— http(s) ama yukarıdakilerin hiçbiri: düz metin, TAM adres
//               görünür, "doğrulanmamış bağlantı" ipucu.
//   blocked   — http(s) ve kök-göreli dışındaki her şema (javascript:, data:,
//               vbscript:, file:, "//host"…): asla link değil.
// Parça (#…) karşılaştırmada yok sayılır: tarayıcı onu sunucuya göndermez.

import type { ChatAnswerLink, RagSource } from '@/lib/types';

// 'allowed-host' — v0.10.1137 inceleme: adres TAM eşleşmedi, yalnız kaynağın
// host'u (+ çok kiracılı host'ta ilk yol parçası) tuttu. Tıklanır ama etiket
// adresi gizlemez: çizim tam adresi / host ↗ işaretini gösterir.
export type LinkClass = 'relative' | 'allowed' | 'allowed-host' | 'unverified' | 'blocked';

export interface LinkPolicy {
  exact: ReadonlySet<string>;
  /** host → kaynak/çip adreslerinin ilk yol parçaları (çok kiracılı daraltma için). */
  hosts: ReadonlyMap<string, ReadonlySet<string>>;
  origin: string;
}

// Çok kiracılı bilinen host'lar: host tek başına kurum sınırı değil (her
// kiracı/örgüt ilk yol parçasında) — host eşleşmesi ilk parçayla daraltılır.
const MULTI_TENANT_HOSTS = new Set([
  'dev.azure.com', 'github.com', 'gitlab.com', 'bitbucket.org', 'gist.github.com', 'raw.githubusercontent.com',
]);

/** Sondaki cümle noktalaması URL'nin parçası değil; dengesiz kapanış parantezi de. */
export function trimUrlPunct(u: string): string {
  let s = u.replace(/[.,;:!?*_~'"]+$/, '');
  // "(bkz. https://x.example.test/a)" — kapanış parantezi dengesizse at.
  while (/[)\]]$/.test(s)) {
    const close = s.endsWith(')') ? ')' : ']';
    const open = close === ')' ? '(' : '[';
    if (s.split(close).length > s.split(open).length) s = s.slice(0, -1).replace(/[.,;:!?*_~'"]+$/, '');
    else break;
  }
  return s;
}

/** http(s) URL'nin karşılaştırma anahtarı (şema+host küçük harf, parça yok, kök "/" yok); değilse null. */
export function normalizeUrl(raw: string): string | null {
  const s = trimUrlPunct(raw.trim());
  const m = /^(https?):\/\/([^/?#\s\\]+)([^#\s]*)/i.exec(s);
  if (!m) return null;
  let tail = m[3];
  if (tail === '/') tail = '';
  return `${m[1].toLowerCase()}://${m[2].toLowerCase()}${tail}`;
}

export function hostOf(raw: string): string {
  const m = /^https?:\/\/([^/?#\s\\]+)/i.exec(raw.trim());
  return m ? m[1].toLowerCase() : '';
}

/** İlk yol parçası (küçük harf), yoksa ''. */
function firstSegment(raw: string): string {
  const m = /^https?:\/\/[^/?#\s\\]+\/([^/?#]*)/i.exec(raw.trim());
  return m ? m[1].toLowerCase() : '';
}

// Kontrol karakteri ya da boşluk (yol içinde asla).
// eslint-disable-next-line no-control-regex -- yol güvenliği: C0/C1 kontrol karakterlerini reddetmek kuralın amacı
const CTRL_OR_SPACE = /[\s\u0000-\u001f\u007f-\u009f]/;

/**
 * Kök-göreli GÜVENLİ uygulama yolu ("/x"). Reddedilenler: "//host" ve "/\host"
 * (tarayıcı protokol-göreli dış adres sayar), yolda ters bölü, boşluk/kontrol
 * karakteri, ve kod çözülünce "//" ya da "/\" ile başlayan ya da ters bölü
 * içeren yol (%2F%2F, %5C — sunucu/yönlendirici çözünce dışarı açılabilir).
 */
export function isRelativePath(u: string): boolean {
  if (!u.startsWith('/') || u.startsWith('//') || u.startsWith('/\\')) return false;
  if (u.includes('\\') || CTRL_OR_SPACE.test(u)) return false;
  const path = u.split(/[?#]/)[0];
  let dec = path;
  try { dec = decodeURIComponent(path); } catch { return false; }
  if (dec.startsWith('//') || dec.startsWith('/\\') || dec.includes('\\') || CTRL_OR_SPACE.test(dec)) return false;
  return true;
}

export function buildLinkPolicy(opts: {
  allowedLinks?: readonly string[];
  sources?: readonly RagSource[];
  links?: readonly ChatAnswerLink[];
  origin?: string;
}): LinkPolicy {
  const exact = new Set<string>();
  const hosts = new Map<string, Set<string>>();
  for (const u of opts.allowedLinks ?? []) {
    const n = normalizeUrl(u);
    if (n) exact.add(n);
  }
  const trusted = [...(opts.sources ?? []).map(s => s.ref ?? ''), ...(opts.links ?? []).map(l => l.href)];
  for (const u of trusted) {
    const n = normalizeUrl(u);
    if (!n) continue;
    exact.add(n);
    const h = hostOf(u);
    if (!h) continue;
    if (!hosts.has(h)) hosts.set(h, new Set());
    hosts.get(h)!.add(firstSegment(u));
  }
  return { exact, hosts, origin: (opts.origin ?? '').toLowerCase() };
}

export const EMPTY_POLICY: LinkPolicy = { exact: new Set(), hosts: new Map(), origin: '' };

/** Host eşleşmesi: çok kiracılı host'ta (ya da kaynakların ilk yol parçaları farklıysa) ilk parça da tutmalı. */
function hostAllowed(u: string, p: LinkPolicy): boolean {
  const h = hostOf(u);
  const segs = p.hosts.get(h);
  if (!segs) return false;
  if (MULTI_TENANT_HOSTS.has(h) || segs.size > 1) return segs.has(firstSegment(u));
  return true;
}

/**
 * Aynı köken mi: WHATWG URL ile çözülür (büyük harfli host, varsayılan port,
 * "/\" → "//" dönüşümü tarayıcıyla AYNI), köken karşılaştırılır ve SPA yolu
 * (pathname+search+hash) güvenli göreli yol testinden geçmek zorunda. Değilse null.
 */
function sameOriginPath(raw: string, origin: string): string | null | undefined {
  if (!origin) return undefined;
  let url: URL;
  try { url = new URL(raw, origin); } catch { return undefined; }
  let o: string;
  try { o = new URL(origin).origin.toLowerCase(); } catch { return undefined; }
  if (url.origin.toLowerCase() !== o) return undefined;
  const path = url.pathname + url.search + url.hash;
  return isRelativePath(path) ? path : null;
}

export function classifyLink(raw: string, p: LinkPolicy): LinkClass {
  const u = trimUrlPunct(raw.trim());
  if (u.startsWith('/')) {
    if (!isRelativePath(u)) return 'blocked';
    // Göreli girdi de köken üzerinden çözülür: çözüm başka kökene çıkarsa (olmamalı) engel.
    if (p.origin && sameOriginPath(u, p.origin) === undefined) return 'blocked';
    return 'relative';
  }
  const n = normalizeUrl(u);
  if (!n || /^https?:\/\/[^/?#]*\\/i.test(u)) return 'blocked';
  const so = sameOriginPath(u, p.origin);
  if (so === null) return 'unverified'; // aynı köken ama yol dışarı açılabilir ("//evil", "/\evil")
  if (typeof so === 'string') return 'relative';
  if (p.exact.has(n)) return 'allowed';
  if (hostAllowed(u, p)) return 'allowed-host';
  return 'unverified';
}

/** Göreli / aynı-köken adresi GÜVENLİ SPA yoluna çevirir; güvenli değilse null. */
export function toAppPath(raw: string, origin: string): string | null {
  const u = trimUrlPunct(raw.trim());
  if (u.startsWith('/')) return isRelativePath(u) ? u : null;
  const so = sameOriginPath(u, origin);
  return typeof so === 'string' ? so : null;
}

export const UNVERIFIED_TITLE = 'doğrulanmamış bağlantı';
