// toast.tsx — the Toast renderer + Toaster container, ported from src/ui/toast.js.
//
// toast.js rendered a single bottom-center pill: --surface background + 1px
// --border + 16px radius, 14px sans body, a fade+rise entrance (the `toastFade`
// keyframe), role="status" aria-live="polite", pointer-events:none. This keeps
// that exact visual intent but drives it off the queued useToast store so a
// stack of variant-aware toasts can render, each auto-dismissing after the same
// 2500ms timeout (see hooks/use-toast.ts).
//
// CSS -> Tailwind token mapping (no raw CSS strings copied):
//   var(--surface) + 1px var(--border) + 16px radius -> bg-surface border
//     border-border rounded-2xl
//   14px sans body, --text -> text-sm font-sans text-text-primary
//   bottom-center fixed stack, z 1000 -> fixed inset-x-0 bottom-* z-50 flex
//     flex-col items-center
//   the `toastFade` fade+rise entrance -> the existing `slide-up` keyframe
//     (index.css) via animate-slide-up — same fade + upward translate intent,
//     so NO new keyframe is added.
// Variant accent colors come from the green tokens: success -> primary (green),
// error -> destructive, info -> neutral border/text.

import { createPortal } from 'react-dom';
import { CircleCheck, CircleX, Info, X } from 'lucide-react';

import { cn } from '@/lib/utils';
import { useToast, type Toast as ToastModel, type ToastVariant } from '@/hooks/use-toast';

// Per-variant accent: icon + left accent border tint, mapped to green tokens.
// success = primary (green), error = destructive, info = neutral.
const VARIANT_STYLES: Record<ToastVariant, { icon: typeof Info; iconClass: string }> = {
  success: { icon: CircleCheck, iconClass: 'text-primary' },
  error: { icon: CircleX, iconClass: 'text-destructive' },
  info: { icon: Info, iconClass: 'text-text-secondary' },
};

interface ToastItemProps {
  toast: ToastModel;
  onDismiss: (id: string) => void;
}

/**
 * ToastItem — a single pill. Renders the variant glyph, the message, and a
 * dismiss button with an accessible name. Ports toast.js's surface/border/
 * radius/typography intent into tokens; the fade+rise entrance reuses the
 * `slide-up` keyframe (animate-slide-up) rather than a new `toastFade` keyframe.
 */
function ToastItem({ toast, onDismiss }: ToastItemProps) {
  const { icon: Icon, iconClass } = VARIANT_STYLES[toast.variant];
  // Error toasts assert (interrupt) for screen readers; success/info announce
  // politely. The wrapper region (Toaster) is polite by default; an error item
  // upgrades itself to role="alert" + assertive so it is read promptly.
  const isError = toast.variant === 'error';

  return (
    <div
      data-slot="toast"
      data-variant={toast.variant}
      role={isError ? 'alert' : 'status'}
      aria-live={isError ? 'assertive' : 'polite'}
      className={cn(
        'pointer-events-auto flex w-fit max-w-[min(90vw,28rem)] items-center gap-2.5',
        'rounded-2xl border border-border bg-surface px-4 py-3 shadow-[0_8px_32px_-8px_rgba(0,0,0,0.6)]',
        'animate-slide-up'
      )}
    >
      <Icon aria-hidden="true" className={cn('size-4 shrink-0', iconClass)} />
      <span className="font-sans text-sm leading-relaxed text-text-primary">{toast.message}</span>
      <button
        type="button"
        aria-label="Dismiss notification"
        onClick={() => onDismiss(toast.id)}
        className={cn(
          'ml-1 inline-flex size-5 shrink-0 cursor-pointer items-center justify-center rounded-md',
          'text-text-tertiary transition-colors hover:text-text-primary',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'
        )}
      >
        <X aria-hidden="true" className="size-3.5" />
      </button>
    </div>
  );
}

/**
 * Toaster — the portal container that renders the live toast stack bottom-center
 * (toast.js's fixed bottom-center position, z above content). It is an aria-live
 * region (polite) so screen readers announce queued notifications; error items
 * upgrade themselves to assertive. Mount this ONCE near the app root.
 */
export function Toaster() {
  const { toasts, dismiss } = useToast();

  // No DOM during SSR / before hydration: guard document access.
  if (typeof document === 'undefined') return null;

  return createPortal(
    <div
      data-slot="toaster"
      role="region"
      aria-label="Notifications"
      aria-live="polite"
      className="pointer-events-none fixed inset-x-0 bottom-8 z-50 flex flex-col items-center gap-2 px-4"
    >
      {toasts.map((t) => (
        <ToastItem key={t.id} toast={t} onDismiss={dismiss} />
      ))}
    </div>,
    document.body
  );
}
