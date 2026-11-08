// v0.10.1984 — servis Logs sekmesinde satır tıkı satır içi açılır (çekmece yalnız ?doc=); /logs 1970 ile aynı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'CompactLogSearch.tsx'), 'utf8');

describe('CompactLogSearch satır içi', () => {
  it('satır tıkı genişlemeyi aç/kapatır, çekmeceyi açmaz', () => {
    expect(src).toContain('expandedIds={expanded}');
    expect(src).toContain('onToggleExpand={toggleRow}');
    expect(src).not.toContain('NO_EXPANDED');
  });
});
