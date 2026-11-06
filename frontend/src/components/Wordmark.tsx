// Coremetry wordmark — the "Core" segment renders in the corporate
// red (--brand, #E30613) per operator request (v0.8.107). Custom
// appName brandings render verbatim — only the stock name gets the
// two-tone treatment.
//
// v0.10.1912 (operatör-raporlu: login başlığında "Core" ile "metry" ayrı
// duruyordu) — kök TEK <span>. Fragment dönerken "Core" span'i ile "metry"
// metni, flex / grid bir ebeveynin (login h1: `flex gap-2`) AYRI öğeleri
// oluyor ve araya `gap` giriyordu. Tek kök → ad hiçbir ebeveynde bölünmez.
export function Wordmark({ name = 'Coremetry' }: { name?: string }) {
  if (name !== 'Coremetry') return <span>{name}</span>;
  return (
    <span>
      <span style={{ color: 'var(--brand)' }}>Core</span>metry
    </span>
  );
}
