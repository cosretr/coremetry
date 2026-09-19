// v0.10.803 — stdio MCP sunucusu ortam alanı (SAF; bileşen dosyasından ayrı
// ki react-refresh yalnız bileşen dışa aktarsın).

// parseEnvText — "KEY=VALUE" satırları → nesne; boş/`#` satır atlanır, ilk
// `=` ayırıcıdır (değer `=` içerebilir), anahtar/değer kırpılır.
export function parseEnvText(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    const eq = line.indexOf('=');
    if (eq <= 0) continue;
    out[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
  }
  return out;
}
