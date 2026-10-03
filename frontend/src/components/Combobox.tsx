import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { IconButton } from '@/components/ui/IconButton';
import { PickerPopover, type PickerItem, type PickerStatus } from '@/components/ui/PickerPopover';
import { pickerOptionId } from '@/lib/pickerPopover';
import { getPickerRecents, recordPickerRecent } from '@/lib/pickerRecents';

/**
 * Free-text input with a custom dropdown panel that filters as you
 * type. Replaces the previous native <datalist> implementation —
 * datalist's browser-controlled rendering cuts off long option
 * strings when the input is narrow, which made operation /
 * peer-service pickers unreadable on /traces.
 *
 * v0.10.1089 — açılır liste artık `ui/PickerPopover`: body'ye portal edilen
 * kart yüzeyi (kırpılmaz, kenarda çevrilir), "N sonuç" başlığı, durumlar
 * (aranıyor… / eşleşme yok / arama başarısız), isteğe bağlı "Son
 * kullanılan" grubu (`recentKey`), `aria-activedescendant` ile klavye
 * satırı. Tüm seçiciler bu atomdan geçtiği için görünüm TEK yerde değişir.
 *
 * Behaviour:
 *   - Opens on focus or arrow click; closes on outside click / Esc.
 *   - Filters options by case-insensitive substring as the user
 *     types. The current input value is the source of truth — Enter
 *     keeps whatever's typed (so the user can submit a string that
 *     isn't in the suggestion list, e.g. a brand-new search term).
 *   - Arrow keys navigate, Enter picks (or fires onEnter if there's
 *     no active highlight), Tab picks and moves focus on.
 *   - Dropdown sizes to its content via CSS (min-width = input
 *     width, max-width capped) so long span names aren't truncated
 *     mid-word.
 *
 * v0.9.1022 — ÇIKIŞ SÖZLEŞMESİ (satır içi düzenleyici gereksinimi).
 * Atom bugüne dek yalnız BAĞIMSIZ bir alan olarak yaşadı: odağı hep
 * kullanıcı verirdi, kilitlenmezdi, ve alandan ÇIKIŞ yolu yoktu.
 * Satır içi bir düzenleyici (tabloda bir hücre, bir çip) bunların
 * dördünü de ister — `autoFocus` ile düzenleme moduna girer,
 * `disabled` ile kayıt sürerken kilitlenir, `onBlurCommit` ile
 * odaktan çıkınca yazılanı kaydeder, `onEscape` ile İPTAL eder.
 * Dördü de opsiyonel: hiçbiri verilmezse davranış birebir eskisi.
 */
export function Combobox({
  value, onChange, options, placeholder, width, onEnter,
  autoFocus, disabled, onBlurCommit, onEscape,
  serverFiltered, title, ariaLabel, className, shortcutSearch,
  footer, optionMeta, resultCount, status, recentKey,
}: {
  value: string;
  onChange: (v: string) => void;
  options: string[];
  placeholder?: string;
  width?: number | string;
  onEnter?: () => void;
  // Mount'ta odaklan ve metni SEÇ. Seçmek şart: satır içi düzenleyici
  // mevcut değerle açılır, operatör yazmaya başlayınca onu değiştirmek
  // ister — imleci sona koymak "sil sonra yaz" demek olurdu.
  autoFocus?: boolean;
  // Kilitli alan: yazılamaz, liste AÇILMAZ. Temizle (✕) düğmesi çalışır
  // KALIR — atıl/kilitli bir alanda bile bayat bir değeri bırakabilmek
  // tam olarak orada işe yarar (EnvPicker'ın atıl hâli bunu yaşıyor).
  disabled?: boolean;
  // Odaktan çıkışta O ANKİ yazılı değeri verir. Karar çağıranın:
  // kaydetmek (TeamEditor) ya da eski değere dönmek (EnvPicker).
  // Listeden seçim BLUR ÜRETMEZ (satırlar mousedown'da preventDefault
  // eder) — yani bu, "alanı bıraktı" olayının tek yorumu.
  onBlurCommit?: (value: string) => void;
  // Liste KAPALIYKEN Esc. Düzenleme modundan iptal-çıkış. Liste
  // açıkken ÇAĞRILMAZ: v0.9.1021 katman sözleşmesi (bir Esc bir
  // katman) — ilk Esc listeyi kapatır, ikincisi düzenleyiciden çıkar.
  onEscape?: () => void;

  // ——— v0.9.1024 · SUNUCU-TARAFLI picker'lar ———————————————————
  //
  // serverFiltered — `options` zaten `value` için SUNUCUDAN gelen
  // cevap. İstemci tarafında BİR DAHA süzmek iki şeyi bozar:
  //   1. Joker karakterler. Picker'lar `pay*`, `*pay*`, `p?y`
  //      destekliyor; alt-dize süzgeci "pay*" dizesini option'ların
  //      İÇİNDE arar ve HİÇBİRİ eşleşmez — liste boşalır.
  //   2. Sunucunun sıralaması (ör. env'de yoğunluk sırası) alfabetik
  //      bir alt kümeye dönüşür.
  // Yani "kim süzüyor" sorusunun cevabı çağırana bırakılıyor.
  serverFiltered?: boolean;
  title?: string;
  ariaLabel?: string;
  // Sarmalayıcıya (`.cb-wrap`) eklenir — ölçü/konum kancası.
  className?: string;
  // `/` kısayolunun AÇIK hedefi (v0.9.951 / Ö31). Sayfa başına TEK
  // alan işaretlenmeli: GlobalShortcuts querySelectorAll'ın İLKİNİ
  // seçer, yani ikinci bir işaret DOM sırası kumarına döner.
  shortcutSearch?: boolean;
  // Listenin dibine tıklanamaz bir not satırı. Picker'ların
  // "… +N more — refine search" kesinti uyarısı buradan geçiyor:
  // datalist'te bu, `disabled` bir <option> ile taklit ediliyordu
  // (tarayıcıya göre görünen/görünmeyen bir hile).
  footer?: React.ReactNode;
  // Satırın sonuna soluk bir ek etiket (ör. metrik için "ms · gauge").
  // datalist'in `label` niteliğinin yerini alıyor — o nitelik
  // Chromium/Firefox'ta görünür, Safari'de HİÇ görünmez.
  optionMeta?: (option: string) => string | undefined;

  // ——— v0.10.1089 · ortak seçici popover'ı ————————————————————
  //
  // resultCount — başlıktaki "N sonuç". Sunucu-taraflı picker'lar
  // SUNUCU TOPLAMINI verir (liste 200'de kesilse de gerçek eşleşme
  // sayısı); verilmezse istemci süzgecinin eşleşme sayısı.
  resultCount?: number;
  // status — sunucu araması sürüyor ('loading' → "aranıyor…") ya da
  // düştü ('error'). undefined = hazır.
  status?: PickerStatus;
  // recentKey — "Son kullanılan" grubunun kapsamı (lib/pickerRecents).
  // Verilirse listeden SEÇİLEN değer kaydedilir ve alan BOŞKEN son 5
  // değer sonuçların üstünde ayrı grupta gösterilir. Yalnız seçim
  // kaydedilir: Enter'la gönderilen serbest metin (joker sorgu) değil.
  recentKey?: string;
}) {
  const id = useId();
  const listId = `${id}-list`;
  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  // "Son kullanılan" — liste her AÇILDIĞINDA depodan okunur (başka bir
  // sekmede ya da aynı sayfanın ikinci seçicisinde yapılan seçim de görünür).
  const [recents, setRecents] = useState<string[]>([]);
  const [highlight, setHighlight] = useState<number>(-1);
  // Esc ile İPTAL edildi mi? Sonraki blur'un commit ETMEMESİ için.
  // Tuzak: iptal çoğu çağırıcıda düzenleyiciyi söker ya da odağı
  // taşır — yani Esc'in hemen ardından bir blur gelir. O blur commit
  // etseydi "iptal" sessizce KAYDET olurdu (tam tersi).
  const escapedRef = useRef(false);

  // Filtered list — substring match, case-insensitive. Empty query
  // shows the full list so clicking the field reveals all options
  // (matches native <select> "open and look at everything" UX).
  // Cap to 200 rows; service / operation lists in the wild stay
  // well under this but the cap keeps render cheap on degenerate
  // inputs.
  const matched = useMemo(() => {
    // serverFiltered: liste ZATEN cevap — dokunma (joker karakterler
    // ve sunucu sıralaması bozulur).
    if (serverFiltered) return options;
    const q = value.trim().toLowerCase();
    if (!q) return options;
    return options.filter(o => o.toLowerCase().includes(q));
  }, [value, options, serverFiltered]);
  const filtered = useMemo(() => matched.slice(0, 200), [matched]);

  useEffect(() => {
    if (open && recentKey) setRecents(getPickerRecents(recentKey));
  }, [open, recentKey]);

  // Popover'ın düz satır sırası: alan BOŞKEN önce "Son kullanılan", sonra
  // sonuçlar (son kullanılanlar sonuç grubunda tekrar edilmez).
  const items = useMemo<PickerItem[]>(() => {
    const meta = (v: string) => optionMeta?.(v);
    const showRecents = !!recentKey && !value.trim() && recents.length > 0;
    if (!showRecents) return filtered.map(v => ({ value: v, meta: meta(v) }));
    const seen = new Set(recents);
    return [
      ...recents.map(v => ({ value: v, meta: meta(v), group: 'recent' as const })),
      ...filtered.filter(v => !seen.has(v)).map(v => ({ value: v, meta: meta(v) })),
    ];
  }, [filtered, recents, recentKey, value, optionMeta]);

  // Reset highlight whenever the visible set changes — otherwise
  // the index points into a stale list and arrow nav jumps around.
  // İmza DEĞERLERDEN: çağıranlar `optionMeta`yı satır içi ok fonksiyonu
  // olarak veriyor, yani `items` kimliği ebeveynin her çiziminde değişir;
  // kimliğe bağlı bir sıfırlama ok tuşuyla gezilen satırı ilgisiz bir
  // yeniden çizimde (arama durumu) silerdi.
  const itemsSig = items.map(it => `${it.group ?? ''}:${it.value}`).join('\n');
  useEffect(() => { setHighlight(-1); }, [itemsSig]);

  // Satır içi düzenleyici açılışı: odak + metni seç. Yalnız mount'ta.
  useEffect(() => {
    if (!autoFocus || disabled) return;
    inputRef.current?.focus();
    inputRef.current?.select();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Click-outside / Esc close. Portal edilen popover kendi mousedown'ını
  // durdurur (PickerPopover), yani buraya ulaşan her basış alanın dışıdır
  // ya da sarmalayıcının içindedir.
  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, [open]);

  const pick = (v: string) => {
    if (recentKey) recordPickerRecent(recentKey, v);
    onChange(v);
    setOpen(false);
    setHighlight(-1);
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    // Esc'ten SONRA gelen her tuş iptali geçersiz kılar: çağıran alanı
    // ayakta bıraktıysa (sökmediyse) operatör yazmaya devam edebilir ve
    // o oturumun blur'u yeniden commit etmelidir.
    if (e.key !== 'Escape') escapedRef.current = false;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      if (!open) setOpen(true);
      setHighlight(h => Math.min(items.length - 1, h + 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setHighlight(h => Math.max(-1, h - 1));
    } else if (e.key === 'Enter') {
      if (open && highlight >= 0 && highlight < items.length) {
        e.preventDefault();
        pick(items[highlight].value);
      } else {
        // No highlight → take the typed value as-is and let the
        // caller submit. Common case: user typed a custom search.
        setOpen(false);
        onEnter?.();
      }
    } else if (e.key === 'Escape') {
      // Katman sözleşmesi (v0.9.950 / Ö28, E2 dalgası): bir Esc BİR
      // katman kapatır. Liste AÇIKKEN Esc'i tüketiyoruz —
      // keyboard.ts'in escLayer'ı defaultPrevented'a bakar; tüketmezsek
      // açık Combobox'lı bir Drawer'da tek Esc ikisini birden kapatır
      // (FilterBuilder bunu yaşıyordu). Liste KAPALIYKEN dokunmuyoruz:
      // olay katmana akar ve Drawer/Modal normal kapanır.
      if (open) {
        e.preventDefault();
        setOpen(false);
        setHighlight(-1);
      } else if (onEscape) {
        // Liste kapalı + çağıran bir çıkış yolu verdi → SATIR İÇİ
        // DÜZENLEYİCİ katmanı. Aynı kural: bunu da tüketiyoruz, yoksa
        // bir Drawer içindeki düzenleyicide tek Esc ikisini birden
        // kapatırdı (v0.9.1021'in düzelttiği hatanın aynısı, bir
        // katman aşağıda). onEscape VERİLMEDİĞİNDE hiçbir şey
        // yapmıyoruz: olay üst katmana akar — eski davranış birebir.
        e.preventDefault();
        escapedRef.current = true;
        onEscape();
      }
    } else if (e.key === 'Tab') {
      if (open && highlight >= 0 && highlight < items.length) {
        pick(items[highlight].value);
      } else {
        setOpen(false);
      }
    }
  };

  // Liste ne zaman görünür: gösterecek satır varsa, sunucu araması sürüyor/
  // düştüyse ya da operatör bir şey yazdıysa ("eşleşme yok"). Boş alan +
  // boş seçenek listesi (veri daha gelmemiş bir istemci listesi) boş bir
  // kart açmaz — eski davranış.
  const showList = open && !disabled && (items.length > 0 || !!status || !!value.trim());

  return (
    <div ref={wrapRef} className={className ? `cb-wrap ${className}` : 'cb-wrap'} style={{ width }}>
      <input
        ref={inputRef}
        id={id}
        value={value}
        placeholder={placeholder}
        disabled={disabled}
        title={title}
        aria-label={ariaLabel}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={showList}
        aria-controls={showList ? listId : undefined}
        aria-activedescendant={showList && highlight >= 0 ? pickerOptionId(listId, highlight) : undefined}
        {...(shortcutSearch ? { 'data-shortcut-search': '' } : {})}
        onChange={e => { escapedRef.current = false; onChange(e.target.value); setOpen(true); }}
        onFocus={() => { if (!disabled) setOpen(true); }}
        onClick={() => { if (!disabled) setOpen(true); }}
        onBlur={() => {
          setOpen(false);
          setHighlight(-1);
          // Esc iptalinden sonra gelen blur commit ETMEZ.
          if (escapedRef.current) { escapedRef.current = false; return; }
          onBlurCommit?.(value);
        }}
        onKeyDown={onKeyDown}
        autoComplete="off"
        spellCheck={false}
      />
      {/* Caret indicator + clear button. Caret only when value is
          empty so the affordance pair isn't redundant.
          v0.10.924 — buton bütünlüğü Faz 2: IconButton ghost; `cb-clear`/
          `cb-caret` yalnız girdinin içine mutlak YERLEŞİM için. onMouseDown
          preventDefault odağı girdide tutar — kaldırılmamalı. */}
      {value ? (
        <IconButton variant="ghost" size="xs" className="cb-clear"
          aria-label="Clear"
          tooltip="Clear"
          onClick={() => {
            onChange('');
            // Kilitliyken odak/açılış YOK — ama temizleme çalışır.
            if (disabled) return;
            inputRef.current?.focus();
            setOpen(true);
          }}
          onMouseDown={e => e.preventDefault()}
          icon="✕" />
      ) : disabled ? null : (
        <IconButton variant="ghost" size="xs" className="cb-caret" tabIndex={-1}
          aria-label={open ? 'Close' : 'Open'}
          onClick={() => { setOpen(o => !o); inputRef.current?.focus(); }}
          onMouseDown={e => e.preventDefault()}
          icon="▾" />
      )}

      {/* `disabled` liste render'ını da kapatır: alan AÇIK LİSTEYLE
          kilitlenebiliyor (TeamEditor Enter'da busy=true yapıyor,
          alan hâlâ odakta) — o hâlde liste satırın üstünde asılı
          kalırdı. v0.10.1089 — liste ortak PickerPopover (body portalı). */}
      {showList && (
        <PickerPopover
          anchorRef={wrapRef}
          listId={listId}
          items={items}
          highlight={highlight}
          onHighlight={setHighlight}
          onPick={pick}
          query={value}
          current={value}
          count={resultCount ?? matched.length}
          status={status}
          emptyHint="Enter yazılanı kullanır"
          footer={footer}
          ariaLabel={ariaLabel ?? placeholder} />
      )}
    </div>
  );
}
