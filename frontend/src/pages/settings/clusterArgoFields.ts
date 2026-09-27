// clusterArgoFields.ts — v0.10.974 — Remote Cluster kaydının "Argo CD eşlemesi"
// bölümü (onaylı mockup ClusterFields.dc.html, operatör "Onay" 2026-09-27;
// docs/rollouts/v2-audit.md §3.2). Saf; tablo-testli (clusterArgoFields.test.ts).
//
// v0.10.974 — Neden istemci kopyası: operatör "Tümünü kaydet"e basmadan
// kaydedilecek biçimi görmeli (önizleme) ve sunucunun reddedeceği bir listeyi
// göndermeden düzeltebilmeli (Kaydet öncesi denetim). Sunucu TEK karar verici
// kalır: kaydedilen değer thanos.ReconcileClusterSettings'in ürettiğidir; bu
// dosya yalnız onu önceden gösterir.
//
// v0.10.974 — Neden `new URL()` DEĞİL: WHATWG ayrıştırıcısı https'te :443'ü
// atar, yolu ve host'u yeniden kodlar, "https:host" gibi opak biçimi host'lu
// sayar — önizleme sunucunun kaydedeceğinden farklı bir değer gösterirdi.
// Aşağıdaki ayrıştırıcı Go `net/url.Parse`'ın ilgili dallarının elle
// kopyasıdır (getScheme, ?/# kesimi, opak yol, authority, parseHost,
// validOptionalPort, host kaçış kuralları, Hostname/Port).
//
// v0.10.974 — İstemci sunucudan SIKI olamaz: sunucunun kabul ettiği bir
// adresi reddetmek operatörü kaydedemez hâle getirirdi. Bu yüzden Go 1.25
// davranışı (go.mod `go 1.25.0` → GODEBUG urlstrictcolons=0: http(s) host'unda
// birden çok ':' SON iki noktadan bölünür) aynen taklit edilir; şüpheli
// köşelerde (IPv6 bölge kimliği) kopya gevşek kalır, son söz sunucunun.
import type { ArgoCDSettingsResponse } from '@/lib/types';

// ── Metin (ClusterFields.dc.html, birebir) ─────────────────────────────────
export const IN_CLUSTER_API_SERVER_URL = 'https://kubernetes.default.svc';
export const API_URLS_NOTE = "https://kubernetes.default.svc yazılmaz — Argo'nun küme-içi hedefi instance'ın hub'ına çözülür; hub için dış API adresini girin.";
export const API_URLS_PREVIEW_TITLE = 'Kaydedilecek biçim · tüm kayıtlarda tekil';
export const SUFFIX_HINT = 'Argo uygulama adının son jetonu (…-env-ek). Tekil, büyük/küçük harf duyarsız.';
export const PAIR_HINT = 'Serbest metin · aynı değeri taşıyan kümeler aktif-aktif çifttir.';
/** Özet maddesinin alan adı (mockup Türkçe; formdaki İngilizce etiketler değişmez, §4.9). */
export const FIELD_LABEL = { api: "API server URL'leri", suffix: 'Argo app eki' } as const;

export function clientCheckSummary(n: number): string {
  return `Kaydedilmedi — istemci denetimi ${n} sorun buldu; istek gönderilmedi, hiçbir kayıt değişmedi.`;
}

// Sunucu sınırları — internal/thanos/cluster_identity.go (v0.10.956).
export const API_SERVER_URLS_MAX = 16;
export const ARGO_SUFFIX_MAX = 63;
const ARGO_SUFFIX_RE = /^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$/;
const DEFAULT_API_SERVER_PORT = '6443';
const IN_CLUSTER_HOSTS = new Set(['kubernetes.default.svc', 'kubernetes.default.svc.cluster.local']);

const E = {
  empty: 'boş API server URL',
  parse: 'ayrıştırılamadı (biçim: https://<host>[:port])',
  scheme: 'şema http ya da https olmalı',
  host: 'host yok (biçim: https://<host>[:port])',
  user: 'kullanıcı bilgisi (user@) taşıyamaz',
  queryFrag: 'sorgu ya da fragment taşıyamaz',
  path: 'yol taşıyamaz (yalnız şema + host + port)',
  port: 'port 1-65535 aralığında olmalı',
  inCluster: 'yazılamaz: küme-içi hedef',
} as const;

// ── Go net/url kopyası ─────────────────────────────────────────────────────
const isAlnum = (c: string) => /^[A-Za-z0-9]$/.test(c);
const isHex = (c: string | undefined) => c !== undefined && /^[0-9A-Fa-f]$/.test(c);
// Go shouldEscape(c, encodeHost) == false olan ASCII (ölçüldü: url.Parse
// "https://a<c>b" 0x20–0x7e) + yapısal ':' '[' ']'.
const HOST_PUNCT = new Set([...'!"$&\'()*+,-.;<=>_~:[]']);
const hostCharOk = (c: string) => c.charCodeAt(0) >= 0x80 || isAlnum(c) || HOST_PUNCT.has(c);
const USERINFO_PUNCT = new Set([...'-._:~!$&\'()*+,;=%@']);

type EscMode = 'host' | 'zone' | 'other';
/** Go `unescape`'in hata dalları: %XX biçimi; host'ta ASCII bayt %-kodlanamaz
 *  (%25 hariç); host/bölgede kaçışsız izinsiz ASCII karakter hata. */
function escapesOk(s: string, mode: EscMode): boolean {
  for (let i = 0; i < s.length;) {
    const c = s[i];
    if (c === '%') {
      if (!isHex(s[i + 1]) || !isHex(s[i + 2])) return false;
      const v = parseInt(s.slice(i + 1, i + 3), 16);
      const is25 = s.slice(i, i + 3) === '%25';
      if (mode === 'host' && v < 0x80 && !is25) return false;
      if (mode === 'zone' && !is25 && v !== 0x20 && (v >= 0x80 || !hostCharOk(String.fromCharCode(v)))) return false;
      i += 3;
      continue;
    }
    if ((mode === 'host' || mode === 'zone') && !hostCharOk(c)) return false;
    i++;
  }
  return true;
}

/** %XX dizilerini UTF-8 baytı olarak çözer (yalnız görüntü/anahtar için). */
function percentDecode(s: string): string {
  if (!s.includes('%')) return s;
  const bytes: number[] = [];
  const enc = new TextEncoder();
  for (let i = 0; i < s.length;) {
    if (s[i] === '%' && isHex(s[i + 1]) && isHex(s[i + 2])) {
      bytes.push(parseInt(s.slice(i + 1, i + 3), 16));
      i += 3;
    } else {
      const cp = s.codePointAt(i) ?? 0;
      const ch = String.fromCodePoint(cp);
      bytes.push(...enc.encode(ch));
      i += ch.length;
    }
  }
  return new TextDecoder().decode(new Uint8Array(bytes));
}

const validOptionalPort = (p: string) => p === '' || /^:[0-9]*$/.test(p);

/** netip.ParseAddr + !Is4 — köşeli parantez içi yalnız IPv6 (bölge kimliğiyle). */
function isIPv6Literal(s: string): boolean {
  const first = s.search(/[.:%]/);
  if (first < 0 || s[first] !== ':') return false; // IPv4 ya da adres yok
  let addr = s;
  const z = s.indexOf('%');
  if (z >= 0) {
    if (z === s.length - 1) return false; // boş bölge
    addr = s.slice(0, z);
  }
  const halves = addr.split('::');
  if (halves.length > 2) return false;
  const groups = (part: string, allowV4: boolean): number | null => {
    if (part === '') return 0;
    const fields = part.split(':');
    let n = 0;
    for (let k = 0; k < fields.length; k++) {
      const f = fields[k];
      if (allowV4 && k === fields.length - 1 && f.includes('.')) {
        const oct = f.split('.');
        if (oct.length !== 4 || !oct.every(o => /^(0|[1-9][0-9]{0,2})$/.test(o) && Number(o) <= 255)) return null;
        n += 2;
      } else if (/^[0-9A-Fa-f]{1,4}$/.test(f)) {
        n += 1;
      } else return null;
    }
    return n;
  };
  if (halves.length === 1) return groups(addr, true) === 8;
  const head = groups(halves[0], false);
  const tail = groups(halves[1], true);
  return head !== null && tail !== null && head + tail <= 7;
}

/** Go parseHost: köşeli parantezli IP-literal ya da host[:port]; dönen değer
 *  u.Host (kaçışları çözülmüş). null = ayrıştırma hatası. */
function parseHost(host: string): string | null {
  const ob = host.lastIndexOf('[');
  if (ob > 0) return null;
  if (ob === 0) {
    const cb = host.lastIndexOf(']');
    if (cb < 0) return null;
    const colonPort = host.slice(cb + 1);
    if (!validOptionalPort(colonPort)) return null;
    const hostname = host.slice(1, cb);
    const zi = hostname.indexOf('%25');
    let un: string;
    if (zi >= 0) {
      const h = hostname.slice(0, zi); const zone = hostname.slice(zi);
      if (!escapesOk(h, 'host') || !escapesOk(zone, 'zone')) return null;
      un = percentDecode(h) + percentDecode(zone);
    } else {
      if (!escapesOk(hostname, 'host')) return null;
      un = percentDecode(hostname);
    }
    if (!isIPv6Literal(un)) return null;
    return `[${un}]${colonPort}`;
  }
  // Go 1.25 (urlstrictcolons=0): birden çok ':' varsa SON iki noktadan böl.
  const last = host.lastIndexOf(':');
  if (last !== -1 && !validOptionalPort(host.slice(last))) return null;
  if (!escapesOk(host, 'host')) return null;
  return percentDecode(host);
}

interface Parsed {
  rawScheme: string; opaque: string; hasUser: boolean; host: string;
  rawQuery: string; forceQuery: boolean; fragment: string; path: string;
}

/** Go url.Parse'ın NormalizeAPIServerURL'ün baktığı alanları. null = hata. */
function goParse(raw: string): Parsed | null {
  const hash = raw.indexOf('#');
  const u = hash < 0 ? raw : raw.slice(0, hash);
  const frag = hash < 0 ? '' : raw.slice(hash + 1);
  // Go stringContainsCTLByte: < 0x20 ya da 0x7f.
  for (let i = 0; i < u.length; i++) { const cc = u.charCodeAt(i); if (cc < 0x20 || cc === 0x7f) return null; }
  const p: Parsed = { rawScheme: '', opaque: '', hasUser: false, host: '', rawQuery: '', forceQuery: false, fragment: '', path: '' };
  if (u === '*') { p.path = '*'; return p; }
  // getScheme
  let rest = u;
  for (let i = 0; i < u.length; i++) {
    const c = u[i];
    if (/[A-Za-z]/.test(c)) continue;
    if (/[0-9+\-.]/.test(c)) { if (i === 0) break; continue; }
    if (c === ':') {
      if (i === 0) return null; // missing protocol scheme
      p.rawScheme = u.slice(0, i); rest = u.slice(i + 1);
    }
    break;
  }
  const scheme = p.rawScheme.toLowerCase();
  if (rest.endsWith('?') && rest.split('?').length === 2) {
    p.forceQuery = true; rest = rest.slice(0, -1);
  } else {
    const q = rest.indexOf('?');
    if (q >= 0) { p.rawQuery = rest.slice(q + 1); rest = rest.slice(0, q); }
  }
  if (!rest.startsWith('/')) {
    if (scheme !== '') { p.opaque = rest; return withFragment(p, frag); }
    const seg = rest.split('/')[0];
    if (seg.includes(':')) return null;
  }
  if ((scheme !== '' || !rest.startsWith('///')) && rest.startsWith('//')) {
    let authority = rest.slice(2); rest = '';
    const slash = authority.indexOf('/');
    if (slash >= 0) { rest = authority.slice(slash); authority = authority.slice(0, slash); }
    const at = authority.lastIndexOf('@');
    const host = parseHost(at < 0 ? authority : authority.slice(at + 1));
    if (host === null) return null;
    if (at >= 0) {
      const userinfo = authority.slice(0, at);
      if (![...userinfo].every(ch => isAlnum(ch) || USERINFO_PUNCT.has(ch))) return null;
      if (!escapesOk(userinfo, 'other')) return null;
      p.hasUser = true;
    }
    p.host = host;
  }
  if (!escapesOk(rest, 'other')) return null;
  p.path = rest;
  return withFragment(p, frag);
}
function withFragment(p: Parsed, frag: string): Parsed | null {
  if (frag === '') return p;
  if (!escapesOk(frag, 'other')) return null;
  p.fragment = frag;
  return p;
}

/** Go splitHostPort(u.Host) → Hostname() / Port(). */
function splitHostPort(hostPort: string): { hostname: string; port: string } {
  let host = hostPort; let port = '';
  const colon = host.lastIndexOf(':');
  if (colon !== -1 && validOptionalPort(host.slice(colon))) { port = host.slice(colon + 1); host = host.slice(0, colon); }
  if (host.startsWith('[') && host.endsWith(']')) host = host.slice(1, -1);
  return { hostname: host, port };
}

export type ApiUrlNorm =
  | { ok: true; value: string; notes: string[] }
  | { ok: false; error: string };

/**
 * v0.10.974 — thanos.NormalizeAPIServerURL kopyası (kontrol SIRASI dahil):
 * boşluk kırpılır; şema http|https; şema + host küçük harf; sondaki `/`
 * atılır; port yoksa `:6443`; yol yalnız "" ya da "/"; userinfo, sorgu,
 * fragment reddedilir. `notes` önizlemenin "ne değişti" notlarıdır (boş =
 * değişmedi). Küme-içi hedef burada GEÇERLİDİR (Go'daki gibi); reddi
 * isInClusterAPIServerURL + checkArgoFields yapar.
 */
export function normalizeAPIServerURL(raw: string): ApiUrlNorm {
  const s = raw.trim();
  if (s === '') return { ok: false, error: E.empty };
  const u = goParse(s);
  if (!u) return { ok: false, error: E.parse };
  const scheme = u.rawScheme.toLowerCase();
  if (scheme !== 'http' && scheme !== 'https') return { ok: false, error: E.scheme };
  if (u.opaque !== '') return { ok: false, error: E.host };
  if (u.hasUser) return { ok: false, error: E.user };
  const { hostname, port: rawPort } = splitHostPort(u.host);
  const host = hostname.toLowerCase();
  if (host === '') return { ok: false, error: E.host };
  if (u.rawQuery !== '' || u.forceQuery) return { ok: false, error: E.queryFrag };
  if (u.fragment !== '' || s.includes('#')) return { ok: false, error: E.queryFrag };
  if (u.path !== '' && u.path !== '/') return { ok: false, error: E.path };
  let port = DEFAULT_API_SERVER_PORT;
  if (rawPort !== '') {
    const n = Number(rawPort);
    if (!Number.isFinite(n) || n < 1 || n > 65535) return { ok: false, error: E.port };
    port = String(n);
  }
  const value = `${scheme}://${host.includes(':') ? `[${host}]` : host}:${port}`;
  const notes: string[] = [];
  if (u.rawScheme !== scheme || hostname !== host) notes.push('küçük harf');
  if (u.path === '/') notes.push('sondaki / atıldı');
  if (rawPort === '') notes.push(':6443 eklendi');
  else if (rawPort !== port) notes.push('port sadeleşti');
  if (notes.length === 0 && value !== s) notes.push('biçim düzeltildi');
  return { ok: true, value, notes };
}

/** v0.10.974 — thanos.IsInClusterAPIServerURL kopyası: https + küme-içi host, port fark etmez. */
export function isInClusterAPIServerURL(raw: string): boolean {
  const n = normalizeAPIServerURL(raw);
  if (!n.ok || !n.value.startsWith('https://')) return false;
  return IN_CLUSTER_HOSTS.has(splitHostPort(n.value.slice('https://'.length)).hostname);
}

/** apiServerUrls metni: virgül ya da satır sonu ayırır; kırpılır, boşlar atılır (v0.10.956). */
export const splitUrlList = (text: string) => text.split(/[,\n]/).map(v => v.trim()).filter(Boolean);

// ── Önizleme ───────────────────────────────────────────────────────────────
export interface UrlPreviewLine {
  /** Kaydedilecek biçim (geçerliyse) ya da girilen değer (hatalıysa, userinfo gizli). */
  text: string;
  note: string;
  bad: boolean;
  inCluster?: true;
}

// userinfo'lu bir satır ekranda ve role=alert özetinde parolasız görünür
// (Go hata metni de ham girdiyi yankılamaz — apiserver_url.go başlığı).
const maskUserinfo = (s: string) => s.replace(/^([^:/?#]*:\/\/)[^/?#]*@/, '$1…@');

/** Tek kaydın satırları; kayıtlar arası tekillik checkArgoFields'ta. */
export function previewAPIServerUrls(text: string): UrlPreviewLine[] {
  const seen = new Set<string>();
  return splitUrlList(text).map((line): UrlPreviewLine => {
    const n = normalizeAPIServerURL(line);
    if (!n.ok) return { text: maskUserinfo(line), note: n.error, bad: true };
    if (isInClusterAPIServerURL(n.value)) return { text: maskUserinfo(line), note: E.inCluster, bad: true, inCluster: true };
    if (seen.has(n.value)) return { text: n.value, note: 'tekrar · bir kez kaydedilir', bad: false };
    seen.add(n.value);
    return { text: n.value, note: n.notes.length ? n.notes.join(' · ') : 'değişmedi', bad: false };
  });
}

// ── Kaydet öncesi denetim ──────────────────────────────────────────────────
export interface ArgoFieldRow {
  name: string;
  apiServerUrls: string;
  /** GET'te gelen metin ('' = yeni satır) — "düzenlenen satır" ayrımı için. */
  savedApiServerUrls: string;
  argoSuffix: string;
  savedArgoSuffix: string;
  pairGroup: string;
}
export interface ArgoIssue { field: 'api' | 'suffix'; text: string }
export interface ArgoFieldCheck {
  preview: UrlPreviewLine[];
  apiInvalid: boolean;
  /** Ek alanının satır içi hatası (yoksa ipucu SUFFIX_HINT). */
  suffixError?: string;
  pairHint: string;
  /** Özet maddeleri, alan sırasıyla ("<ad> · <alan>: <metin>"). */
  issues: ArgoIssue[];
}

const displayName = (rows: ArgoFieldRow[], i: number) => rows[i].name.trim() || `#${i + 1}`;

/**
 * v0.10.974 — Çakışma hangi satırda: sunucu (checkClusterUniqueness) liste
 * sırasında SONRAKİ kayda, ad farkıyla (`o != c.Name`) hata verir. Mockup
 * kuralı: hata DÜZENLENEN satıra bağlanır. Düzenlenen yoksa (içe aktarılmış
 * blob) sunucunun seçtiği sonraki kayıtlar. Dönen harita: hedef → diğer kayıt.
 */
function conflictTargets(members: number[], names: string[], edited: boolean[]): Map<number, number> {
  const out = new Map<number, number>();
  if (new Set(members.map(m => names[m])).size < 2) return out;
  let targets = members.filter(m => edited[m]);
  if (targets.length === 0) targets = members.slice(1);
  for (const t of targets) {
    const other = members.find(m => m !== t && names[m] !== names[t] && !targets.includes(m))
      ?? members.find(m => m !== t && names[m] !== names[t]);
    if (other !== undefined) out.set(t, other);
  }
  return out;
}

export function checkArgoFields(rows: ArgoFieldRow[]): ArgoFieldCheck[] {
  const names = rows.map(r => r.name.trim());
  const out: ArgoFieldCheck[] = rows.map(r => ({
    preview: previewAPIServerUrls(r.apiServerUrls), apiInvalid: false, pairHint: PAIR_HINT, issues: [],
  }));

  // API server URL — kayıtlar arası tekillik (kanonik anahtar).
  const owners = new Map<string, number[]>();
  out.forEach((c, i) => {
    for (const l of c.preview) {
      if (l.bad || l.note.startsWith('tekrar')) continue;
      const m = owners.get(l.text) ?? [];
      if (!m.includes(i)) m.push(i);
      owners.set(l.text, m);
    }
  });
  const urlEdited = rows.map(r => r.apiServerUrls.trim() !== r.savedApiServerUrls.trim());
  const crossDup = new Map<number, Map<string, number>>(); // satır → anahtar → diğer
  for (const [key, members] of owners) {
    for (const [t, o] of conflictTargets(members, names, urlEdited)) {
      const m = crossDup.get(t) ?? new Map<string, number>();
      m.set(key, o);
      crossDup.set(t, m);
    }
  }

  // Ek — biçim + tekillik (büyük/küçük harf duyarsız).
  const sfx = rows.map(r => r.argoSuffix.trim());
  const sfxValid = sfx.map(v => v === '' || (v.length <= ARGO_SUFFIX_MAX && ARGO_SUFFIX_RE.test(v)));
  const sfxOwners = new Map<string, number[]>();
  sfx.forEach((v, i) => {
    if (v === '' || !sfxValid[i]) return;
    const k = v.toLowerCase();
    sfxOwners.set(k, [...(sfxOwners.get(k) ?? []), i]);
  });
  const sfxEdited = rows.map(r => r.argoSuffix.trim() !== r.savedArgoSuffix.trim());
  const sfxDup = new Map<number, number>();
  for (const members of sfxOwners.values()) {
    for (const [t, o] of conflictTargets(members, names, sfxEdited)) sfxDup.set(t, o);
  }
  const takenSuffixes = sfx.filter(Boolean);

  out.forEach((c, i) => {
    const dups = crossDup.get(i);
    const hasExternal = c.preview.some(l => !l.bad);
    const lines = splitUrlList(rows[i].apiServerUrls);
    c.preview = c.preview.map((l, k) => {
      if (l.inCluster) {
        c.issues.push({ field: 'api', text: `${IN_CLUSTER_API_SERVER_URL} yazılamaz — Argo'nun küme-içi hedefi instance'ın hub'ına çözülür. Satırı silin; ${hasExternal ? "hub'ın dış API adresi zaten listede." : 'hub için dış API adresini girin.'}` });
        return l;
      }
      if (l.bad) {
        c.issues.push({ field: 'api', text: `${k + 1}. adres: ${l.note}` });
        return l;
      }
      const o = dups?.get(l.text);
      if (o === undefined || l.note.startsWith('tekrar')) return l;
      c.issues.push({ field: 'api', text: `${l.text} ${displayName(rows, o)} kaydında da var; bir API server adresi aynı anda tek kayda bağlanabilir.` });
      return { text: l.text, note: `${displayName(rows, o)} kaydında da var`, bad: true };
    });
    const unique = new Set(lines.map(normalizeAPIServerURL).flatMap(n => (n.ok ? [n.value] : []))).size;
    if (unique > API_SERVER_URLS_MAX) {
      c.issues.push({ field: 'api', text: `en çok ${API_SERVER_URLS_MAX} adres taşıyabilir (${unique} verildi)` });
    }
    c.apiInvalid = c.issues.length > 0;

    const v = sfx[i];
    if (v !== '' && !sfxValid[i]) {
      c.suffixError = `“${v}” geçersiz: harf/rakamla başlayıp biten, en çok ${ARGO_SUFFIX_MAX} karakter (arada . _ - olabilir).`;
      c.issues.push({ field: 'suffix', text: c.suffixError });
    } else if (sfxDup.has(i)) {
      const other = displayName(rows, sfxDup.get(i)!);
      const me = displayName(rows, i);
      const ex = suggestSuffix(names[i], takenSuffixes);
      const eg = ex ? `, ör. ${ex}` : '';
      c.suffixError = `“${v}” eki ${other} kaydında da var (büyük/küçük harf duyarsız). Ek, uygulama adının son jetonundan kümeyi seçer; her kümede tekil olmalı. ${me} uygulamalarındaki son jetonu girin${eg}.`;
      c.issues.push({ field: 'suffix', text: `“${v}” ${other} kaydında da var (büyük/küçük harf duyarsız). ${trDative(me)} kendi ekini verin${eg}.` });
    }

    const pg = rows[i].pairGroup.trim();
    if (pg) {
      const group = [...new Set(rows.flatMap((r, j) => (j !== i && r.pairGroup.trim() === pg && names[j] && names[j] !== names[i] ? [names[j]] : [])))];
      if (group.length) c.pairHint = `${PAIR_HINT} Bu grupta: ${group.join(', ')}.`;
    }
  });
  return out;
}

// ── Argo hub rozeti ────────────────────────────────────────────────────────
/**
 * v0.10.974 — Argo CD ayarındaki hub'lar → bağlı instance sayısı (hubs sırası).
 * Rozet ve not buradan okunur; hub seçimi yalnız Ayarlar › Argo CD'de değişir.
 * Sayım BE4 kuralıyla aynı: etkin/kapalı ayrımı yok (kapalı instance da hub'a bağlı).
 */
export function argoHubInstances(res: Pick<ArgoCDSettingsResponse, 'settings'> | null | undefined): Map<string, number> {
  const out = new Map<string, number>();
  for (const h of res?.settings.hubs ?? []) {
    const id = (h.clusterId || '').trim();
    if (id && !out.has(id)) out.set(id, 0);
  }
  for (const inst of res?.settings.instances ?? []) {
    const id = (inst.hubClusterId || '').trim();
    if (out.has(id)) out.set(id, (out.get(id) ?? 0) + 1);
  }
  return out;
}

// ── Türkçe ek + örnek ek ───────────────────────────────────────────────────
const BACK = new Set([...'aıou']);
const FRONT = new Set([...'eiöü']);
const LETTER_NAMES: Record<string, string> = {
  a: 'a', b: 'be', c: 'ce', ç: 'çe', d: 'de', e: 'e', f: 'fe', g: 'ge', ğ: 'yumuşak ge', h: 'he',
  ı: 'ı', i: 'i', j: 'je', k: 'ke', l: 'le', m: 'me', n: 'ne', o: 'o', ö: 'ö', p: 'pe', q: 'kü',
  r: 're', s: 'se', ş: 'şe', t: 'te', u: 'u', ü: 'ü', v: 've', w: 've', x: 'iks', y: 'ye', z: 'ze',
};
const ONES = ['', 'bir', 'iki', 'üç', 'dört', 'beş', 'altı', 'yedi', 'sekiz', 'dokuz'];
const TENS = ['', 'on', 'yirmi', 'otuz', 'kırk', 'elli', 'altmış', 'yetmiş', 'seksen', 'doksan'];
const GROUPS = ['bin', 'milyon', 'milyar', 'trilyon', 'katrilyon'];

/** Sayının Türkçe okunuşunun SON sözcüğü (ek uyumu yalnız ona bakar). */
function lastNumberWord(digits: string): string {
  const d = digits.replace(/^0+/, '');
  if (d === '') return 'sıfır';
  for (let g = 0; g * 3 < d.length; g++) {
    const chunk = Number(d.slice(Math.max(0, d.length - 3 * (g + 1)), d.length - 3 * g));
    if (chunk === 0) continue;
    if (g > 0) return GROUPS[g - 1] ?? 'bin';
    if (chunk % 10) return ONES[chunk % 10];
    if (Math.floor(chunk / 10) % 10) return TENS[Math.floor(chunk / 10) % 10];
    return 'yüz';
  }
  return 'sıfır';
}

/**
 * v0.10.974 — Kimliğe yönelme eki: "cluster-a" → "cluster-a'ya", "hub-1" →
 * "hub-1'e", "cluster-b" → "cluster-b'ye". Okunuş: sondaki sayı Türkçe
 * okunur; tek harf ya da ünlüsüz parça harf adıyla; gerisi sözcük olarak
 * (son ünlü uyumu, ünlüyle bitene kaynaştırma y). trace iş akışının
 * trSuffix.ts'i izlenmeyen dosya olduğu için ondan alınmaz.
 */
export function trDative(word: string): string {
  const w = word.trim();
  if (!w) return '';
  const lower = w.toLocaleLowerCase('tr');
  const tail = lower.match(/[0-9]+$/)?.[0];
  let spoken: string;
  if (tail) spoken = lastNumberWord(tail);
  else {
    const letters = lower.match(/[a-zçğıöşü]+$/)?.[0] ?? '';
    const hasVowel = [...letters].some(ch => BACK.has(ch) || FRONT.has(ch));
    spoken = letters && hasVowel && letters.length > 1 ? letters : LETTER_NAMES[letters.slice(-1)] ?? letters;
  }
  const vowels = [...spoken].filter(ch => BACK.has(ch) || FRONT.has(ch));
  const lastV = vowels[vowels.length - 1];
  if (!lastV) return `${w}'e`;
  const a = BACK.has(lastV) ? 'a' : 'e';
  const endsVowel = BACK.has(spoken.slice(-1)) || FRONT.has(spoken.slice(-1));
  return `${w}'${endsVowel ? 'y' : ''}${a}`;
}

/** v0.10.974 — Örnek ek: ad parçalarının baş harfleri (cluster-a → ca, hub-1 → h1);
 *  başka kayıtta varsa ya da sunucu biçimine uymuyorsa yok. */
export function suggestSuffix(name: string, taken: Iterable<string>): string | undefined {
  const s = name.toLowerCase().split(/[^a-z0-9]+/).filter(Boolean).map(t => t[0]).join('');
  if (!s || s.length > ARGO_SUFFIX_MAX || !ARGO_SUFFIX_RE.test(s)) return undefined;
  const used = new Set([...taken].map(t => t.trim().toLowerCase()));
  return used.has(s) ? undefined : s;
}
