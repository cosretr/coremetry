/**
 * traceHealth — "Trace hattı sağlığı" panelinin saf yarısı (v0.10.757).
 * Kart kararları görünmez sayılardan verilir; yanlış dal yanlış rozet basar,
 * o yüzden tablo-testli.
 */
export interface TraceHealthPodLike {
  accepted: number; dropped: number; writeFailed: number;
  rejects?: Record<string, number>;
}

// v0.10.929 (K5) — 'b-ok' tipten çıktı: "kayıp yok", temiz adlar, ≥%99.5
// saklandı SAĞLIKLI hâl, geçiş değil → nötr. Renk yalnız sapmada.
export type LossTone = 'b-warn' | 'b-err' | 'b-gray';

/**
 * Kayıp kartı rozeti: kalıcı kayıp (drop + write_failed + reddedilen istek) → err;
 * spool tıkalı → err (henüz kayıp değil ama kuyrukta; 2026-08 olay sınıfı);
 * yalnız kalite sayaçları → warn. v0.10.760: ingest rolü olmayan pod (api)
 * sayaç taşımaz — "kayıp yok" YERİNE "bu pod ingest değil" (prod ekranı).
 */
export function lossVerdict(p: TraceHealthPodLike, spoolDegraded = false, ingestRole = true): { tone: LossTone; text: string } {
  if (spoolDegraded) return { tone: 'b-err', text: 'spool tıkalı' };
  if (!ingestRole) return { tone: 'b-gray', text: 'bu pod ingest değil' };
  const r = p.rejects ?? {};
  const lost = p.dropped + p.writeFailed + (r.http_decode_failed ?? 0) + (r.http_body_too_large ?? 0) + (r.grpc_message_too_big ?? 0);
  const quality = (r.span_empty_id ?? 0) + (r.span_invalid_time ?? 0);
  if (lost > 0) return { tone: 'b-err', text: `${lost.toLocaleString()} kayıp` };
  if (quality > 0) return { tone: 'b-warn', text: `${quality.toLocaleString()} kalite işareti` };
  return { tone: 'b-gray', text: 'kayıp yok' };
}

/** Yüzde (0-100) ya da null (payda 0). */
export function pctOf(n: number, d: number): number | null {
  return d > 0 ? (n / d) * 100 : null;
}

/** Kova çubukları: en yüksek kova 100. Boş → []. */
export function bucketBars(buckets: { t: number; spans: number }[]): { t: number; spans: number; h: number }[] {
  const max = buckets.reduce((m, b) => Math.max(m, b.spans), 0);
  return buckets.map(b => ({ ...b, h: max > 0 ? Math.max(2, Math.round((b.spans / max) * 100)) : 0 }));
}

/** Ad kalitesi rozeti: çıplak fiil payı ≥ %20 err, ≥ %5 warn. */
export function nameTone(barePct: number | null): LossTone {
  if (barePct === null) return 'b-gray';
  return barePct >= 20 ? 'b-err' : barePct >= 5 ? 'b-warn' : 'b-gray';
}

// v0.10.767 (Faz B) — filo mutabakatı rozeti. Oran = yerleşmiş pencerede
// CH'de saklanan ÷ ingest podlarının kabul ettiği. %100'ü aşabilir (geç
// span; write_failed MV kaskadını fazla sayar) — olduğu gibi yazılır.
export interface FleetLike {
  accepted: number; storedSettled: number; storedKnown: boolean; empty: boolean; settledFrom: number; settledTo: number;
  /** v0.10.770 — defterin kapsadığı pencere; yoksa settledFrom / accepted kullanılır. */
  coveredFrom?: number; acceptedSettled?: number;
}
// v0.10.770 — oran yalnız defterin kapsadığı kovalardan (prod: deploy'dan
// 15 dk sonra 6 saatlik pencere "%7495" demişti). %110 üstü yeşil OLAMAZ:
// kapsam/zaman kayması işareti, sarı ve "kapsam?" der.
export function fleetVerdict(f: FleetLike): { tone: LossTone; text: string; pct: number | null } {
  if (f.empty) return { tone: 'b-gray', text: 'defter boş', pct: null };
  if (f.settledTo <= f.settledFrom) return { tone: 'b-gray', text: 'pencere kısa', pct: null };
  const covered = f.coveredFrom ?? f.settledFrom;
  if (covered >= f.settledTo) return { tone: 'b-gray', text: 'defter henüz yerleşmedi', pct: null };
  if (!f.storedKnown) return { tone: 'b-gray', text: 'saklanan okunamadı', pct: null };
  const accepted = f.acceptedSettled ?? f.accepted;
  if (accepted <= 0) return { tone: 'b-gray', text: 'kabul yok', pct: null };
  const pct = (f.storedSettled / accepted) * 100;
  const num = pct >= 100 ? String(Math.round(pct)) : pct.toFixed(1);
  if (pct > 110) return { tone: 'b-warn', text: `%${num} saklandı (kapsam?)`, pct };
  return { tone: pct >= 99.5 ? 'b-gray' : pct >= 97 ? 'b-warn' : 'b-err', text: `%${num} saklandı`, pct };
}

// v0.10.823 — ham sayım satırları. Şard ETİKETLENİR: aynı shard'ın host'ları
// kıyaslanabilir, farklı shard'larınki KIYASLANAMAZ (shard anahtarı veriyi
// zaten böler). Eşlenemeyen host (shard < 0) "?" ile işaretlenir ve
// sunucudaki kıyasa da girmemiştir.
export interface RawHostLike { host: string; shard: number; count: number }

/** Satır etiketi: "ham · shard 1 · ch-01"; eşlenemeyen host "shard ?". */
export function rawHostLabel(h: RawHostLike): string {
  return `ham · shard ${h.shard < 0 ? '?' : h.shard} · ${h.host}`;
}

/** Önce shard (eşlenemeyenler EN SONA), sonra host adı. Girdi kopyalanır. */
export function sortRawHosts(hs: RawHostLike[]): RawHostLike[] {
  const rank = (s: number) => (s < 0 ? Number.MAX_SAFE_INTEGER : s);
  return [...hs].sort((a, b) => rank(a.shard) - rank(b.shard) || a.host.localeCompare(b.host));
}

// stalePods — son örneği maxAgeS'den eski pod sayısı (defter kalp atışı 60 s).
export function stalePods(pods: { lastSampleAt: number }[], nowNs: number, maxAgeS = 180): number {
  return pods.filter(p => nowNs - p.lastSampleAt > maxAgeS * 1e9).length;
}

// v0.10.772 — "N pod bayat" rollout sırasında kırmızıydı (prod 770 deploy'u:
// eski ReplicaSet'in 3 podu kapanınca 3 dk sonra bayat sayıldı). Kapanan
// eski pod ile takılı pod aynı görünür; ayrım K8s ad kalıbından: pod adı
// <deploy>-<rs-hash>-<ek>. Bayat podların hepsi taze podlarda GÖRÜNMEYEN bir
// rs-hash taşıyorsa bu bir rollout artığıdır, gri; değilse gerçekten bayat.
export function podRSHash(pod: string): string {
  const parts = pod.split('-');
  return parts.length >= 3 ? parts[parts.length - 2] : pod;
}
export function staleVerdict(pods: { pod: string; lastSampleAt: number }[], nowNs: number, maxAgeS = 180): { count: number; tone: LossTone; text: string } | null {
  const stale = pods.filter(p => nowNs - p.lastSampleAt > maxAgeS * 1e9);
  if (stale.length === 0) return null;
  const fresh = pods.filter(p => nowNs - p.lastSampleAt <= maxAgeS * 1e9);
  const freshHashes = new Set(fresh.map(p => podRSHash(p.pod)));
  const rollout = fresh.length > 0 && stale.every(p => !freshHashes.has(podRSHash(p.pod)));
  return rollout
    ? { count: stale.length, tone: 'b-gray', text: `${stale.length} eski pod (rollout)` }
    : { count: stale.length, tone: 'b-err', text: `${stale.length} pod bayat` };
}
