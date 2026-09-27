// v0.10.145 — entity pod'ları her kapıdan ÖNCE görünür (kaynak taraması).
// v0.10.149 — tek yer (Pods), ikinci pod listesi yok.
// v0.10.720 — entity tablosu + Thanos akordeonu TEK tabloya indi
// (ServicePodsTable). Kural aynı, biçimi değişti: entity satırları Thanos
// keşfinden BAĞIMSIZ hook'tan gelir ve birleşik kümeye girer; hiçbir Thanos
// boş-durumu entity satırlarını yutamaz (boş durum yalnız rows.length === 0
// iken); Infra sekmesi tabloyu mount etmez, Pods'a işaret eder.
// v0.10.973 — tablo standardı T12 (tarif P6): boş durum artık tablonun
// İÇİNDE (`state={podsState}`, "Eşleşen pod yok"). Aynı niyet yeni biçimde
// çivili: tablo her durumda bağlanır, dt birleşik kümeden (rows) kurulur ve
// durum satırı YALNIZ o küme boşken çizilir — Thanos'un kendi boşluğu
// (th.rows / th.matched) tabloyu ya da entity satırlarını gizleyemez.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const read = (f: string) => readFileSync(join(__dirname, f), 'utf8');
const strip = (s: string) => s.replace(/^\s*\/\/.*$/gm, '').replace(/\{\/\*[\s\S]*?\*\/\}/g, '');

describe('Pods tek tablo — entity erişilebilirliği (v0.10.145 → 149 → 720 → 972)', () => {
  it('Infra tab does NOT mount the pods table (it lives in the Pods tab only) but points at it', () => {
    const src = strip(read('ServiceInfraTab.tsx'));
    expect(src).not.toContain('<ServicePodsTable');
    expect(src).not.toContain('<ServiceEntityPods');
    expect(src).toMatch(/entityHint/);
    expect(src).toMatch(/tab: 'pods'/);
  });
  it("Pods tab: entity hook Thanos'tan bağımsız, birleşik küme, boş durum yalnız birleşik küme boşken", () => {
    const src = strip(read('ServicePodsTab.tsx'));
    const tbl = strip(read('ServicePodsTable.tsx'));
    expect(src).toContain("useEntityServicePods(service, '', win, entityEnabled)");
    expect(src).toContain('mergePods(entityEnabled ? (entityQ.data?.pods ?? []) : [], th.rows, nameOf)');
    // dt birleşik (ve kaynak süzgeçli) kümeden kurulur; tablonun satırları o.
    expect(src).toContain('const rows = useMemo(() => filterBySource(merged, source), [merged, source]);');
    expect(src).toMatch(/useDataTable<MergedPodRow>\(\{\s*storageKey: 'service-pods-v2', columns: POD_COLS, rows,/);
    // Boş durum tablonun içinde, eski "No pods matched" kutusunun yerine.
    expect(src).toContain("message: 'Eşleşen pod yok'");
    expect(src).not.toContain('title="No pods matched"');
    // Tablo HER durumda bağlanır: hiçbir satır-sayısı / bekleme koşulu onu
    // bir kutuyla değiştirmez; durum `state` prop'uyla içeri girer.
    expect(src).toMatch(/<ServicePodsTable dt=\{dt\}[^>]*state=\{podsState\}/);
    expect(src).not.toMatch(/\?\s*<Spinner|\?\s*\(\s*<ServicePodsTable|:\s*\(\s*<ServicePodsTable/);
    expect(src).not.toContain('rows.length === 0 ? (');
    // Thanos'un kendi boşluğu tabloyu ya da durumu seçmez.
    expect(src).not.toMatch(/th\.(rows|matched)\.length === 0 \?/);
    // Tablo tarafı: durum satırı YALNIZ birleşik küme boşken; satırlar kazanır.
    expect(tbl).toContain('{dt.sortedRows.length === 0 && <DataTableState dt={dt} {...(state ?? { kind: \'empty\' })} />}');
    // Eski iki liste ve yapışkan şerit yok.
    expect(src).not.toContain('ServiceEntityPods');
    expect(src).not.toContain('ServiceClusterPods');
    expect(src).not.toContain("position: 'sticky'");
  });
  it('tablo: yapışık Pod sütunu, kaynak rozeti, ?jpod tüketimi, grup başlığı eylemleri', () => {
    const tab = read('ServicePodsTab.tsx');
    const tbl = read('ServicePodsTable.tsx');
    expect(tab).toContain('stickyLeft: true');
    expect(tab).toContain("storageKey: 'service-pods-v2'");
    expect(tbl).toContain("next.delete('jpod')");
    expect(tbl).toContain('className="badge b-gray" title={src.title}');
    expect(tbl).toContain('Infra →');
    expect(tbl).toContain('/pod →');
  });
});
