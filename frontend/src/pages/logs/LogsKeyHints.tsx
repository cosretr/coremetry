// LogsKeyHints — v0.10.1857 (Log Search Faz 2 dilim 2.6; mockup görünüm 1
// tablo kartının alt satırı `.keys`). Klavye ipuçları tablonun ALTINDA, akışta
// (yüzen şerit değil — operatör kuralı). Saf sunum; bağlamalar Logs.tsx'te
// (useShortcuts, yalnız /logs bağlıyken). `?` yardım ekranı aynı kayıtlardan.
const HINTS: ReadonlyArray<[keys: string[], label: string]> = [
  [['/'], 'sorguya odak'],
  [['j', 'k'], 'satırlar'],
  [['Enter'], 'detay'],
  [['f'], 'filtrele'],
  [['e'], 'hariç tut'],
  [['t'], "trace'e git"],
];

export function LogsKeyHints() {
  return (
    <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[length:var(--fs-xs)] text-(--text3)"
      aria-label="Klavye kısayolları" data-testid="logs-key-hints">
      {HINTS.map(([keys, label]) => (
        <span key={label} className="inline-flex items-center gap-1">
          {keys.map(k => <kbd key={k} className="kbd">{k}</kbd>)}
          {label}
        </span>
      ))}
      <span>f / e / t seçili satır (j / k) üzerinde çalışır</span>
    </div>
  );
}
