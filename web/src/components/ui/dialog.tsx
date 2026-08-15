import * as React from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
// Icons are imported ONE PER MODULE rather than from the package barrel.
// The barrel pulls the whole set through Vite's dependency optimiser, which
// produces a single ~7 MB module the dev server then refuses to parse — every
// browser journey fails at once, each screenshotting a page still on its
// spinner. Per-icon paths keep the dev server honest and the chunk small.
import { X } from '@phosphor-icons/react/dist/icons/X'
import { cn } from '../../lib/utils'

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogClose = DialogPrimitive.Close

export function DialogContent({ className, children, ...props }: React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-foreground/35 backdrop-blur-sm data-[state=open]:animate-in data-[state=closed]:animate-out" />
      <DialogPrimitive.Content className={cn('fixed left-1/2 top-1/2 z-50 grid max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 gap-4 overflow-y-auto rounded-[10px] border border-border bg-surface p-6 text-foreground shadow-[0_24px_80px_rgb(0_0_0/0.22)]', className)} {...props}>
        {children}
        <DialogPrimitive.Close className="absolute right-4 top-4 rounded-[8px] p-2 text-foreground-muted transition-colors hover:bg-surface-muted hover:text-foreground focus-visible:outline-2 focus-visible:outline-primary" aria-label="Close dialog">
          <X size={18} aria-hidden="true" />
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

export const DialogHeader = ({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) => <div className={cn('flex flex-col gap-1.5', className)} {...props} />
export const DialogTitle = ({ className, ...props }: React.ComponentPropsWithoutRef<typeof DialogPrimitive.Title>) => <DialogPrimitive.Title className={cn('text-lg font-semibold tracking-tight', className)} {...props} />
export const DialogDescription = ({ className, ...props }: React.ComponentPropsWithoutRef<typeof DialogPrimitive.Description>) => <DialogPrimitive.Description className={cn('text-sm leading-relaxed text-foreground-muted', className)} {...props} />
