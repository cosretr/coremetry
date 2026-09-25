import { useState, type ReactNode } from 'react';
import { ChevronRight, Copy, Download, Pencil, Plus, RefreshCw, Save, Star, Trash2, X } from 'lucide-react';
import { Topbar } from '@/components/Topbar';
import { PageShell } from '@/components/ui/PageShell';
import {
  ActionRow, Button, ButtonGroup, Card, Chip, DisclosureButton, IconButton, LinkButton,
  SegmentedControl, Tooltip,
} from '@/components/ui';
import './design/design.css';

// Design — v0.10.919 (buton bütünlüğü, Seçenek B; operatör onayı
// 2026-09-25). YALNIZ GELİŞTİRMEDE: App.tsx bu modülü
// `import.meta.env.DEV ? lazy(() => import('./pages/Design')) : null`
// ile yükler — üretim derlemesinde dal ölü koddur, parça üretilmez
// (Design.pin.test.ts + `vite build` çıktısında `design-catalog` araması).
//
// Amaç: düğme ailesinin HER varyant × boyut × durumunu tek ekranda
// görmek; yeni bir atom ya da CSS değişikliği önce burada gözle
// doğrulanır. Hover/odak etkileşimlidir (fareyle/Tab ile deneyin);
// disabled/loading/active sabit örneklerdir. Buradaki her şey atom —
// sayfa kendi düğmesini kurmaz (buttonUnityRatchet + ui/no-raw-button).

const BUTTON_VARIANTS = ['primary', 'secondary', 'ghost', 'accent', 'danger', 'ghost-danger'] as const;
const BUTTON_SIZES = ['xs', 'sm', 'md', 'lg'] as const;
const ICON_VARIANTS = ['secondary', 'ghost', 'bare', 'danger'] as const;
const ICON_SIZES = ['xs', 'sm', 'md'] as const;

function Section({ title, note, children }: { title: string; note?: ReactNode; children: ReactNode }) {
  return (
    <Card header={title}>
      {note && <p className="dc-note">{note}</p>}
      <div className="stack gap-4">{children}</div>
    </Card>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="dc-row">
      <span className="dc-label">{label}</span>
      <div className="row gap-2 row-wrap">{children}</div>
    </div>
  );
}

function ButtonSection() {
  const [busy, setBusy] = useState(false);
  return (
    <Section title="Button"
      note="variant zorunlu (tip kapısı). Yüklenirken genişlik korunur: etiket yerinde kalır, spinner üstüne biner.">
      <div className="dc-matrix">
        <span />
        {BUTTON_SIZES.map(s => <span key={s} className="dc-label">{s}</span>)}
        {BUTTON_VARIANTS.map(v => (
          <Matrix key={v} label={v}>
            {BUTTON_SIZES.map(s => <Button key={s} variant={v} size={s}>Kaydet</Button>)}
          </Matrix>
        ))}
      </div>
      <Row label="disabled">
        {BUTTON_VARIANTS.map(v => <Button key={v} variant={v} size="sm" disabled>{v}</Button>)}
      </Row>
      <Row label="loading">
        {BUTTON_VARIANTS.map(v => <Button key={v} variant={v} size="sm" loading>{v}</Button>)}
      </Row>
      <Row label="ikonlu">
        <Button variant="primary" size="sm" leftIcon={<Save size={14} />}>Kaydet</Button>
        <Button variant="secondary" size="sm" leftIcon={<Download size={14} />}>Dışa aktar</Button>
        <Button variant="ghost" size="sm" rightIcon={<ChevronRight size={14} />}>Detay</Button>
      </Row>
      <Row label="genişlik testi">
        <Button variant="primary" loading={busy} leftIcon={<Save size={16} />}>Değişiklikleri kaydet</Button>
        <Button variant="secondary" size="sm" onClick={() => setBusy(b => !b)}>
          {busy ? 'Yüklemeyi bitir' : 'Yüklemeyi başlat'}
        </Button>
      </Row>
    </Section>
  );
}

function Matrix({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <span className="dc-label">{label}</span>
      {children}
    </>
  );
}

function IconButtonSection() {
  const [starred, setStarred] = useState(true);
  return (
    <Section title="IconButton" note="aria-label zorunlu (tip kapısı). Varsayılan: ghost · sm.">
      <div className="dc-matrix dc-matrix-3">
        <span />
        {ICON_SIZES.map(s => <span key={s} className="dc-label">{s}</span>)}
        {ICON_VARIANTS.map(v => (
          <Matrix key={v} label={v}>
            {ICON_SIZES.map(s => (
              <IconButton key={s} variant={v} size={s} aria-label={`${v} ${s} düzenle`} icon={<Pencil size={14} />} />
            ))}
          </Matrix>
        ))}
      </div>
      <Row label="durumlar">
        <IconButton aria-label="Yıldızla" active={starred} onClick={() => setStarred(s => !s)}
          icon={<Star size={14} />} />
        <IconButton aria-label="Devre dışı" disabled icon={<X size={14} />} />
      </Row>
    </Section>
  );
}

function ButtonGroupSection() {
  return (
    <Section title="ButtonGroup"
      note="Eş eylemler; her buton kendi Tab durağı. Tek seçim → SegmentedControl; commit satırı → ActionRow. size çocuklara devredilir.">
      <Row label="aralıklı · sm">
        <ButtonGroup aria-label="Triage" size="sm">
          <Button variant="secondary">Ack</Button>
          <Button variant="secondary">Resolve</Button>
          <Button variant="secondary">Ignore</Button>
        </ButtonGroup>
      </Row>
      <Row label="bitişik · sm">
        <ButtonGroup aria-label="Dışa aktar" attached size="sm">
          <Button variant="secondary" leftIcon={<Download size={14} />}>CSV</Button>
          <Button variant="secondary">NDJSON</Button>
        </ButtonGroup>
      </Row>
      <Row label="satır eylemleri">
        <ButtonGroup aria-label="Satır eylemleri" size="sm">
          <Button variant="secondary">Edit</Button>
          <Button variant="secondary">Disable</Button>
          <Button variant="ghost-danger">Delete</Button>
        </ButtonGroup>
      </Row>
      <Row label="ikon araç çubuğu">
        <ButtonGroup aria-label="Grafik araçları">
          <Tooltip content="Yenile"><IconButton aria-label="Yenile" icon={<RefreshCw size={14} />} /></Tooltip>
          <Tooltip content="Kopyala"><IconButton aria-label="Kopyala" icon={<Copy size={14} />} /></Tooltip>
          <Tooltip content="Panel ekle"><IconButton aria-label="Panel ekle" icon={<Plus size={14} />} /></Tooltip>
        </ButtonGroup>
      </Row>
    </Section>
  );
}

function SegmentedSection() {
  const [v, setV] = useState<'list' | 'aggregate' | 'shapes'>('list');
  const [w, setW] = useState<'5' | '15' | '60'>('15');
  return (
    <Section title="SegmentedControl" note="Tek seçim (radiogroup); grup TEK Tab durağı, oklarla gezilir.">
      <Row label="md">
        <SegmentedControl aria-label="Görünüm" value={v} onChange={setV} options={[
          { value: 'list', label: 'Traces' },
          { value: 'aggregate', label: 'Aggregated' },
          { value: 'shapes', label: 'Shapes', disabled: true, title: 'disabled örneği' },
        ]} />
      </Row>
      <Row label="sm">
        <SegmentedControl size="sm" aria-label="Pencere" value={w} onChange={setW} options={[
          { value: '5', label: '5 dk' }, { value: '15', label: '15 dk' }, { value: '60', label: '1 sa' },
        ]} />
      </Row>
    </Section>
  );
}

function ChipSection() {
  const [on, setOn] = useState(true);
  return (
    <Section title="Chip" note="Filtre/etiket; active → aria-pressed. onRemove ayrı × düğmesi basar.">
      <Row label="neutral">
        <Chip size="xs">xs</Chip>
        <Chip>sm</Chip>
        <Chip pill>pill</Chip>
        <Chip active={on} onClick={() => setOn(x => !x)}>toggle</Chip>
      </Row>
      <Row label="accent">
        <Chip tone="accent" size="xs">xs</Chip>
        <Chip tone="accent">sm</Chip>
        <Chip tone="accent" active onClick={() => undefined}>active</Chip>
        <Chip tone="accent" onRemove={() => undefined} removeLabel="service=checkout filtresini kaldır">
          service=checkout
        </Chip>
      </Row>
    </Section>
  );
}

function TextButtonSection() {
  const [open, setOpen] = useState(false);
  const [sec, setSec] = useState(true);
  return (
    <Section title="LinkButton · DisclosureButton">
      <Row label="LinkButton">
        <LinkButton>accent · hover</LinkButton>
        <LinkButton tone="muted">muted</LinkButton>
        <LinkButton underline="dotted">dotted</LinkButton>
        <LinkButton underline="none">none</LinkButton>
      </Row>
      <Row label="Disclosure">
        <DisclosureButton expanded={open} onClick={() => setOpen(o => !o)}>Satır</DisclosureButton>
        <DisclosureButton anatomy="section" expanded={sec} onClick={() => setSec(o => !o)}>Bölüm</DisclosureButton>
      </Row>
    </Section>
  );
}

function TooltipSection() {
  return (
    <Section title="Tooltip"
      note="Fare: 500 ms gecikme, ipucunun üstüne geçilebilir. Klavye (Tab): hemen. Dokunmatik: açılmaz. Esc kapatır.">
      <Row label="örnekler">
        <Tooltip content="Son 24 saatin verisiyle yeniden çiz">
          <IconButton aria-label="Yenile" variant="secondary" icon={<RefreshCw size={14} />} />
        </Tooltip>
        <Tooltip content="Kalıcı olarak siler — geri alınamaz" side="bottom">
          <Button variant="ghost-danger" size="sm" leftIcon={<Trash2 size={14} />}>Sil</Button>
        </Tooltip>
        <Tooltip content="Uzun içerik kıstırılır: en çok 320px genişlik, viewport kenarından 8px pay. Üstte yer yoksa alta çevrilir.">
          <Button variant="secondary" size="sm">Uzun ipucu</Button>
        </Tooltip>
      </Row>
    </Section>
  );
}

function ActionRowSection() {
  return (
    <Section title="ActionRow" note="Commit satırı: yıkıcı solda, ikincil ortada, tek birincil en sağda.">
      <ActionRow
        destructive={<Button variant="ghost-danger">Sil</Button>}
        secondary={<Button variant="secondary">İptal</Button>}
        confirm={<Button variant="primary">Kaydet</Button>} />
    </Section>
  );
}

export default function DesignPage() {
  return (
    <>
      <Topbar title="Design catalog" />
      <PageShell>
        <div className="stack gap-4" data-cm="design-catalog">
          <p className="dc-note">
            Yalnız geliştirme ortamında (vite dev) — üretim derlemesinde bu sayfa yok.
            Tema değişimi için sağ üstteki tema anahtarını kullanın.
          </p>
          <ButtonSection />
          <IconButtonSection />
          <ButtonGroupSection />
          <SegmentedSection />
          <ChipSection />
          <TextButtonSection />
          <TooltipSection />
          <ActionRowSection />
        </div>
      </PageShell>
    </>
  );
}
