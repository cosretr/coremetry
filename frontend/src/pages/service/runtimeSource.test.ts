import { describe, it, expect } from 'vitest';
import { runtimeSourceKind, runtimeSourceTip } from './runtimeSource';

describe('runtime kart kaynak ipucu (follow-up 2026-11-21)', () => {
  it('ad biçiminden kaynak', () => {
    expect(runtimeSourceKind(['process_runtime_dotnet_gc_collections_count_total'])).toBe('vm-otel');
    expect(runtimeSourceKind(['process.runtime.dotnet.gc.collections.count'])).toBe('otel');
    expect(runtimeSourceKind(['hikaricp_connections_active'])).toBe('micrometer');
    expect(runtimeSourceKind(['jvm_memory_bytes_used'])).toBe('micrometer');
    expect(runtimeSourceKind(['jboss_datasource_active'])).toBe('jmx');
  });
  it('ipucu metni aile + kaynak, tekrar yok', () => {
    expect(runtimeSourceTip(['a_b', 'a_b', 'c_d'], '{m} · {s}')).toBe('a_b, c_d · VM OTel');
  });
});
