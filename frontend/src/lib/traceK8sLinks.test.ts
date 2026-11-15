// v0.10.143 — traces listesi K8s kolon seti (v0.10.2233: link kurulumu
// sunucuya taşındı — POST /api/entity/paths).
import { describe, it, expect } from 'vitest';
import { withK8sColumns, canAddK8sColumns } from './traceK8sLinks';

describe('traceK8sLinks', () => {
  it('withK8sColumns adds cluster FIRST (the one key every link needs), respects the 8-column cap, skips existing', () => {
    expect(withK8sColumns(['http.route', 'k8s.pod.name'])).toEqual(['http.route', 'k8s.pod.name', 'cluster', 'k8s.namespace.name', 'k8s.node.name']);
    expect(withK8sColumns(['a', 'b', 'c', 'd', 'e', 'f', 'g'])).toEqual(['a', 'b', 'c', 'd', 'e', 'f', 'g', 'cluster']);
    expect(canAddK8sColumns(['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h'])).toBe(false);
    expect(canAddK8sColumns(['cluster', 'k8s.namespace.name', 'k8s.pod.name', 'k8s.node.name'])).toBe(false);
    expect(canAddK8sColumns([])).toBe(true);
  });
});
