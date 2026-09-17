/**
 * traceHealth — "Trace hattı sağlığı" panelinin saf yarısı (v0.10.757).
 * Kart kararları görünmez sayılardan verilir; yanlış dal yanlış rozet basar,
 * o yüzden tablo-testli.
 */
export interface TraceHealthPodLike {
  accepted: number; dropped: number; writeFailed: number;
  rejects?: Record<string, number>;
}

export type LossTone = 'b-ok' | 'b-warn' | 'b-err' | 'b-gray';

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
  return { tone: 'b-ok', text: 'kayıp yok' };
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
  if (barePct === null) return 'b-ok';
  return barePct >= 20 ? 'b-err' : barePct >= 5 ? 'b-warn' : 'b-ok';
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
  return { tone: pct >= 99.5 ? 'b-ok' : pct >= 97 ? 'b-warn' : 'b-err', text: `%${num} saklandı`, pct };
}

// stalePods — son örneği maxAgeS'den eski pod sayısı (defter kalp atışı 60 s).
export function stalePods(pods: { lastSampleAt: number }[], nowNs: number, maxAgeS = 180): number {
  return pods.filter(p => nowNs - p.lastSampleAt > maxAgeS * 1e9).length;
}
