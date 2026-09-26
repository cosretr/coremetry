// rowHeight — v0.10.933 (tablo standardı T6): evin satır ritmi TEK yerde.
//
// 36 üç ayrı yerde elle yazılıydı (VirtualTable varsayılanı, Traces
// `rowHeight`, globals.css `.row-link` yüksekliği). CSS tarafı artık
// `--row-h` token'ı; rahat (varsayılan) yoğunluktaki değeri bu sabitle
// EŞİT olmak zorunda — virtualizer satırı JS'te sabit tahmin eder, CSS
// satırı çizer; ikisi ayrışırsa kaydırma çubuğu zıplar (rowHeight.test).
export const ROW_H = 36;
