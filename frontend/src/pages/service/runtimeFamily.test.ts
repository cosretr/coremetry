import { describe, it, expect } from 'vitest';
import { familyFromResource, familyFromProfile } from './runtimeFamily';
import { familyOf } from './RuntimeCharts';
import { plannedCards } from './DotnetRuntimePanel';

describe('familyFromResource', () => {
  it('language first, then process.runtime.name', () => {
    expect(familyFromResource('dotnet')).toBe('dotnet');
    expect(familyFromResource('CSharp')).toBe('dotnet');
    expect(familyFromResource('java')).toBe('jvm');
    expect(familyFromResource('nodejs')).toBe('nodejs');
    expect(familyFromResource(undefined, '.NET')).toBe('dotnet');
    expect(familyFromResource('', 'OpenJDK Runtime Environment')).toBe('jvm');
    expect(familyFromResource('', 'go')).toBe('go');
    expect(familyFromResource('', '')).toBeNull();
  });
  it('profile runtime', () => {
    expect(familyFromProfile('dotnet')).toBe('dotnet');
    expect(familyFromProfile('')).toBeNull();
  });
  it('familyOf keeps only chart families', () => {
    expect(familyOf('kotlin')).toBe('jvm');
    expect(familyOf('csharp')).toBe('dotnet');
    expect(familyOf('nodejs')).toBeNull();
  });
});

describe('plannedCards', () => {
  it('draws only cards whose signal resolved; gen breakdown falls back to total', () => {
    const cards = plannedCards([
      { signal: 'gc_collections', metric: 'dotnet_gc_collections_total', kind: 'counter', agg: 'rate', scale: 1 },
      { signal: 'exceptions', metric: 'dotnet_exceptions_total', kind: 'counter', agg: 'rate', scale: 1 },
    ]);
    expect(cards.map(c => c.card.key)).toEqual(['gcgen', 'exc']);
    expect(cards[0].lines[0].line.group).toBe('none');
  });
});
