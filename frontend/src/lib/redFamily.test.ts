// redFamily.test.ts — v0.10.1429 (operator context: coremetry-ingest gRPC RED).
// Katalog pinleri (Go red_family.go ile çapraz pin TestREDFamilyLabelsMatchFrontend),
// çözüm → görünüm tablosu (unknown/error → HTTP sorguları koşar, "yok" iddiası
// yalnız known'da), bölüm seçimi (HTTP önce, gRPC altta; ?family=grpc), rozet
// eki, api.serviceRedFamily yolu (VM'e sabit, boş env/cluster gönderilmez).
import { describe, it, expect, vi } from 'vitest';
import {
  HTTP_FAMILY, GRPC_LEGACY_FAMILY, GRPC_STABLE_FAMILY, RED_FAMILIES, familyById, GRPC_CODE_NAMES,
  statusClassOf, statusClassToken, statusCodeLabel, serverErrorLabel,
  redFamilyView, pickFamilySection, familySections, familyBadgeSuffix, familyUnknownNote, familyPartialNote,
  type RedFamilyResolution,
} from './redFamily';

describe('katalog', () => {
  it('üç aile, sıra HTTP → legacy → stable; adlar/etiketler/hata kümeleri pinli', () => {
    expect(RED_FAMILIES.map(f => f.id)).toEqual(['http', 'grpc_legacy', 'grpc_stable']);
    expect(HTTP_FAMILY.names.count).toBe('http_server_request_duration_seconds_count');
    expect(HTTP_FAMILY.labels).toEqual({ service: 'service_name', route: 'http_route', method: 'http_request_method', status: 'http_response_status_code' });
    expect(HTTP_FAMILY.serverErr).toBe('5..');
    expect(GRPC_LEGACY_FAMILY).toMatchObject({ unit: 'ms', sliLe: '500', probe: 'rpc.server.duration',
      names: { count: 'rpc_server_duration_milliseconds_count', bucket: 'rpc_server_duration_milliseconds_bucket', sum: 'rpc_server_duration_milliseconds_sum' },
      labels: { route: 'rpc_method', method: 'rpc_service', status: 'rpc_grpc_status_code' },
      serverErr: '2|4|8|12|13|14|15', clientErr: '1|3|5|6|7|9|10|11|16', routeTitle: 'Method', methodTitle: 'Service' });
    expect(GRPC_STABLE_FAMILY).toMatchObject({ unit: 's', sliLe: '0.5', probe: 'rpc.server.call.duration',
      names: { count: 'rpc_server_call_duration_seconds_count' }, labels: { status: 'rpc_response_status_code' } });
    expect(familyById('grpc_stable').id).toBe('grpc_stable');
    expect(familyById('nope').id).toBe('http');
    expect(familyById(undefined).id).toBe('http');
    expect(Object.keys(GRPC_CODE_NAMES)).toHaveLength(17);
  });
  it('durum sınıfı / kod adı / renk tokenı', () => {
    expect(statusClassOf(HTTP_FAMILY, '503')).toBe('5xx');
    expect(statusClassOf(GRPC_LEGACY_FAMILY, '0')).toBe('ok');
    expect(statusClassOf(GRPC_LEGACY_FAMILY, '14')).toBe('server');
    expect(statusClassOf(GRPC_LEGACY_FAMILY, '5')).toBe('client'); // NOT_FOUND sunucu hatası DEĞİL
    expect(statusClassOf(GRPC_LEGACY_FAMILY, '99')).toBe('');
    expect(statusClassToken('server')).toBe('5xx');
    expect(statusClassToken('2xx')).toBe('2xx');
    expect(statusCodeLabel(GRPC_LEGACY_FAMILY, '13')).toBe('13 INTERNAL');
    expect(statusCodeLabel(GRPC_LEGACY_FAMILY, '42')).toBe('42');
    expect(statusCodeLabel(HTTP_FAMILY, '500')).toBe('500');
    expect(serverErrorLabel(GRPC_LEGACY_FAMILY)).toBe('sunucu-hatası kodları (2|4|8|12|13|14|15)');
    expect(serverErrorLabel(HTTP_FAMILY)).toBe('5xx');
  });
});

const res = (over: Partial<RedFamilyResolution>): RedFamilyResolution => ({ service: 'grpc.example.test', state: 'known', primary: 'none', tried: [], ...over });

describe('redFamilyView — çözüm → görünüm', () => {
  it('kapalı → HTTP hazır; bekleniyor → pending (sorgu yok)', () => {
    expect(redFamilyView({}, false)).toMatchObject({ state: 'known', ready: true, primary: 'http', http: { family: HTTP_FAMILY } });
    expect(redFamilyView({})).toMatchObject({ state: 'pending', ready: false });
  });
  it('uç hatası / unknown → HTTP sorguları koşar, not söyler (yok DEĞİL)', () => {
    const e = redFamilyView({ error: new Error('HTTP 502') });
    expect(e).toMatchObject({ state: 'error', ready: true, primary: 'http', reason: 'HTTP 502' });
    expect(familyUnknownNote(e)).toContain('çözülemedi (HTTP 502)');
    const u = redFamilyView({ data: res({ state: 'unknown', primary: 'http', reason: 'VictoriaMetrics okuması BAŞARISIZ' }) });
    expect(u).toMatchObject({ state: 'unknown', primary: 'http', http: { family: HTTP_FAMILY } });
    expect(familyUnknownNote(u)).toContain('gRPC yok DEĞİL');
  });
  it('yalnız gRPC → primary grpc, bölüm legacy/stable kimliğinden', () => {
    const v = redFamilyView({ data: res({ primary: 'grpc', grpc: { id: 'grpc_legacy', kind: 'grpc', statusLabel: 'rpc_grpc_status_code' } }) });
    expect(v.primary).toBe('grpc');
    expect(v.http).toBeUndefined();
    expect(v.grpc?.family).toBe(GRPC_LEGACY_FAMILY);
    expect(pickFamilySection(v).family.id).toBe('grpc_legacy');
    expect(familySections(v).map(s => s.family.id)).toEqual(['grpc_legacy']);
    expect(familyBadgeSuffix(v)).toBe(' · gRPC');
    expect(familyUnknownNote(v)).toBeNull();
    const st = redFamilyView({ data: res({ primary: 'grpc', grpc: { id: 'grpc_stable', kind: 'grpc' } }) });
    expect(pickFamilySection(st).family).toBe(GRPC_STABLE_FAMILY);
  });
  it('ikisi → HTTP önce, gRPC altta; birincil HTTP; rozet +gRPC; ?family=grpc gRPC bölümünü seçer', () => {
    const v = redFamilyView({ data: res({ primary: 'http', both: true, http: { id: 'http', kind: 'http' }, grpc: { id: 'grpc_legacy', kind: 'grpc' } }) });
    expect(v.both).toBe(true);
    expect(familySections(v).map(s => s.family.id)).toEqual(['http', 'grpc_legacy']);
    expect(pickFamilySection(v).family.id).toBe('http');
    expect(pickFamilySection(v, 'grpc').family.id).toBe('grpc_legacy');
    expect(pickFamilySection(v, 'http').family.id).toBe('http');
    expect(familyBadgeSuffix(v)).toBe(' · +gRPC');
  });
  it('hiçbiri → primary none, bölüm HTTP (boş durum dürüst), tried taşınır; ?family=grpc yokken legacy varsayılanı', () => {
    const v = redFamilyView({ data: res({ primary: 'none', tried: ['http_server_request_duration_seconds_count: metrik yok (son 24 sa)'] }) });
    expect(v.primary).toBe('none');
    expect(familySections(v).map(s => s.family.id)).toEqual(['http']);
    expect(v.tried).toHaveLength(1);
    expect(familyBadgeSuffix(v)).toBe('');
    expect(pickFamilySection(v, 'grpc').family).toBe(GRPC_LEGACY_FAMILY);
  });
  it('tried null (Go nil) → []', () => {
    expect(redFamilyView({ data: res({ tried: null }) }).tried).toEqual([]);
  });
});

describe('api.serviceRedFamily yolu', () => {
  it('/api/services/{name}/red-family + metricsrc=vm; boş env/cluster gönderilmez', async () => {
    const { api } = await vi.importActual<typeof import('./api')>('./api');
    const urls: string[] = [];
    const orig = globalThis.fetch;
    globalThis.fetch = (async (u: RequestInfo | URL) => {
      urls.push(String(u));
      return new Response('{"service":"grpc.example.test","state":"known","primary":"grpc","grpc":{"id":"grpc_legacy","kind":"grpc"},"tried":[]}', { status: 200, headers: { 'Content-Type': 'application/json' } });
    }) as typeof fetch;
    try {
      const r = await api.serviceRedFamily('grpc.example.test', { env: 'eu-a', cluster: undefined, to: 1_800_000_000_000_000_000 });
      expect(r.primary).toBe('grpc');
    } finally { globalThis.fetch = orig; }
    const u = new URL(urls[0], 'http://x');
    expect(u.pathname).toBe('/api/services/grpc.example.test/red-family');
    expect(u.searchParams.get('metricsrc')).toBe('vm');
    expect(u.searchParams.get('env')).toBe('eu-a');
    expect(u.searchParams.has('cluster')).toBe(false);
  });
});

// v0.10.1559 — kısmi çözüm: HTTP okunamadı (zaman aşımı) ama gRPC bulundu.
// İki bölüm çizilir, not yalnız kısmi okumayı söyler; "bilinemedi" DEĞİL.
describe('kısmi aile çözümü (v0.10.1559)', () => {
  it('HTTP okunamadı + gRPC var → iki bölüm + kısmi not', () => {
    const v = redFamilyView({ data: {
      service: 's', state: 'known', primary: 'http', both: true,
      reason: 'Bazı aileler okunamadı (okunamadı ≠ yok); okunanlar çizildi: http_server_request_duration_seconds_count: okunamadı (timeout) — yok DEĞİL',
      http: { id: 'http', kind: 'http' }, grpc: { id: 'grpc_legacy', kind: 'grpc' },
      tried: ['http_server_request_duration_seconds_count: okunamadı (timeout) — yok DEĞİL'],
    } });
    expect(familySections(v).map(s => s.family.kind)).toEqual(['http', 'grpc']);
    expect(familyUnknownNote(v)).toBeNull();
    expect(familyPartialNote(v)).toContain('okunamadı ≠ yok');
  });
});
