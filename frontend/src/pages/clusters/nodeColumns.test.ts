import { describe, it, expect } from 'vitest';
import type { ClusterNodeRow } from '@/lib/types';
import {
  NODE_COLS, NODE_DEFAULT_HIDDEN, nodeStatus, nodePodsText, nodeCpuText, nodeMemText,
  nodeRequestText, nodeAllocText,
} from './nodeColumns';

// Clusters S6 (plan §0 D3): Nodes tablosu kolon seti, durum sözcüğü,
// kapasitesiz node hücresi ve "dikkat isteyenler önce" sıralaması.
const GiB = 1024 ** 3;
const row = (o: Partial<ClusterNodeRow>): ClusterNodeRow => ({ cluster: 'c1', node: 'n', cpuCores: 2, memBytes: 8 * GiB, ...o });

describe('Nodes kolonları (D3)', () => {
  it('kolon kimlikleri ve varsayılan görünür set sabit', () => {
    expect(NODE_COLS.map(c => c.id)).toEqual(
      ['node', 'role', 'status', 'pods', 'cpu', 'mem', 'cluster', 'alloc', 'reqPct', 'limits', 'netIn', 'netOut', 'versions']);
    expect(NODE_COLS.map(c => c.id).filter(id => !NODE_DEFAULT_HIDDEN.includes(id)))
      .toEqual(['node', 'role', 'status', 'pods', 'cpu', 'mem']);
  });

  it('durum tek sözcük: NotReady > baskı > Unknown > Ready; seri yoksa boş', () => {
    expect(nodeStatus({ ready: 'false', pressure: ['MemoryPressure'] }).word).toBe('NotReady');
    expect(nodeStatus({ ready: 'true', pressure: ['DiskPressure', 'MemoryPressure'] })).toMatchObject({
      word: 'Bellek baskısı', tone: 'warn', attention: true, title: 'Disk baskısı, Bellek baskısı' });
    expect(nodeStatus({ ready: 'true', pressure: ['PIDPressure'] }).word).toBe('PID baskısı');
    expect(nodeStatus({ ready: 'unknown' }).word).toBe('Unknown');
    expect(nodeStatus({ ready: 'true' })).toMatchObject({ word: 'Ready', attention: false, tone: undefined });
    expect(nodeStatus({}).word).toBe('');
  });

  it('kapasiteli hücre "kullanılan / kapasite · %", kapasitesiz yalnız kullanılan', () => {
    const full = row({ cpuCapacityCores: 8, memCapacityBytes: 32 * GiB, pods: 40, podCapacity: 250 });
    expect(nodeCpuText(full)).toBe('2 / 8 çekirdek · %25');
    expect(nodeMemText(full)).toBe('8.00 / 32.0 GiB · %25');
    expect(nodePodsText(full)).toBe('40 / 250');
    const bare = row({ pods: 40 });
    expect(nodeCpuText(bare)).toBe('2 çekirdek');
    expect(nodeMemText(bare)).toBe('8.00 GiB');
    expect(nodePodsText(bare)).toBe('40');
    expect(nodePodsText(row({}))).toBe('');
    expect(nodeAllocText(bare)).toBe('');
    expect(nodeRequestText(bare)).toBe('');
  });

  it('istek / ayrılabilir %100 üstünü kırpmaz (aşırı taahhüt)', () => {
    expect(nodeRequestText(row({ cpuRequestCores: 6, cpuAllocatableCores: 4, memRequestBytes: 4 * GiB, memAllocatableBytes: 16 * GiB })))
      .toBe('CPU %150 · Mem %25');
  });

  it('Status sıralama değeri: dikkat isteyen node bellek %si düşük olsa da önde', () => {
    const status = NODE_COLS.find(c => c.id === 'status')!;
    const hot = row({ ready: 'true', memCapacityBytes: 9 * GiB });
    const pressured = row({ ready: 'true', pressure: ['DiskPressure'], memCapacityBytes: 80 * GiB });
    expect(status.sortValue!(pressured) as number).toBeGreaterThan(status.sortValue!(hot) as number);
  });
});
