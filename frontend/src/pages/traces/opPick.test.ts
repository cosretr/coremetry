// opPick.test.ts — v0.10.752 operasyon seçimi TAM eşleşme + Name hücresi.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { opCellText, opCellTitle, opChipFor } from './opPick';

describe('opPick — saf', () => {
  it('opChipFor: boş → yok; dolu → operation = <ad> (span düzeyi tam eşleşme)', () => {
    expect(opChipFor('')).toEqual([]);
    expect(opChipFor('  ')).toEqual([]);
    expect(opChipFor('SELECT shop.orders')).toEqual([{ k: 'operation', op: '=', v: ['SELECT shop.orders'] }]);
  });
  it('Name hücresi: seçilen operasyon yazılır, kök farklıysa ipucunda', () => {
    expect(opCellText('', 'root-op')).toBe('root-op');
    expect(opCellText('', '')).toBe('—');
    expect(opCellText('SELECT x', 'grpc.Call')).toBe('SELECT x');
    expect(opCellTitle('SELECT x', 'grpc.Call')).toBe('Kök: grpc.Call');
    expect(opCellTitle('SELECT x', 'SELECT x')).toBe('SELECT x');
    expect(opCellTitle('', 'root-op')).toBe('root-op');
  });
});

describe('opPick — kablolama', () => {
  const traces = readFileSync(resolve(__dirname, '../Traces.tsx'), 'utf8');
  const picker = readFileSync(resolve(__dirname, '../../components/OperationPicker.tsx'), 'utf8');
  it('seçici seçimi onPick ile bildirir; sayfa op durumunu URL ve isteklere taşır', () => {
    expect(picker).toContain('onPick?: (v: string) => void');
    expect(picker).toContain('onPick(next)');
    expect(traces).toContain("op:       searchParams.get('op')");
    expect(traces).toContain("['op',       filter.op]");
    expect(traces).toContain('onPick={');
    // Liste + toplu + sayım: ÜÇ istek de etkin çipleri (op dahil) gönderir; şerit de.
    expect((traces.match(/JSON\.stringify\(advFiltersEff\)/g) ?? []).length).toBe(3);
    expect(traces).toContain('[...advFiltersEff]');
    expect(traces).toContain('opCellText(op, opDisplayName(t.rootName, t.rootRoute))'); // v0.10.756 gösterim adı
    expect(traces).toContain(', filter.op);'); // çağrı yeri seçili operasyonu geçirir
  });
  it('Service kolonu varsayılanı genişledi (operatör: "alan dar")', () => {
    const m = traces.match(/service: (\d+), operation: 210/);
    expect(m).not.toBeNull();
    expect(Number(m![1])).toBeGreaterThanOrEqual(190);
  });
});
