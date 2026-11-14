// PromQLList — etiket + <pre> sorgu listesi (README'nin PromQL kart
// idiomu; v0.9.51'de Clusters.tsx'ten çıkarıldı). Overview kartı,
// pod drawer'ı (v0.9.40) ve Service→Infra sekmesi (§8) paylaşır.
// Değerler çağıran tarafta promQuote ile kaçışlanır — display-only.
export function PromQLList({ queries }: { queries: [string, string][] }) {
  return (
    <div className="cl-promql">
      {queries.map(([label, q]) => (
        <div key={label}>
          <div className="cl-promql__label">{label}</div>
          <pre className="cl-promql__q">{q}</pre>
        </div>
      ))}
    </div>
  );
}
