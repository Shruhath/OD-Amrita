import * as React from 'react'
import * as SelectPrimitive from '@radix-ui/react-select'
import { Check, CaretDown, CaretUp } from '@phosphor-icons/react'
import { cn } from '../../lib/utils'

export const RadixSelect = SelectPrimitive.Root
export const RadixSelectValue = SelectPrimitive.Value
export const RadixSelectGroup = SelectPrimitive.Group
export const RadixSelectLabel = ({ className, ...props }: React.ComponentPropsWithoutRef<typeof SelectPrimitive.Label>) => <SelectPrimitive.Label className={cn('px-3 py-2 text-xs font-semibold uppercase tracking-wider text-foreground-muted', className)} {...props} />

export const RadixSelectTrigger = ({ className, children, ...props }: React.ComponentPropsWithoutRef<typeof SelectPrimitive.Trigger>) => (
  <SelectPrimitive.Trigger className={cn('flex min-h-11 w-full items-center justify-between gap-2 rounded-[10px] border border-border bg-surface px-3 py-2 text-left text-sm text-foreground outline-none transition-[border-color,box-shadow,background-color] duration-200 hover:border-border-strong focus-visible:border-primary focus-visible:ring-2 focus-visible:ring-primary/20 data-[state=open]:border-primary data-[state=open]:bg-surface-raised disabled:pointer-events-none disabled:opacity-50', className)} {...props}>
    {children}<SelectPrimitive.Icon><CaretDown size={16} aria-hidden="true" /></SelectPrimitive.Icon>
  </SelectPrimitive.Trigger>
)

export function RadixSelectContent({ className, children, position = 'popper', ...props }: React.ComponentPropsWithoutRef<typeof SelectPrimitive.Content>) {
  return <SelectPrimitive.Portal><SelectPrimitive.Content position={position} align="center" sideOffset={6} collisionPadding={12} className={cn('z-[100] max-h-72 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-[10px] border border-border bg-surface text-foreground shadow-[0_2px_10px_rgb(0_0_0/0.12)] data-[state=open]:animate-in data-[state=closed]:animate-out', className)} {...props}><SelectPrimitive.ScrollUpButton className="flex h-6 items-center justify-center text-foreground-muted"><CaretUp size={14} /></SelectPrimitive.ScrollUpButton><SelectPrimitive.Viewport className="p-1.5">{children}</SelectPrimitive.Viewport><SelectPrimitive.ScrollDownButton className="flex h-6 items-center justify-center text-foreground-muted"><CaretDown size={14} /></SelectPrimitive.ScrollDownButton></SelectPrimitive.Content></SelectPrimitive.Portal>
}

export const RadixSelectItem = ({ className, children, ...props }: React.ComponentPropsWithoutRef<typeof SelectPrimitive.Item>) => (
  <SelectPrimitive.Item className={cn('relative flex min-h-10 cursor-pointer select-none items-center rounded-[10px] py-2 pl-8 pr-3 text-sm outline-none transition-colors duration-150 focus:bg-accent focus:text-accent-foreground data-[highlighted]:bg-accent data-[highlighted]:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50', className)} {...props}><SelectPrimitive.ItemIndicator className="absolute left-2 inline-flex items-center"><Check size={15} aria-hidden="true" /></SelectPrimitive.ItemIndicator><SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText></SelectPrimitive.Item>
)
