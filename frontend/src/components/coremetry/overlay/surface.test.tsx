// @vitest-environment jsdom
//
// surface.test — v0.10.1715 (operatör onayı): overlay içerikleri sayfa
// zemininde (bg-background = --bg0) değil, bg-card (= --bg1) üzerinde.
// Sarmalayıcı başına bir pin + çağıranın bg sınıfı kazanır (twMerge).
import { describe, it, expect, beforeAll, afterEach } from 'vitest';
import { act, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { Sheet, SheetContent, SheetTitle, SheetDescription } from './Sheet';
import { Dialog, DialogContent, DialogTitle, DialogDescription } from './Dialog';
import { AlertDialog, AlertDialogContent, AlertDialogTitle, AlertDialogDescription } from './AlertDialog';

const noop = () => {};
let root: Root | null = null;
let host: HTMLDivElement | null = null;

beforeAll(() => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
});
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null;
  host = null;
  document.body.innerHTML = '';
});

function classesOf(node: ReactNode): string[] {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(node));
  const el = document.querySelector<HTMLElement>('[data-probe]');
  expect(el).not.toBeNull();
  return el!.className.split(/\s+/);
}

const cases: [string, (cls?: string) => ReactNode][] = [
  ['Sheet', cls => (
    <Sheet open onOpenChange={noop}><SheetContent data-probe="" className={cls}><SheetTitle>t</SheetTitle><SheetDescription>d</SheetDescription></SheetContent></Sheet>
  )],
  ['Dialog', cls => (
    <Dialog open onOpenChange={noop}><DialogContent data-probe="" className={cls}><DialogTitle>t</DialogTitle><DialogDescription>d</DialogDescription></DialogContent></Dialog>
  )],
  ['AlertDialog', cls => (
    <AlertDialog open onOpenChange={noop}><AlertDialogContent data-probe="" className={cls}><AlertDialogTitle>t</AlertDialogTitle><AlertDialogDescription>d</AlertDialogDescription></AlertDialogContent></AlertDialog>
  )],
];

describe('overlay zemini bg-card (v0.10.1715)', () => {
  it.each(cases)('%s: bg-card var, bg-background yok', (_n, mk) => {
    const cls = classesOf(mk());
    expect(cls).toContain('bg-card');
    expect(cls).not.toContain('bg-background');
  });
  it.each(cases)('%s: çağıranın bg sınıfı kazanır', (_n, mk) => {
    const cls = classesOf(mk('bg-muted'));
    expect(cls).toContain('bg-muted');
    expect(cls).not.toContain('bg-card');
  });
});
