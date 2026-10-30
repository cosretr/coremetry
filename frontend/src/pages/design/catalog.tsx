import { useRef, useState, type ReactNode } from 'react';
import type { VariantProps } from 'class-variance-authority';
import { Bell, Copy, Pencil, Plus, Trash2 } from 'lucide-react';
import { Badge, badgeVariants } from '@/components/shadcn/badge';
import { Button, buttonVariants } from '@/components/shadcn/button';
import { ButtonGroup, ButtonGroupSeparator, ButtonGroupText } from '@/components/shadcn/button-group';
import {
  Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandShortcut,
} from '@/components/shadcn/command';
import { Input } from '@/components/shadcn/input';
import { Label } from '@/components/shadcn/label';
import { Checkbox } from '@/components/shadcn/checkbox';
import { NativeSelect, NativeSelectOption } from '@/components/shadcn/native-select';
import { ScrollArea } from '@/components/shadcn/scroll-area';
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from '@/components/shadcn/select';
import { Separator } from '@/components/shadcn/separator';
import { Skeleton } from '@/components/shadcn/skeleton';
import { Table, TableBody, TableCaption, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { Tabs, TabsContent, TabsList, TabsTrigger, tabsListVariants } from '@/components/shadcn/tabs';
import { Textarea } from '@/components/shadcn/textarea';
import {
  ContextMenu, ContextMenuCheckboxItem, ContextMenuContent, ContextMenuItem, ContextMenuLabel, ContextMenuSeparator,
  ContextMenuShortcut, ContextMenuTrigger,
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
  DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuShortcut, DropdownMenuTrigger,
  Popover, PopoverContent, PopoverDescription, PopoverHeader, PopoverTitle, PopoverTrigger,
  Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle,
  Tooltip, TooltipContent, TooltipTrigger,
} from '@/components/coremetry/overlay';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { Field, SelectField, TextareaField, CheckboxField } from '@/components/coremetry/Field';
import CoremetryToaster from '@/components/coremetry/Toaster';
import { CM_STRINGS } from '@/components/coremetry/strings';
import { toast, type ToastEntry } from '@/lib/toast';
import { STATUSES } from '@/lib/statusTone';
import type { Cell } from './cells';

// /design shadcn kataloğu — v0.10.1619 (shadcn Faz 1 R10, plan §8.1).
// YALNIZ GELİŞTİRME (pages/Design.tsx üzerinden; üretimde ölü kod).
// components/shadcn/'deki HER dosya burada bir anahtar (Design.pin +
// designAxe testleri; yeni kurulum katalogsuz = kırmızı). Varyant listeleri
// cva tipinden TÜKETİLİR: `satisfies` + `Exhaustive` — dosyaya yeni varyant
// eklenip buraya eklenmezse tsc kırmızı (cva 0.7 yapılandırmayı çalışma
// anında dışa vermiyor).
// Overlay'ler coremetry/overlay sarmalayıcılarıyla çizilir (Esc köprüsü,
// Türkçe "Kapat"); portala giden içerik hücrenin data-theme/data-density'sini
// taşır, böylece üç temada da hücrenin temasıyla görünür. select + command
// shadcn dosyasından doğrudan (sarmalayıcıları kendi dilimlerinde gelir;
// eslint muafiyeti yalnız pages/design/catalog.tsx).

export interface CatalogCtx {
  cell: Cell;
  /** true: overlay'ler açık başlar (axe testi). Sayfada tetikleyiciyle açılır. */
  forceOpen: boolean;
}

type Exhaustive<All, Listed> = [Exclude<All, Listed>] extends [never] ? true : never;

type ButtonVariant = NonNullable<VariantProps<typeof buttonVariants>['variant']>;
type ButtonSize = NonNullable<VariantProps<typeof buttonVariants>['size']>;
const BUTTON_VARIANTS = ['default', 'secondary', 'outline', 'ghost', 'link', 'destructive', 'accent', 'ghost-danger'] as const satisfies readonly ButtonVariant[];
const TEXT_SIZES = ['xs', 'sm', 'default', 'lg'] as const satisfies readonly ButtonSize[];
const ICON_SIZES = ['icon-xs', 'icon-sm', 'icon', 'icon-lg'] as const satisfies readonly ButtonSize[];
const buttonVariantsCovered: Exhaustive<ButtonVariant, typeof BUTTON_VARIANTS[number]> = true;
const buttonSizesCovered: Exhaustive<ButtonSize, typeof TEXT_SIZES[number] | typeof ICON_SIZES[number]> = true;

type BadgeVariant = NonNullable<VariantProps<typeof badgeVariants>['variant']>;
const BADGE_VARIANTS = ['default', 'secondary', 'destructive', 'outline', 'ghost', 'link'] as const satisfies readonly BadgeVariant[];
const badgeVariantsCovered: Exhaustive<BadgeVariant, typeof BADGE_VARIANTS[number]> = true;

type TabsVariant = NonNullable<VariantProps<typeof tabsListVariants>['variant']>;
const TABS_VARIANTS = ['default', 'line'] as const satisfies readonly TabsVariant[];
const tabsVariantsCovered: Exhaustive<TabsVariant, typeof TABS_VARIANTS[number]> = true;

export const VARIANTS_COVERED = buttonVariantsCovered && buttonSizesCovered && badgeVariantsCovered && tabsVariantsCovered;

const themeAttrs = (c: Cell) => ({ 'data-theme': c.theme, 'data-density': c.density });

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="dc-row">
      <span className="dc-label">{label}</span>
      <div className="row gap-2 row-wrap">{children}</div>
    </div>
  );
}

function ButtonDemo() {
  return (
    <>
      {BUTTON_VARIANTS.map(v => (
        <Row key={v} label={v}>
          {TEXT_SIZES.map(s => <Button key={s} variant={v} size={s}>Kaydet {s}</Button>)}
          {ICON_SIZES.map(s => <Button key={s} variant={v} size={s} aria-label={`${v} ${s} düzenle`}><Pencil /></Button>)}
        </Row>
      ))}
      <Row label="disabled">{BUTTON_VARIANTS.map(v => <Button key={v} variant={v} size="sm" disabled>{v}</Button>)}</Row>
      <Row label="loading">{BUTTON_VARIANTS.map(v => <Button key={v} variant={v} size="sm" loading>{v}</Button>)}</Row>
      <Row label="aria-pressed">
        <Button variant="secondary" size="sm" aria-pressed="true">Açık</Button>
        <Button variant="ghost" size="sm" aria-pressed="false">Kapalı</Button>
      </Row>
      <Row label="aria-invalid">
        <Button variant="outline" size="sm" aria-invalid="true">Geçersiz</Button>
      </Row>
    </>
  );
}

function BadgeDemo() {
  return (
    <Row label="variant">
      {BADGE_VARIANTS.map(v => <Badge key={v} variant={v}>{v}</Badge>)}
      <Badge variant="secondary"><Bell aria-hidden="true" />ikonlu</Badge>
    </Row>
  );
}

function ButtonGroupDemo() {
  return (
    <>
      <Row label="horizontal">
        <ButtonGroup aria-label="Dışa aktar">
          <Button variant="outline" size="sm">CSV</Button>
          <Button variant="outline" size="sm">NDJSON</Button>
          <ButtonGroupSeparator />
          <Button variant="outline" size="icon-sm" aria-label="Kopyala"><Copy /></Button>
        </ButtonGroup>
        <ButtonGroup aria-label="Metinli grup">
          <ButtonGroupText>https://</ButtonGroupText>
          <Input aria-label="Adres" defaultValue="coremetry.local" />
        </ButtonGroup>
      </Row>
      <Row label="vertical">
        <ButtonGroup orientation="vertical" aria-label="Yakınlaştır">
          <Button variant="outline" size="icon-sm" aria-label="Ekle"><Plus /></Button>
          <Button variant="outline" size="icon-sm" aria-label="Sil"><Trash2 /></Button>
        </ButtonGroup>
      </Row>
    </>
  );
}

function CommandDemo() {
  return (
    <Command className="dc-boxed" label="Komut paleti örneği">
      <CommandInput placeholder={CM_STRINGS.search} aria-label={CM_STRINGS.search} />
      <CommandList label={CM_STRINGS.suggestions}>
        <CommandEmpty>{CM_STRINGS.noResults}</CommandEmpty>
        <CommandGroup heading="Sayfalar">
          <CommandItem>Servisler<CommandShortcut>G S</CommandShortcut></CommandItem>
          <CommandItem>Problemler<CommandShortcut>G P</CommandShortcut></CommandItem>
          <CommandItem disabled>Devre dışı öğe</CommandItem>
        </CommandGroup>
        {/* CommandSeparator YOK: cmdk 1.1.1 onu role=separator olarak listbox'ın
            içine koyar → axe aria-required-children (R10 ölçümü). */}
        <CommandGroup heading="Eylemler">
          <CommandItem>Temayı değiştir</CommandItem>
        </CommandGroup>
      </CommandList>
    </Command>
  );
}

function ContextMenuDemo({ cell }: CatalogCtx) {
  const [open, setOpen] = useState(false);
  const [pinned, setPinned] = useState(true);
  return (
    <ContextMenu open={open} onOpenChange={setOpen}>
      <ContextMenuTrigger className="dc-drop">Sağ tık (bağlam menüsü)</ContextMenuTrigger>
      <ContextMenuContent {...themeAttrs(cell)}>
        <ContextMenuLabel>checkout-api</ContextMenuLabel>
        <ContextMenuSeparator />
        <ContextMenuItem>Trace'leri aç<ContextMenuShortcut>T</ContextMenuShortcut></ContextMenuItem>
        <ContextMenuCheckboxItem checked={pinned} onCheckedChange={v => setPinned(v === true)}>Sabitle</ContextMenuCheckboxItem>
        <ContextMenuItem variant="destructive">Kaldır</ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

function DialogDemo({ cell, forceOpen }: CatalogCtx) {
  const [open, setOpen] = useState(forceOpen);
  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>Dialog aç</Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent {...themeAttrs(cell)}>
          <DialogHeader>
            <DialogTitle>Kuralı sil</DialogTitle>
            <DialogDescription>Bu işlem geri alınamaz; kuralın geçmişi korunur.</DialogDescription>
          </DialogHeader>
          <DialogFooter showCloseButton>
            <Button variant="destructive">Sil</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function DropdownMenuDemo({ cell, forceOpen }: CatalogCtx) {
  const [open, setOpen] = useState(forceOpen);
  const [sort, setSort] = useState('p95');
  const [errors, setErrors] = useState(true);
  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm">Menü</Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent {...themeAttrs(cell)}>
        <DropdownMenuLabel>Sırala</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={sort} onValueChange={setSort}>
          <DropdownMenuRadioItem value="p95">p95</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="rate">İstek oranı</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem checked={errors} onCheckedChange={v => setErrors(v === true)}>Yalnız hatalı</DropdownMenuCheckboxItem>
        <DropdownMenuItem>Kopyala<DropdownMenuShortcut>⌘C</DropdownMenuShortcut></DropdownMenuItem>
        <DropdownMenuItem disabled>Devre dışı</DropdownMenuItem>
        <DropdownMenuItem variant="destructive">Sil</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function InputDemo() {
  return (
    <Row label="durumlar">
      <Input aria-label="Servis adı" placeholder="servis adı" />
      <Input aria-label="Dolu" defaultValue="checkout-api" />
      <Input aria-label="Devre dışı" disabled defaultValue="salt okunur" />
      <Input aria-label="Geçersiz" aria-invalid="true" defaultValue="??" />
    </Row>
  );
}

function NativeSelectDemo() {
  return (
    <Row label="size">
      {(['default', 'sm'] as const).map(s => (
        <NativeSelect key={s} size={s} aria-label={`Pencere ${s}`} defaultValue="15m">
          <NativeSelectOption value="5m">5 dk</NativeSelectOption>
          <NativeSelectOption value="15m">15 dk</NativeSelectOption>
          <NativeSelectOption value="1h">1 sa</NativeSelectOption>
        </NativeSelect>
      ))}
      <NativeSelect aria-label="Devre dışı" disabled><NativeSelectOption value="x">devre dışı</NativeSelectOption></NativeSelect>
    </Row>
  );
}

function PopoverDemo({ cell, forceOpen }: CatalogCtx) {
  const [open, setOpen] = useState(forceOpen);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild><Button variant="outline" size="sm">Popover</Button></PopoverTrigger>
      {/* Radix Popover içeriği role=dialog, adı yok → axe aria-dialog-name.
          Tüketici aria-label (ya da aria-labelledby) vermeli (R10 ölçümü). */}
      <PopoverContent aria-label="Eşik" {...themeAttrs(cell)}>
        <PopoverHeader>
          <PopoverTitle>Eşik</PopoverTitle>
          <PopoverDescription>p95 bu değeri aşınca uyarı.</PopoverDescription>
        </PopoverHeader>
      </PopoverContent>
    </Popover>
  );
}

function ScrollAreaDemo() {
  return (
    <ScrollArea className="dc-boxed dc-scroll">
      <ul className="dc-list">
        {Array.from({ length: 20 }, (_, i) => <li key={i}>servis-{String(i + 1).padStart(2, '0')}</li>)}
      </ul>
    </ScrollArea>
  );
}

function SelectDemo({ cell, forceOpen }: CatalogCtx) {
  const [open, setOpen] = useState(forceOpen);
  return (
    <Row label="size">
      <Select open={open} onOpenChange={setOpen} defaultValue="dark">
        <SelectTrigger aria-label="Tema">
          <SelectValue placeholder={CM_STRINGS.selectPlaceholder} />
        </SelectTrigger>
        <SelectContent {...themeAttrs(cell)}>
          <SelectGroup>
            <SelectLabel>Tema</SelectLabel>
            <SelectItem value="dark">Koyu</SelectItem>
            <SelectItem value="light">Açık</SelectItem>
            <SelectItem value="redhat" disabled>Red Hat</SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
      <Select>
        <SelectTrigger size="sm" aria-label="Boş seçim"><SelectValue placeholder={CM_STRINGS.selectPlaceholder} /></SelectTrigger>
        <SelectContent {...themeAttrs(cell)}><SelectItem value="a">A</SelectItem></SelectContent>
      </Select>
    </Row>
  );
}

function SeparatorDemo() {
  return (
    <Row label="yönler">
      <div className="dc-sep-h"><span>üst</span><Separator /><span>alt</span></div>
      <div className="row gap-2 dc-sep-v"><span>sol</span><Separator orientation="vertical" /><span>sağ</span></div>
    </Row>
  );
}

function SheetDemo({ cell, forceOpen }: CatalogCtx) {
  const [side, setSide] = useState<'right' | 'left' | 'top' | 'bottom' | null>(forceOpen ? 'right' : null);
  return (
    <>
      <Row label="side">
        {(['right', 'left', 'top', 'bottom'] as const).map(s => (
          <Button key={s} variant="outline" size="sm" onClick={() => setSide(s)}>Sheet {s}</Button>
        ))}
      </Row>
      <Sheet open={side !== null} onOpenChange={o => { if (!o) setSide(null); }}>
        <SheetContent side={side ?? 'right'} {...themeAttrs(cell)}>
          <SheetHeader>
            <SheetTitle>Span ayrıntısı</SheetTitle>
            <SheetDescription>checkout-api · POST /orders</SheetDescription>
          </SheetHeader>
          <SheetFooter><Button variant="outline" onClick={() => setSide(null)}>{CM_STRINGS.close}</Button></SheetFooter>
        </SheetContent>
      </Sheet>
    </>
  );
}

function SkeletonDemo() {
  return (
    <div className="stack gap-2" role="status" aria-busy="true" aria-label="Yükleniyor">
      <Skeleton className="dc-skel dc-skel-lg" />
      <Skeleton className="dc-skel" />
    </div>
  );
}

function TableDemo() {
  return (
    <Table>
      <TableCaption>Örnek servis tablosu</TableCaption>
      <TableHeader>
        <TableRow><TableHead>Servis</TableHead><TableHead>p95</TableHead><TableHead>Hata</TableHead></TableRow>
      </TableHeader>
      <TableBody>
        <TableRow><TableCell>checkout-api</TableCell><TableCell>212 ms</TableCell><TableCell>0.4%</TableCell></TableRow>
        <TableRow data-state="selected"><TableCell>payment</TableCell><TableCell>88 ms</TableCell><TableCell>0.0%</TableCell></TableRow>
      </TableBody>
    </Table>
  );
}

function TabsDemo() {
  return (
    <>
      {TABS_VARIANTS.map(v => (
        <Row key={v} label={v}>
          <Tabs defaultValue="overview">
            <TabsList variant={v}>
              <TabsTrigger value="overview">Genel</TabsTrigger>
              <TabsTrigger value="traces">Trace'ler</TabsTrigger>
              <TabsTrigger value="logs" disabled>Loglar</TabsTrigger>
            </TabsList>
            <TabsContent value="overview">Genel bakış içeriği</TabsContent>
            <TabsContent value="traces">Trace listesi</TabsContent>
          </Tabs>
        </Row>
      ))}
    </>
  );
}

function TextareaDemo() {
  return (
    <Row label="durumlar">
      <Textarea aria-label="Not" placeholder="not" />
      <Textarea aria-label="Devre dışı not" disabled defaultValue="salt okunur" />
      <Textarea aria-label="Geçersiz not" aria-invalid="true" defaultValue="??" />
    </Row>
  );
}

function TooltipDemo({ cell, forceOpen }: CatalogCtx) {
  return (
    <Tooltip open={forceOpen || undefined}>
      <TooltipTrigger asChild>
        <Button variant="outline" size="icon-sm" aria-label="Kopyala"><Copy /></Button>
      </TooltipTrigger>
      <TooltipContent {...themeAttrs(cell)}>Panoya kopyala</TooltipContent>
    </Tooltip>
  );
}

function LabelDemo() {
  return (
    <Row label="htmlFor">
      <Label htmlFor="dc-label-in">Thanos Querier URL</Label>
      <Input id="dc-label-in" placeholder="https://…" />
    </Row>
  );
}

function CheckboxDemo() {
  return (
    <Row label="durumlar">
      <Checkbox aria-label="Kapalı" />
      <Checkbox aria-label="Açık" defaultChecked />
      <Checkbox aria-label="Devre dışı" disabled />
      <Checkbox aria-label="Devre dışı açık" disabled defaultChecked />
      <Checkbox aria-label="Geçersiz" aria-invalid="true" />
    </Row>
  );
}

function FieldDemo() {
  const [on, setOn] = useState(true);
  return (
    <>
      <Row label="Field">
        <Field label="Cluster name" placeholder="prod-ist" hint="join key" />
        <Field label="Token" type="password" labelAside={<span>· stored</span>} placeholder="(leave empty to keep stored value)" />
        <Field small label="Port" defaultValue="6443" error="1–65535 arası" />
      </Row>
      <Row label="Select / Textarea">
        <SelectField small label="Auth" defaultValue="bearer">
          <option value="bearer">Bearer token</option>
          <option value="none">None</option>
        </SelectField>
        <TextareaField label="API server URLs" rows={2} hint="satır ya da virgül" />
      </Row>
      <Row label="Checkbox">
        <CheckboxField label="Enabled" checked={on} onCheckedChange={setOn} />
        <CheckboxField label="Skip TLS verify" hint="yalnız test kümeleri" checked={false} onCheckedChange={() => {}} />
        <CheckboxField label="Devre dışı" disabled checked onCheckedChange={() => {}} />
      </Row>
    </>
  );
}

function StatusBadgeDemo() {
  return (
    <>
      {(['md', 'sm'] as const).map(size => (
        <Row key={size} label={size}>
          {STATUSES.map(s => <StatusBadge key={s} status={s} size={size} />)}
          {STATUSES.map(s => <StatusBadge key={`${s}-dot`} status={s} size={size} dot />)}
        </Row>
      ))}
    </>
  );
}

function ToasterDemo({ forceOpen }: CatalogCtx) {
  // Sayfada: düğmeler lib/toast'u çağırır, kabuktaki TEK tembel kapı çizer.
  // Testte: çizici doğrudan mount edilir, bir girdi sıradan yeniden oynatılır.
  const queue = useRef<ToastEntry[]>(forceOpen ? [{ id: 'design-1', kind: 'success', message: 'Kaydedildi', durationMs: 60_000 }] : []);
  const live = useRef(false);
  return (
    <>
      <Row label="kind">
        <Button variant="outline" size="sm" onClick={() => toast.success('Görünüm kaydedildi')}>success</Button>
        <Button variant="outline" size="sm" onClick={() => toast.error('HTTP 500: kaydedilemedi')}>error</Button>
        <Button variant="outline" size="sm" onClick={() => toast.info('Koyu temaya geçildi')}>info</Button>
        <Button variant="outline" size="sm" onClick={() => toast.warning('Kaydedildi, SMTP testi başarısız')}>warning</Button>
      </Row>
      {forceOpen && <CoremetryToaster queue={queue} live={live} />}
    </>
  );
}

/** Anahtar = components/shadcn/<ad>.tsx (+ coremetry bileşikleri). */
export const CATALOG: Record<string, (ctx: CatalogCtx) => ReactNode> = {
  'badge': () => <BadgeDemo />,
  'button': () => <ButtonDemo />,
  'button-group': () => <ButtonGroupDemo />,
  'command': () => <CommandDemo />,
  'context-menu': (ctx) => <ContextMenuDemo {...ctx} />,
  'dialog': (ctx) => <DialogDemo {...ctx} />,
  'dropdown-menu': (ctx) => <DropdownMenuDemo {...ctx} />,
  'input': () => <InputDemo />,
  'label': () => <LabelDemo />,
  'checkbox': () => <CheckboxDemo />,
  'native-select': () => <NativeSelectDemo />,
  'popover': (ctx) => <PopoverDemo {...ctx} />,
  'scroll-area': () => <ScrollAreaDemo />,
  'select': (ctx) => <SelectDemo {...ctx} />,
  'separator': () => <SeparatorDemo />,
  'sheet': (ctx) => <SheetDemo {...ctx} />,
  'skeleton': () => <SkeletonDemo />,
  'table': () => <TableDemo />,
  'tabs': () => <TabsDemo />,
  'textarea': () => <TextareaDemo />,
  'tooltip': (ctx) => <TooltipDemo {...ctx} />,
  'coremetry/StatusBadge': () => <StatusBadgeDemo />,
  'coremetry/Toaster': (ctx) => <ToasterDemo {...ctx} />,
  'coremetry/Field': () => <FieldDemo />,
};
