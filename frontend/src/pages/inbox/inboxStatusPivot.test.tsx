// @vitest-environment jsdom
//
// v0.10.2476 (operatör: "problemler kapanınca ayrıca gözüksün aktifler
// arasında kalmasın") — Problems durum pivotu: Açık · Kapanan · Susturulan ·
// Tümü; ?status=closed codec'i; Kapanan satırının zaman hücresi.
import { describe, it, expect, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

vi.mock('uplot', () => ({ default: function UPlot() {} })); // InboxCells → ProblemDetail zinciri
import { renderToStaticMarkup } from 'react-dom/server';
import { STATUS_PIVOTS, STATUS_LABEL, INBOX_STATUS_CODEC } from './inboxFilters';
import { ClosedWhen } from './InboxCells';
import { inboxCols } from './inboxColumns';
import { t } from '@/lib/i18n';
import { inboxStatusHref } from '@/lib/inboxUrl';

describe('Problems durum pivotu', () => {
  it('sıra Açık · Kapanan · Susturulan · Tümü, iki dilde', () => {
    expect(STATUS_PIVOTS).toEqual(['open', 'closed', 'ignored', 'all']);
    expect(STATUS_PIVOTS.map(s => t(STATUS_LABEL[s], 'tr'))).toEqual(['Açık', 'Kapanan', 'Susturulan', 'Tümü']);
    expect(STATUS_PIVOTS.map(s => t(STATUS_LABEL[s], 'en'))).toEqual(['Open', 'Closed', 'Ignored', 'All']);
  });

  it('?status codec: closed okunur/yazılır, varsayılan ve bozuk değer açık', () => {
    expect(INBOX_STATUS_CODEC.decode('closed')).toBe('closed');
    expect(INBOX_STATUS_CODEC.decode(null)).toBe('open');
    expect(INBOX_STATUS_CODEC.decode('resolved')).toBe('open');
    expect(INBOX_STATUS_CODEC.encode('closed')).toBe('closed');
    expect(INBOX_STATUS_CODEC.encode('open')).toBe(''); // varsayılan → param silinir
  });

  it('sayfa altı "Kapananlar" linki Kapanan görünümünü açar', () => {
    const u = new URL(inboxStatusHref(new URLSearchParams('q=x&problem=p1'), 'closed'), 'http://x');
    expect(u.searchParams.get('status')).toBe('closed');
    expect(u.searchParams.has('problem')).toBe(false);
    const page = readFileSync(join(__dirname, '..', 'Inbox.tsx'), 'utf8');
    expect(page).toContain("inboxStatusHref(searchParams, 'closed')");
    // pivot ana çubukta, codec'ten okunur ve sunucuya gider (anahtar = filter nesnesi)
    expect(page).toContain("INBOX_STATUS_CODEC.decode(searchParams.get('status'))");
    expect(page).toContain('status: statusFilter,');
  });

  it('Kapanan görünümünde zaman kolonu "Kapandı"', () => {
    expect(inboxCols('tr', true).find(c => c.id === 'firstSeen')?.label).toBe('Kapandı');
    expect(inboxCols('tr').find(c => c.id === 'firstSeen')?.label).toBe('Ne zamandan beri');
  });

  it('kapanış anı + süre çizilir', () => {
    const start = 1_800_000_000e9;
    const html = renderToStaticMarkup(<ClosedWhen startedAt={start} resolvedAt={start + 47 * 60e9} lang="tr" />);
    expect(html).toContain('47 min sürdü') // v0.10.2484 — lib/format/duration;
    const en = renderToStaticMarkup(<ClosedWhen startedAt={start} resolvedAt={start + 2 * 3600e9} lang="en" />);
    expect(en).toContain('lasted 2 h');
  });
});
