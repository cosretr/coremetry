import type { HTMLAttributes, ReactNode } from 'react';

// Badge wraps the existing `.badge .b-{tone}` CSS that's already
// used across severity columns, status pills, and section
// headers. Typing the `tone` prop turns "what colour is critical
// again?" into a compile-time check.
//
// Tone semantics:
//   neutral  - default grey (.b-gray) — counts, untyped labels
//   info     - blue (.b-info)         — informational, anomaly meta
//   success  - green (.b-ok)          - resolved / recovered (GEÇİŞ; v0.10.922: sağlıklı durum nötr)
//   warning  - yellow (.b-warn)       - warning severity
//   danger   - red (.b-err)           - critical, error (v0.10.922: 'open' durumu nötr, b-gray)
//   accent   - default (no .b-* cls)  — uses --accent — for app
//                                       chrome (preset, sampled)

export type Tone = 'neutral' | 'info' | 'success' | 'warning' | 'danger' | 'accent';

const toneClass: Record<Tone, string> = {
  neutral: 'b-gray',
  info:    'b-info',
  success: 'b-ok',
  warning: 'b-warn',
  danger:  'b-err',
  accent:  '',
};

export interface BadgeProps extends HTMLAttributes<HTMLSpanElement> {
  tone?: Tone;
  children?: ReactNode;
}

export function Badge({ tone = 'neutral', className, children, ...rest }: BadgeProps) {
  const cls = ['badge', toneClass[tone], className].filter(Boolean).join(' ');
  return <span className={cls} {...rest}>{children}</span>;
}
