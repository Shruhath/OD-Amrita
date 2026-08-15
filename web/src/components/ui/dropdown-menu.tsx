import * as React from 'react'
import * as DropdownMenuPrimitive from '@radix-ui/react-dropdown-menu'
import { cn } from '../../lib/utils'

export const DropdownMenu = DropdownMenuPrimitive.Root
export const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger

export const DropdownMenuContent = ({ className, sideOffset = 6, collisionPadding = 12, ...props }: React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.Content> & { sideOffset?: number }) => (
  <DropdownMenuPrimitive.Portal>
    <DropdownMenuPrimitive.Content sideOffset={sideOffset} collisionPadding={collisionPadding} className={cn('z-[100] min-w-48 overflow-hidden rounded-[10px] border border-border bg-surface p-1.5 text-foreground shadow-[0_16px_36px_rgb(0_0_0/0.14)]', className)} {...props} />
  </DropdownMenuPrimitive.Portal>
)

export const DropdownMenuItem = ({ className, inset, ...props }: React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.Item> & { inset?: boolean }) => (
  <DropdownMenuPrimitive.Item className={cn('flex min-h-10 cursor-pointer items-center rounded-[8px] px-3 py-2 text-sm outline-none focus:bg-accent focus:text-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50', inset && 'pl-8', className)} {...props} />
)

export const DropdownMenuLabel = ({ className, ...props }: React.ComponentPropsWithoutRef<typeof DropdownMenuPrimitive.Label>) => (
  <DropdownMenuPrimitive.Label className={cn('px-3 py-2 text-xs font-semibold uppercase tracking-wider text-foreground-muted', className)} {...props} />
)
