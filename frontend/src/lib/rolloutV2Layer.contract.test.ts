// rolloutV2Layer.contract.test.ts — v0.10.960 (Rollouts v2 P1.8, inceleme F2).
//
// 0015 sihirbazının FE şekilleri Go'nun ve gömülü migration'ın AYNASI; ikisi
// ayrı dillerde yazıldığı için kayma sessiz olur (alan adı değişir, TS tipi
// eski adı okur → kart "undefined" basar). Bu dosya kaynağı OKUYARAK çiviler
// (minOccDefault.test.ts emsali — Go kaynağını okuyan FE testi):
//
//   1. ROLLOUT_V2_TABLES = migrations/0015_rollouts_v2.sql'in CREATE sırası.
//      Kart durum listesini BU adlarla 0012 / 0015 diye böler; bir tablo
//      eklenip listeye girmezse 0012 rozetine sayılır ve F2 geri gelir.
//      (0015 ↔ rollout_v2_schema.go bayt eşitliğini Go testi pinler.)
//   2. RolloutV2LayerPreflightResult alanları = Go struct json etiketleri;
//      omitempty ↔ TS'te `?:` (derleme + çalışma zamanı), tip ↔ tip.
//      v0.10.975 — installed + missing ("kurulu" hükmü) eklendi.
//   3. apply-0015 / rollback-0015 cevap anahtarları ve rollback'in okuduğu
//      gövde alanları (cluster + confirm) + üç rota.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  ROLLOUT_V2_TABLES,
  type RolloutV2LayerPreflightResult, type RolloutV2LayerApplyResult, type RollupActionResult,
} from './types';

const REPO = resolve(__dirname, '../../..');
const read = (rel: string) => readFileSync(resolve(REPO, rel), 'utf8');
const CHSTORE = read('internal/chstore/rollout_layer_admin.go');
const HANDLER = read('internal/api/admin_rollout_layer.go');
const SQL = read('migrations/0015_rollouts_v2.sql');

/** Go fonksiyon gövdesi: `func … name(` satırından bir sonraki üst düzey `}`'e. */
function goFunc(src: string, name: string): string {
  const at = src.search(new RegExp(`^func (\\([^)]*\\) )?${name}\\(`, 'm'));
  expect(at, `${name} Go kaynağında yok`).toBeGreaterThanOrEqual(0);
  const end = src.indexOf('\n}\n', at);
  return src.slice(at, end < 0 ? undefined : end);
}

/** `writeJSON(w, map[string]any{…})` anahtarları. */
function writeJSONKeys(body: string): string[] {
  const at = body.indexOf('writeJSON(w, map[string]any{');
  expect(at, 'writeJSON(map) yok').toBeGreaterThanOrEqual(0);
  const lit = body.slice(at, body.indexOf('})', at));
  return [...lit.matchAll(/"(\w+)":/g)].map(m => m[1]).sort();
}

// TS'te `?:` olan anahtarlar (derleme zamanı).
type OptionalKeys<T> = { [K in keyof T]-?: {} extends Pick<T, K> ? K : never }[keyof T];

describe('0015 durum grubu adları (v0.10.960)', () => {
  it('ROLLOUT_V2_TABLES = 0015 migration CREATE sırası', () => {
    const created = [...SQL.matchAll(/^CREATE TABLE IF NOT EXISTS (\w+) ON CLUSTER /gm)].map(m => m[1]);
    expect(created).toHaveLength(8);
    expect([...ROLLOUT_V2_TABLES]).toEqual(created);
  });

  it('adlar 0012 nesneleriyle çakışmaz (bölme belirsiz kalmasın)', () => {
    const v1 = goFunc(CHSTORE, 'rolloutLayer0012Objects');
    const names0012 = [...v1.matchAll(/Name: "(\w+)"/g)].map(m => m[1]);
    expect(names0012).toHaveLength(17);
    for (const t of ROLLOUT_V2_TABLES) expect(names0012).not.toContain(t);
  });
});

describe('RolloutV2LayerPreflightResult ↔ chstore struct (v0.10.960)', () => {
  const block = CHSTORE.match(/type RolloutV2LayerPreflightResult struct \{([\s\S]*?)\n\}/)?.[1] ?? '';
  const goFields = [...block.matchAll(/^\s*\w+\s+(\[\]string|string|bool|int64)\s+`json:"([^"]+)"`/gm)]
    .map(m => ({ type: m[1], tag: m[2].split(',')[0], omitempty: m[2].includes(',omitempty') }));

  // Her alan dolu örnek: tsc eksik/fazla anahtarı ve yanlış tipi reddeder.
  const full: Required<RolloutV2LayerPreflightResult> = {
    clusters: ['uptrace_all'], suggestedCluster: 'uptrace_all', cluster: 'uptrace_all',
    spansLocal: true, bootManaged: false, conflicts: [], probeErrors: [],
    supported: true, installed: false, missing: [], detail: 'ok', generated: 1,
  };
  const optional: Record<OptionalKeys<RolloutV2LayerPreflightResult>, true> = { suggestedCluster: true, probeErrors: true };

  // v0.10.975 — +installed (bool) +missing ([]string, omitempty DEĞİL: FE .length).
  it('struct okunabildi', () => {
    expect(goFields.length).toBe(12);
    expect(goFields.filter(f => f.tag === 'installed' || f.tag === 'missing'))
      .toEqual([{ type: 'bool', tag: 'installed', omitempty: false }, { type: '[]string', tag: 'missing', omitempty: false }]);
  });

  it('alan adları birebir', () => {
    expect(Object.keys(full).sort()).toEqual(goFields.map(f => f.tag).sort());
  });

  it('omitempty ↔ TS opsiyonel', () => {
    expect(Object.keys(optional).sort()).toEqual(goFields.filter(f => f.omitempty).map(f => f.tag).sort());
  });

  it('tipler eşleşir', () => {
    const tsType = (v: unknown) => (Array.isArray(v) ? '[]string' : typeof v);
    const goType: Record<string, string> = { '[]string': '[]string', string: 'string', bool: 'boolean', int64: 'number' };
    for (const f of goFields) {
      expect(tsType(full[f.tag as keyof typeof full]), f.tag).toBe(goType[f.type]);
    }
  });
});

describe('apply-0015 / rollback-0015 HTTP sözleşmesi (v0.10.960)', () => {
  it('üç rota Go\'da kayıtlı (yöntem + yol)', () => {
    expect(HANDLER).toContain('"GET /api/admin/rollout-layer/preflight-0015"');
    expect(HANDLER).toContain('"POST /api/admin/rollout-layer/apply-0015"');
    expect(HANDLER).toContain('"POST /api/admin/rollout-layer/rollback-0015"');
  });

  it('preflight-0015 kümeyi ?cluster= sorgusundan okur', () => {
    expect(goFunc(HANDLER, 'getRolloutV2LayerPreflight')).toContain('r.URL.Query().Get("cluster")');
  });

  it('apply-0015 cevabı = RolloutV2LayerApplyResult', () => {
    const full: Required<RolloutV2LayerApplyResult> = { statements: [], ok: true, note: '' };
    expect(writeJSONKeys(goFunc(HANDLER, 'postRolloutV2LayerApply'))).toEqual(Object.keys(full).sort());
  });

  it('rollback-0015 cevabı = RollupActionResult; gövde cluster + confirm okur', () => {
    const body = goFunc(HANDLER, 'postRolloutV2LayerRollback');
    const full: Required<RollupActionResult> = { statements: [], ok: true };
    expect(writeJSONKeys(body)).toEqual(Object.keys(full).sort());
    expect(body).toContain('`json:"cluster"`');
    expect(body).toContain('`json:"confirm"`');
    expect(body).toMatch(/if !in\.Confirm \{/);
  });
});
