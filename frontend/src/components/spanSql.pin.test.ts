// v0.10.1955 (operatör: "db sorguları Toad gibi highlight edilebilir mi") — span detayında DB
// sorgusu coremetry/SqlCode ile renkli + kopyala; metin dbStatement > db.statement > db.query.text.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const src = readFileSync(resolve(__dirname, 'SpanDetail.tsx'), 'utf8');

describe('span detayı renkli SQL', () => {
  it('SqlCode bileşeni, öznitelik kolonlarından önce', () => {
    expect(src).toContain("import { SqlCode } from '@/components/coremetry/SqlCode'");
    const sql = src.indexOf('data-testid="span-sql"'), cols = src.indexOf('<div className="kiosk-span__cols">');
    expect(sql).toBeGreaterThan(0);
    expect(sql).toBeLessThan(cols);
  });
  it('metin önceliği: sunucunun dbStatement alanı, sonra eski ve yeni semconv özniteliği', () => {
    expect(src).toMatch(/span\.dbStatement \|\| span\.attributes\?\.\['db\.statement'\] \|\| span\.attributes\?\.\['db\.query\.text'\]/);
  });
});
