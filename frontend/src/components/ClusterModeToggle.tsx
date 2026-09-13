// ClusterModeToggle — v0.10.717 (multi-cluster ilkesi). Bölüm başlığındaki
// "birleşik | cluster başına" parçalı anahtarı; DetailsEndpointsSection'ın
// seg-mini deseni tek bileşene alındı (üç bölüm aynı anatomi). Kapsam tek
// cluster'a daralmışsa çağıran çizmez. Ham <button>: .seg-mini primitif
// gövdesi (tab-strip sınıfı istisnası).
export function ClusterModeToggle({ value, onChange, label }: {
  value: boolean; onChange: (byCluster: boolean) => void; label: string;
}) {
  return (
    <span className="seg-mini" role="group" aria-label={label}>
      <button type="button" className={value ? '' : 'on'} onClick={() => onChange(false)}>birleşik</button>
      <button type="button" className={value ? 'on' : ''} onClick={() => onChange(true)}
        title="Her cluster ayrı okunur (cluster süzgeci MV'yi diskalifiye eder → ham yol)">cluster başına</button>
    </span>
  );
}
