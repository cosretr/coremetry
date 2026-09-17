// inboxTab.test.ts — v0.10.751 Exceptions Inbox = ignored hariç her durum.
// Saf ayrıştırıcı + sayfanın sekmeleri tek kaynaktan çizip sunucuya sekme
// anahtarını olduğu gibi (state=inbox) gönderdiğinin pini.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { EXCEPTION_TABS, resolveExceptionTab } from './tabs';

describe('exception tabs', () => {
  it('Inbox ilk sekme, anahtarı inbox; Ignored ayrı sekme', () => {
    expect(EXCEPTION_TABS[0]).toMatchObject({ key: 'inbox', label: 'Inbox' });
    expect(EXCEPTION_TABS[0].hint).toContain('resolved');
    expect(EXCEPTION_TABS.map(t => t.key)).toEqual(['inbox', 'new', 'acknowledged', 'regressed', 'resolved', 'ignored']);
  });

  it('resolveExceptionTab: boş/bilinmeyen → inbox; eski open → inbox; bilinen aynen', () => {
    expect(resolveExceptionTab(null)).toBe('inbox');
    expect(resolveExceptionTab('')).toBe('inbox');
    expect(resolveExceptionTab('open')).toBe('inbox');
    expect(resolveExceptionTab('garip')).toBe('inbox');
    expect(resolveExceptionTab('resolved')).toBe('resolved');
    expect(resolveExceptionTab('ignored')).toBe('ignored');
  });
});

describe('AnomaliesPage kablolaması', () => {
  const src = readFileSync(resolve(__dirname, 'AnomaliesPage.tsx'), 'utf8');
  it('sekmeler tabs.ts\'ten, URL ayrıştırması resolveExceptionTab, sunucuya state: tab', () => {
    expect(src).toContain("from './tabs'");
    expect(src).toContain('resolveExceptionTab(searchParams.get(\'tab\'))');
    expect(src).toContain('state: tab,');
    expect(src).not.toContain("|| 'open'");
    expect(src).not.toMatch(/const TABS\b/);
  });
});
