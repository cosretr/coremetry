import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "cn"
import { Slot } from "radix-ui"

const buttonVariants = cva(
  // coremetry: `relative` anchors the loading spinner; `aria-busy` keeps a
  // loading button opaque (MB3, v0.9.925 — busy is not "unavailable")
  "relative aria-busy:cursor-wait aria-busy:disabled:opacity-100 " +
  // /coremetry
  "inline-flex shrink-0 items-center justify-center gap-2 rounded-md text-sm font-medium whitespace-nowrap transition-all outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    variants: {
      variant: {
        // coremetry: v0.10.1888 "Öneri B" (operatör 2026-11-06: "Buton öneri b olsun") —
        // birincil dolu + yumuşak mavi gölge; ikincil (outline / secondary) mavi
        // TONLU (--accent-bg zemin, --accent2 metin); seçili (aria-pressed) dolu
        // birincil. Yeni renk yok: hepsi mevcut tema belirteçleri.
        default: "bg-primary text-primary-foreground shadow-[0_2px_8px_var(--accent-border)] hover:bg-primary/90",
        // coremetry: text-destructive-foreground (← --on-accent), not text-white (v0.10.1848);
        // no dark:bg-destructive/60 — on-accent/err-solid is measured on the SOLID fill (v0.10.1850)
        destructive:
          "bg-destructive text-destructive-foreground hover:bg-destructive/90 focus-visible:ring-destructive/20 dark:focus-visible:ring-destructive/40",
        // /coremetry
        outline:
          "border border-(--accent-border) bg-(--accent-bg) text-(--accent2) hover:border-(--accent2) aria-pressed:border-primary aria-pressed:bg-primary aria-pressed:text-primary-foreground",
        secondary:
          "bg-(--accent-bg) text-(--accent2) hover:shadow-[inset_0_0_0_1px_var(--accent-border)] aria-pressed:bg-primary aria-pressed:text-primary-foreground",
        // /coremetry
        ghost:
          "hover:bg-accent hover:text-accent-foreground dark:hover:bg-accent/50",
        link: "text-primary underline-offset-4 hover:underline",
        // coremetry: accent + ghost-danger (plan §5.4 #3) — tokens only
        accent: "bg-primary text-primary-foreground shadow-[0_2px_8px_var(--accent-border)] hover:bg-primary/90",
        "ghost-danger":
          "text-status-critical hover:bg-status-critical-bg hover:text-status-critical",
        // /coremetry
      },
      size: {
        default: "h-9 px-4 py-2 has-[>svg]:px-3",
        xs: "h-6 gap-1 rounded-md px-2 text-xs has-[>svg]:px-1.5 [&_svg:not([class*='size-'])]:size-3",
        sm: "h-8 gap-1.5 rounded-md px-3 has-[>svg]:px-2.5",
        lg: "h-10 rounded-md px-6 has-[>svg]:px-4",
        icon: "size-9",
        "icon-xs": "size-6 rounded-md [&_svg:not([class*='size-'])]:size-3",
        "icon-sm": "size-8",
        "icon-lg": "size-10",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  }
)

// coremetry: icon sizes require an accessible name at TYPE level (plan
// §5.4 #2, the ui/IconButton contract); `loading` keeps the label in the DOM
// at opacity 0 so the button does not change width (v0.10.919 contract) and
// the accessible name survives; `type` defaults to "button" (§5.4 #4).
type ButtonSize = NonNullable<VariantProps<typeof buttonVariants>["size"]>
type IconSize = Extract<ButtonSize, `icon${string}`>
type ButtonBaseProps = Omit<React.ComponentProps<"button">, "aria-label"> &
  Omit<VariantProps<typeof buttonVariants>, "size"> & {
    asChild?: boolean
    loading?: boolean
  }
type ButtonProps = ButtonBaseProps &
  (
    | { size?: Exclude<ButtonSize, IconSize> | null; "aria-label"?: string }
    | { size: IconSize; "aria-label": string }
  )
// /coremetry

function Button({
  className,
  variant = "default",
  size = "default",
  asChild = false,
  // coremetry: loading + type default
  loading = false,
  type,
  disabled,
  children,
  // /coremetry
  ...props
}: ButtonProps) {
  const Comp = asChild ? Slot.Root : "button"

  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      className={cn(buttonVariants({ variant, size, className }))}
      // coremetry: loading + type default
      type={asChild ? type : (type ?? "button")}
      disabled={disabled || loading || undefined}
      aria-busy={loading || undefined}
      {...props}
    >
      {loading ? (
        <>
          <span className="inline-flex items-center gap-[inherit] opacity-0">
            {children}
          </span>
          <span
            aria-hidden="true"
            className="absolute inset-0 m-auto size-3.5 animate-spin rounded-full border-2 border-current border-t-transparent"
          />
        </>
      ) : (
        children
      )}
      {/* /coremetry */}
    </Comp>
  )
}

export { Button, buttonVariants }
