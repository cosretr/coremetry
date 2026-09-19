// v0.10.803 — stdio MCP sunucusu ortamı: KEY=VALUE alanı, saklı değer sentineli.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { parseEnvText } from './mcpEnv';

describe('v0.10.803 — MCP stdio env', () => {
  it('parseEnvText: KEY=VALUE satırları, boş/# atlanır, = içeren değer korunur', () => {
    expect(parseEnvText('A=1\n# yorum\n\nB=x=y\nbad\n =z')).toEqual({ A: '1', B: 'x=y' });
    expect(parseEnvText('')).toEqual({});
  });
  it('form: snapshot envKeys → ******** satırları; PUT env yalnız stdio ve dolu alanla', () => {
    const src = readFileSync(resolve(__dirname, './McpServersTab.tsx'), 'utf8');
    expect(src).toContain("envText: (sv.envKeys || []).map(k => `${k}=${SECRET_KEPT}`).join('\\n')");
    expect(src).toContain("env: r.transport === 'stdio' && r.envText.trim() ? parseEnvText(r.envText) : undefined");
    expect(src).toContain('<SettingRow label="Ortam"');
  });
  it('types: envKeys on snapshot, env on input', () => {
    const t = readFileSync(resolve(__dirname, '../../lib/types.ts'), 'utf8');
    expect(t).toContain('envKeys?: string[];');
    expect(t).toContain('env?: Record<string, string>;');
  });
});
