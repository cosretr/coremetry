import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react';
import { Cpu } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { MenuItem } from '@/components/ui/Menu';
import { Popover } from '@/components/ui/Popover';
import type { CopilotProfileOption } from '@/lib/types';
import { compactModelLabel } from './chatProfileStore';

// ModelPicker — v0.10.1138: kompakt "model ▾" menüsü (CoSRE penceresi,
// /cosre sayfası ve ✨ Explain çekmecesi sohbeti AYNI bileşen).
//
// v0.10.1141 (operatör: "Claude'daki gibi mesaj kutusunun içinde olsun") —
// seçici artık sohbet BAŞLIĞINDA değil, COMPOSER kutusunun içinde, Gönder'in
// solunda bir hap (`.cm-model-pill`). Menü YUKARI açılır (composer ekranın
// dibinde; Popover `placement.prefer='top'`, sığmazsa alta), çapanın sol
// kenarına hizalı, viewport içinde kıstırılı (telefon genişliği dahil).
// Klavye: düğme yerel <button> → Enter/Space açar; ↑ de açar (menü yukarıda);
// menüde ↑↓ Home End gezinir, Esc kapatır ve odak hapa döner (Popover
// sözleşmesi). Açılışta seçili satır odak alır.
//
// ≤1 seçenekte menü yok: yalnız etkin modeli gösteren tıklanamaz etiket
// (v0.9.1037 model çipi sözleşmesi — sahte affordance yok), AYNI yerde.
// Akış sürerken (`disabled`) hap devre dışı ve açık menü kapanır — model
// yalnız boştayken değişir. Seçim kalıcılığı çağıranda (useChatProfile).
//
// 2026-10-10 (operatör: "model hapı çok uzun") — tetikleyici KOMPAKT: Cpu ikonu +
// kısa etiket (compactModelLabel: profil adı, yoksa kısaltılmış model adı); tam
// model kimliği tooltip + aria-label'da ("Model: <tam id>"). Telefon genişliğinde
// yalnız ikon (CSS, ≤640px). Menü satırları tam ayrıntıyı (ad + model + açıklama)
// aynen taşır; tek profilli etiket de aynı kompakt biçim.

export function ModelPicker({ profiles, defaultProfile, value, onChange, activeModel, disabled = false }: {
  profiles: CopilotProfileOption[];
  defaultProfile?: string;
  /** '' = varsayılan */
  value: string;
  onChange: (id: string) => void;
  activeModel: string;
  /** Akış sürerken true: hap tıklanamaz, açık menü kapanır. */
  disabled?: boolean;
}) {
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => { if (disabled) setOpen(false); }, [disabled]);
  const cur = profiles.find(p => p.id === (value || defaultProfile));
  const short = compactModelLabel(cur?.label, activeModel) || 'varsayılan';
  const nameCls = cur?.label?.trim() ? '' : ' mono';
  if (profiles.length < 2) {
    if (!activeModel) return null;
    return (
      <span className="chip cm-model-label" title={`Model: ${activeModel}`} aria-label={`Model: ${activeModel}`}>
        <Cpu size={12} strokeWidth={2} aria-hidden="true" />
        <b className={`cm-model-label__name${nameCls}`}>{short}</b>
      </span>
    );
  }
  const def = profiles.find(p => p.id === defaultProfile);
  const pick = (id: string) => { setOpen(false); onChange(id); };
  const onKeyDown = (e: ReactKeyboardEvent<HTMLButtonElement>) => {
    if (disabled || open) return;
    if (e.key === 'ArrowUp' || e.key === 'ArrowDown') { e.preventDefault(); setOpen(true); }
  };
  const row = (p: CopilotProfileOption | undefined, id: string, title: string) => (
    <MenuItem key={id || '__default'} role="menuitemradio" aria-checked={value === id}
      data-profile={id} onClick={() => pick(id)}>
      <span className="cm-model-row">
        <span className="cm-model-row__head">
          <span className="cm-model-row__check" aria-hidden="true">{value === id ? '✓' : ''}</span>
          <b>{title}</b>
          {p?.model && <span className="mono cm-model-row__model">{p.model}</span>}
        </span>
        {p?.description && <span className="cm-model-row__desc">{p.description}</span>}
      </span>
    </MenuItem>
  );
  const full = activeModel || 'varsayılan';
  return (
    <>
      <Button ref={anchorRef} variant="ghost" size="sm" className="cm-model-pill" type="button"
        onClick={() => setOpen(o => !o)} onKeyDown={onKeyDown} disabled={disabled}
        aria-haspopup="menu" aria-expanded={open}
        aria-label={`Model: ${full}${nameCls ? '' : ` (${short})`} — değiştir`}
        title={`Model: ${full}${disabled ? ' · cevap bitince değiştirilebilir' : ''}`}
        leftIcon={<Cpu size={14} strokeWidth={2} aria-hidden="true" />}>
        <span className={`cm-model-pill__name${nameCls}`}>{short}</span>
        <span className="cm-model-pill__caret" aria-hidden="true">▾</span>
      </Button>
      <Popover anchorRef={anchorRef} open={open && !disabled} onClose={() => setOpen(false)} kind="menu"
        ariaLabel="Model profili" width={280} placement={{ prefer: 'top', align: 'start' }}
        initialFocus='[role="menuitemradio"][aria-checked="true"]'>
        {row(def, '', `Varsayılan${def ? ` · ${def.label || def.id}` : ''}`)}
        {profiles.filter(p => p.id !== defaultProfile).map(p => row(p, p.id, p.label || p.id))}
      </Popover>
    </>
  );
}
