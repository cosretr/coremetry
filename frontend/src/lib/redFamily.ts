// redFamily.ts — v0.10.1429 (operatör, prod: Service · coremetry-ingest →
// Overview metrik kipi "Bu filtrede veri yok — http_server_request_duration_
// seconds"; "gRPC metriklerini de HTTP Metrics altına ekleyebiliriz bence").
//
// SAF: metrik kipinin RED AİLE kataloğu — Service → Overview / HTTP Metrics,
// /endpoints listesi ve endpoint detayı aynı PromQL üreticilerini
// (lib/httpMetrics.ts) hangi aileyle çalıştıracaklarını buradan okur.
// Go kataloğu internal/api/red_family.go ile BAYT BAYT eşli (pin testi
// TestREDFamilyLabelsMatchFrontend): VM adları, etiketler, hata kümeleri.
//
//   http         http_server_request_duration_seconds_*   (s)  — bugünkü aile, bayt bayt aynı sorgular
//   grpc_legacy  rpc_server_duration_milliseconds_*       (ms) — rpc.server.duration (instrumentasyon VARSAYILANI)
//   grpc_stable  rpc_server_call_duration_seconds_*       (s)  — rpc.server.call.duration (opt-in rpc)
//
// Kurallar: ms ↔ s gRPC aileleri tek sorguda birleşmez (ad aile başına TAM);
// HTTP + gRPC toplanmaz (HTTP birincil, gRPC ayrı bölüm). Gecikme ölçeği
// ailenin BEYAN ETTİĞİ birimden (ms → PromQL'de /1000, SLI kovası le="500").
// gRPC hata kümesi = SUNUCU-HATASI kodları (grpc-gateway kod→HTTP eşlemesinin
// 5xx karşılığı): 2 UNKNOWN, 4 DEADLINE_EXCEEDED, 8 RESOURCE_EXHAUSTED,
// 12 UNIMPLEMENTED, 13 INTERNAL, 14 UNAVAILABLE, 15 DATA_LOSS; istemci kodları
// (1 3 5 6 7 9 10 11 16) 4xx gibi ayrı oranda — NOT_FOUND sunucu hatası
// sayılmaz. Route eşdeğeri (rpc_service, rpc_method): sütunlar "Service /
// Method", lejant "pkg.Service/Method".
import type { RedFamilyId, RedFamilyKind, RedFamilyPresence, RedFamilyResolution } from './types';
export type { RedFamilyId, RedFamilyKind, RedFamilyPresence, RedFamilyResolution } from './types';

export interface RedFamilyLabels { service: string; route: string; method: string; status: string }

export interface RedFamilySpec {
  id: RedFamilyId;
  kind: RedFamilyKind;
  /** Rozet / başlık: "HTTP" | "gRPC". */
  label: string;
  /** OTel adı (sunucu kimliği; /metric-red note'u). */
  probe: string;
  names: { count: string; bucket: string; sum: string };
  unit: 's' | 'ms';
  labels: RedFamilyLabels;
  /** `status=~"<serverErr>"` regex gövdeleri (sunucu / istemci hatası). */
  serverErr: string;
  clientErr: string;
  /** SLI kovası (`le="…"`): 500 ms'nin bu ailenin biriminde yazımı. */
  sliLe: string;
  /** Sütun başlıkları: route eşdeğeri / method eşdeğeri. */
  routeTitle: string;
  methodTitle: string;
}

export const HTTP_FAMILY: RedFamilySpec = {
  id: 'http', kind: 'http', label: 'HTTP',
  probe: 'http.server.request.duration',
  names: {
    count: 'http_server_request_duration_seconds_count',
    bucket: 'http_server_request_duration_seconds_bucket',
    sum: 'http_server_request_duration_seconds_sum',
  },
  unit: 's',
  labels: { service: 'service_name', route: 'http_route', method: 'http_request_method', status: 'http_response_status_code' },
  serverErr: '5..', clientErr: '4..',
  sliLe: '0.5',
  routeTitle: 'Route', methodTitle: 'Method',
};

export const GRPC_LEGACY_FAMILY: RedFamilySpec = {
  id: 'grpc_legacy', kind: 'grpc', label: 'gRPC',
  probe: 'rpc.server.duration',
  names: {
    count: 'rpc_server_duration_milliseconds_count',
    bucket: 'rpc_server_duration_milliseconds_bucket',
    sum: 'rpc_server_duration_milliseconds_sum',
  },
  unit: 'ms',
  labels: { service: 'service_name', route: 'rpc_method', method: 'rpc_service', status: 'rpc_grpc_status_code' },
  serverErr: '2|4|8|12|13|14|15', clientErr: '1|3|5|6|7|9|10|11|16',
  sliLe: '500',
  routeTitle: 'Method', methodTitle: 'Service',
};

export const GRPC_STABLE_FAMILY: RedFamilySpec = {
  id: 'grpc_stable', kind: 'grpc', label: 'gRPC',
  probe: 'rpc.server.call.duration',
  names: {
    count: 'rpc_server_call_duration_seconds_count',
    bucket: 'rpc_server_call_duration_seconds_bucket',
    sum: 'rpc_server_call_duration_seconds_sum',
  },
  unit: 's',
  labels: { service: 'service_name', route: 'rpc_method', method: 'rpc_service', status: 'rpc_response_status_code' },
  serverErr: '2|4|8|12|13|14|15', clientErr: '1|3|5|6|7|9|10|11|16',
  sliLe: '0.5',
  routeTitle: 'Method', methodTitle: 'Service',
};

export const RED_FAMILIES: readonly RedFamilySpec[] = [HTTP_FAMILY, GRPC_LEGACY_FAMILY, GRPC_STABLE_FAMILY];

/** Katalog araması; bilinmeyen / boş → HTTP (bugünkü sorgular). */
export function familyById(id: string | undefined | null): RedFamilySpec {
  return RED_FAMILIES.find(f => f.id === id) ?? HTTP_FAMILY;
}

/** gRPC durum kodu → ad (lejant "14 UNAVAILABLE"). */
export const GRPC_CODE_NAMES: Readonly<Record<string, string>> = {
  '0': 'OK', '1': 'CANCELLED', '2': 'UNKNOWN', '3': 'INVALID_ARGUMENT', '4': 'DEADLINE_EXCEEDED',
  '5': 'NOT_FOUND', '6': 'ALREADY_EXISTS', '7': 'PERMISSION_DENIED', '8': 'RESOURCE_EXHAUSTED',
  '9': 'FAILED_PRECONDITION', '10': 'ABORTED', '11': 'OUT_OF_RANGE', '12': 'UNIMPLEMENTED',
  '13': 'INTERNAL', '14': 'UNAVAILABLE', '15': 'DATA_LOSS', '16': 'UNAUTHENTICATED',
};

const GRPC_SERVER_FAULT = new Set(GRPC_LEGACY_FAMILY.serverErr.split('|'));
const GRPC_CLIENT_FAULT = new Set(GRPC_LEGACY_FAMILY.clientErr.split('|'));

/** Durum sınıfı etiketi: HTTP "2xx".."5xx" (ilk hane); gRPC ok | client | server | '' (bilinmeyen kod). */
export function statusClassOf(family: RedFamilySpec, code: string): string {
  if (family.kind === 'http') return code ? `${code.charAt(0)}xx` : '';
  const c = code.trim();
  if (c === '0') return 'ok';
  if (GRPC_SERVER_FAULT.has(c)) return 'server';
  if (GRPC_CLIENT_FAULT.has(c)) return 'client';
  return '';
}

/** Sınıf → HTTP_SERIES_TOKENS anahtarı (renk): gRPC ok→2xx, client→4xx, server→5xx. */
export function statusClassToken(cls: string): string {
  return cls === 'ok' ? '2xx' : cls === 'client' ? '4xx' : cls === 'server' ? '5xx' : cls;
}

/** Durum kodu lejantı: HTTP kod aynen; gRPC "14 UNAVAILABLE". */
export function statusCodeLabel(family: RedFamilySpec, code: string): string {
  if (family.kind !== 'grpc') return code;
  const n = GRPC_CODE_NAMES[code.trim()];
  return n ? `${code} ${n}` : code;
}

/** Hata metni rozetler/notlar için: "5xx" | "sunucu-hatası (2|4|8|12|13|14|15)". */
export function serverErrorLabel(family: RedFamilySpec): string {
  return family.kind === 'grpc' ? `sunucu-hatası kodları (${family.serverErr})` : '5xx';
}

// ── GET /api/services/{name}/red-family cevabı (lib/types.ts RedFamilyResolution) → sayfa görünümü ──

/** Sayfada çizilecek aile bölümü. */
export interface RedFamilySection { family: RedFamilySpec; presence?: RedFamilyPresence }

export type RedFamilyState = 'pending' | 'known' | 'unknown' | 'error';

export interface RedFamilyView {
  state: RedFamilyState;
  /** Sorgular atılabilir mi (pending dışında her hâl). */
  ready: boolean;
  reason?: string;
  /** Birincil tür: http | grpc | none (çözüm yok / bilinmiyor → http). */
  primary: RedFamilyKind | 'none';
  http?: RedFamilySection;
  grpc?: RedFamilySection;
  both: boolean;
  tried: string[];
}

/**
 * Çözüm → görünüm. Çözüm yokken / okunamadıyken / uç başarısızken HTTP
 * bölümü varsayılır (bugünkü sorgular koşar, bant/rozet söyler); "yok"
 * iddiası yalnız known'da.
 */
export function redFamilyView(q: { data?: RedFamilyResolution; error?: Error | null }, enabled = true): RedFamilyView {
  const httpDefault: RedFamilySection = { family: HTTP_FAMILY };
  if (!enabled) return { state: 'known', ready: true, primary: 'http', http: httpDefault, both: false, tried: [] };
  if (q.error) return { state: 'error', ready: true, reason: q.error.message, primary: 'http', http: httpDefault, both: false, tried: [] };
  const r = q.data;
  if (!r) return { state: 'pending', ready: false, primary: 'http', both: false, tried: [] };
  if (r.state === 'unknown') {
    return { state: 'unknown', ready: true, reason: r.reason, primary: 'http', http: httpDefault, both: false, tried: r.tried ?? [] };
  }
  const http = r.http ? { family: HTTP_FAMILY, presence: r.http } : undefined;
  const grpc = r.grpc ? { family: familyById(r.grpc.id), presence: r.grpc } : undefined;
  return {
    state: 'known', ready: true, reason: r.reason, primary: r.primary,
    http, grpc, both: !!http && !!grpc, tried: r.tried ?? [],
  };
}

/** Tek aileli yüzeyin (Overview, endpoint detayı) bölümü: istenen tür varsa o, yoksa birincil, o da yoksa HTTP. */
export function pickFamilySection(v: RedFamilyView, want?: RedFamilyKind): RedFamilySection {
  if (want === 'grpc') return v.grpc ?? { family: GRPC_LEGACY_FAMILY };
  if (want === 'http') return v.http ?? { family: HTTP_FAMILY };
  if (v.primary === 'grpc' && v.grpc) return v.grpc;
  return v.http ?? (v.grpc ?? { family: HTTP_FAMILY });
}

/** Sekmede çizilecek bölümler: HTTP önce, gRPC altta; hiçbiri yoksa HTTP (boş durumu dürüstçe gösterir). */
export function familySections(v: RedFamilyView): RedFamilySection[] {
  const out: RedFamilySection[] = [];
  if (v.http) out.push(v.http);
  if (v.grpc) out.push(v.grpc);
  return out.length ? out : [{ family: HTTP_FAMILY }];
}

/** Overview rozet eki: " · gRPC" (yalnız gRPC) | " · +gRPC" (ikisi) | "". */
export function familyBadgeSuffix(v: RedFamilyView): string {
  if (v.primary === 'grpc') return ' · gRPC';
  if (v.both) return ' · +gRPC';
  return '';
}

/**
 * v0.10.1559 — kısmi çözüm notu: bazı aileler okunamadı (ör. HTTP etiket
 * okuması zaman aşımı), okunanlar çizildi. Yalnız HTTP/RPC sekmesi gösterir;
 * Overview'un "bilinemedi" rozeti bu hâlde YANLIŞ olurdu (aile biliniyor).
 */
export function familyPartialNote(v: RedFamilyView): string | null {
  return v.state === 'known' && v.reason ? v.reason : null;
}

/** Çözüm bilinmiyor / okunamadı notu (null = not yok). */
export function familyUnknownNote(v: RedFamilyView): string | null {
  if (v.state === 'unknown') return `Metrik ailesi BİLİNEMEDİ (${v.reason ?? 'okunamadı'}) — HTTP sorguları koşuyor; gRPC yok DEĞİL`;
  if (v.state === 'error') return `Metrik ailesi çözülemedi (${v.reason ?? 'uç hatası'}) — HTTP sorguları koşuyor`;
  return null;
}
