import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1429 — Overview metrik kipi RED ailesi (yalnız gRPC sunan servis): rozet
// "kaynak: metrik (vm) · gRPC", /metric-red + /metric-throughput aile parametresi,
// route kırılımı rpc.method; çözüm gelene dek metrik sorgusu yok; ikisi → HTTP + not.
const src = readFileSync(resolve(__dirname, 'Overview.tsx'), 'utf8');

describe('Overview RED ailesi (v0.10.1429)', () => {
  it('aile çözümü metrik sorgularından önce; rozet eki; gRPC/both rozetleri', () => {
    expect(src).toContain("const fam = useRedFamily(service, env, '', to, !!service && wantMetric);");
    expect(src).toContain('const famSec = pickFamilySection(fam);');
    expect(src).toContain("const famOpt = famGrpc ? { family: 'grpc' as const } : {};");
    expect(src).toContain('enabled: !!service && wantMetric && fam.ready,');
    expect(src).toContain("`kaynak: metrik (${metricRedQ.data?.source ?? '…'})${familyBadgeSuffix(fam)}`");
    expect(src).toContain('{metricMode && famGrpc && (');
    expect(src).toContain('{metricMode && fam.both && (');
    expect(src).toContain('gRPC de var → HTTP/GRPC');
  });
  it('throughput kimliği ve kırılım aileden; Response time by rpc.method', () => {
    expect(src).toContain("{ breakdown: famGrpc ? 'rpc_method' : 'route', rateWindow: 180, env: env || undefined, ...famOpt }");
    expect(src).toContain("const routeDim = famGrpc ? 'rpc.method' : 'http.route';");
    expect(src).toContain('groupBy: routeDim,');
    expect(src).toContain("title={`Response time · avg (by ${famGrpc ? 'method' : 'route'})`}");
    expect(src).toContain("statusCodeSeries(httpStatusQ.data, family)");
    expect(src).toContain("totalRequestsQuery(service, Math.max(60, (to - from) / 1e9), envSel, family)");
  });
});
