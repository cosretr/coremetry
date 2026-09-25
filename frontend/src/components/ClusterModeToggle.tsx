import { SegmentedControl } from '@/components/ui';

// ClusterModeToggle — v0.10.717 (multi-cluster ilkesi). Bölüm başlığındaki
// "birleşik | cluster başına" parçalı anahtarı; DetailsEndpointsSection'ın
// seg-mini deseni tek bileşene alındı (üç bölüm aynı anatomi). Kapsam tek
// cluster'a daralmışsa çağıran çizmez.
// v0.10.924 — buton bütünlüğü Faz 2: elle `.seg-mini` gövdesi yerine
// SegmentedControl (tek seçim → radiogroup + ←/→); yoğun rung `sm`.
export function ClusterModeToggle({ value, onChange, label }: {
  value: boolean; onChange: (byCluster: boolean) => void; label: string;
}) {
  return (
    <SegmentedControl size="sm" aria-label={label}
      value={value ? 'cluster' : 'combined'}
      onChange={v => onChange(v === 'cluster')}
      options={[
        { value: 'combined', label: 'birleşik' },
        { value: 'cluster', label: 'cluster başına',
          title: "Her cluster ayrı okunur (cluster süzgeci MV'yi diskalifiye eder → ham yol)" },
      ]} />
  );
}
