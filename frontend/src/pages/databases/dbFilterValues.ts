// dbFilterValues — v0.10.2205: Databases başlık hunisinin SAF değer süzgeci.

const LIMIT = 50;

/** SAF: arama → görünen değerler (seçili değer her zaman görünür; en çok LIMIT). */
export function dbFilterValues(values: readonly string[], q: string, selected: string): { shown: string[]; more: number } {
  const t = q.trim().toLowerCase();
  const match = values.filter(v => !t || v.toLowerCase().includes(t));
  const head = match.slice(0, LIMIT);
  if (selected && !head.includes(selected) && values.includes(selected)) head.unshift(selected);
  return { shown: head, more: Math.max(0, match.length - LIMIT) };
}
