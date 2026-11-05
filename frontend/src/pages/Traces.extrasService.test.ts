// v0.10.1806 — the extras (attribute-column) request carries the list's
// service filter, so the server can rank that service's entry span right
// after the root (v0.10.1805: root > service entry > earliest). Pinned on
// the source: a refactor that drops the param silently reverts rank 2.
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const src = readFileSync(new URL('./Traces.tsx', import.meta.url), 'utf8');

describe('Traces extras request carries service', () => {
  it('passes filter.service to api.tracesExtras', () => {
    const call = src.slice(src.indexOf('api.tracesExtras({'));
    const body = call.slice(0, call.indexOf('}).then('));
    expect(body).toContain('service: filter.service || undefined');
  });
  it('lists filter.service in the effect deps', () => {
    expect(src).toContain('}, [view, data, extraCols, filter.service]);');
  });
});
