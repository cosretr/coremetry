// v0.10.796 — incident P rozeti liste + detayda (Inbox ile aynı merdiven).
// "Kural görünür değilse yoktur" (v0.10.364): sunucu alanı gönderiyor, iki
// sayfa da çiziyor — bu pin bunu çiviler.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');

describe('v0.10.796 — incident priority badge', () => {
  it('Incidents list has a Priority column rendered with PriorityBadge', () => {
    const src = read('./Incidents.tsx');
    expect(src).toContain("{ id: 'priority', label: 'Priority', sortValue: i => priorityRank(i.priority)");
    expect(src).toContain('<PriorityBadge p={i.priority} reason={i.priorityReason} />');
    // Sütun tanımı ile hücre sırası aynı: status → priority → severity.
    const colOrder = src.indexOf("id: 'priority'") < src.indexOf("id: 'severity'");
    const cellOrder = src.indexOf('<PriorityBadge p={i.priority}') < src.indexOf('<SeverityPill s={i.severity} />');
    expect(colOrder && cellOrder).toBe(true);
  });
  it('Incident detail bar shows the badge next to severity', () => {
    const src = read('./Incident.tsx');
    expect(src).toContain('{inc.priority && <PriorityBadge p={inc.priority} reason={inc.priorityReason} />}');
  });
  it('Incident type carries priority + reason from the server', () => {
    const t = read('../lib/types.ts');
    expect(t).toContain("priority?: 'P1' | 'P2' | 'P3';\n  priorityReason?: string;\n}");
  });
});
