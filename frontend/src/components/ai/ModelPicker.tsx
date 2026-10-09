import { useRef, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { MenuItem } from '@/components/ui/Menu';
import { Popover } from '@/components/ui/Popover';
import type { CopilotProfileOption } from '@/lib/types';

// ModelPicker — v0.10.1138: sohbet başlığındaki kompakt "model ▾" menüsü
// (CoSRE penceresi, /cosre sayfası ve ✨ Explain çekmecesi sohbeti AYNI bileşen).
//
// ≤1 seçenekte menü yok: yalnız etkin modeli gösteren tıklanamaz rozet
// (v0.9.1037 model çipi sözleşmesi — sahte affordance yok). >1 seçenekte rozet
// bir düğmedir; menü satırı ad + model + kısa açıklama ("Hızlı", "Derin")
// taşır, seçili satır `menuitemradio aria-checked`. Seçim kalıcılığı
// çağıranda (useChatProfile).
export function ModelPicker({ profiles, defaultProfile, value, onChange, activeModel }: {
  profiles: CopilotProfileOption[];
  defaultProfile?: string;
  /** '' = varsayılan */
  value: string;
  onChange: (id: string) => void;
  activeModel: string;
}) {
  const anchorRef = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  if (profiles.length < 2) {
    if (!activeModel) return null;
    return (
      <span className="chip" style={{ flexShrink: 0, fontSize: 10.5 }} title="Cevapları üreten model">
        <span className="k">model</span>
        <b className="mono">{activeModel}</b>
      </span>
    );
  }
  const def = profiles.find(p => p.id === defaultProfile);
  const pick = (id: string) => { setOpen(false); onChange(id); };
  const row = (p: CopilotProfileOption | undefined, id: string, title: string) => (
    <MenuItem key={id || '__default'} role="menuitemradio" aria-checked={value === id}
      data-profile={id} onClick={() => pick(id)}>
      <span style={{ display: 'flex', flexDirection: 'column', gap: 1, minWidth: 0 }}>
        <span style={{ display: 'flex', gap: 6, alignItems: 'baseline' }}>
          <b>{value === id ? '✓ ' : ''}{title}</b>
          {p?.model && <span className="mono" style={{ fontSize: 10.5, color: 'var(--text3)' }}>{p.model}</span>}
        </span>
        {p?.description && <span style={{ fontSize: 11, color: 'var(--text2)', whiteSpace: 'normal' }}>{p.description}</span>}
      </span>
    </MenuItem>
  );
  return (
    <>
      <Button ref={anchorRef} variant="ghost" size="sm" onClick={() => setOpen(o => !o)}
        aria-haspopup="menu" aria-expanded={open} aria-label={`Model: ${activeModel || 'varsayılan'} — değiştir`}
        title="Bu konuşmanın modeli (seçim kullanıcı başına hatırlanır)" style={{ flexShrink: 0 }}>
        <span className="k" style={{ fontSize: 10.5, color: 'var(--text3)' }}>model</span>{' '}
        <b className="mono" style={{ fontSize: 10.5 }}>{activeModel || 'varsayılan'}</b> ▾
      </Button>
      <Popover anchorRef={anchorRef} open={open} onClose={() => setOpen(false)} kind="menu" ariaLabel="Model profili" width={260}>
        {row(def, '', `Varsayılan${def ? ` · ${def.label || def.id}` : ''}`)}
        {profiles.filter(p => p.id !== defaultProfile).map(p => row(p, p.id, p.label || p.id))}
      </Popover>
    </>
  );
}
