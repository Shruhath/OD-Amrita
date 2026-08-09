import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cn } from '../../lib/utils'

export type ButtonProps = React.ButtonHTMLAttributes<HTMLButtonElement> & {
  asChild?: boolean
  variant?: 'default' | 'secondary' | 'outline' | 'ghost' | 'destructive'
  size?: 'default' | 'sm' | 'icon'
}

const variants = {
  default: 'bg-primary text-primary-foreground border-primary hover:brightness-95',
  secondary: 'bg-surface-muted text-foreground border-border hover:bg-surface-raised',
  outline: 'bg-surface text-foreground border-border hover:bg-surface-muted',
  ghost: 'bg-transparent text-foreground border-transparent hover:bg-surface-muted',
  destructive: 'bg-status-error-fg text-white border-status-error-fg hover:brightness-95',
}

const sizes = {
  default: 'min-h-11 px-4 py-2.5',
  sm: 'min-h-9 px-3 py-2 text-sm',
  icon: 'h-11 w-11 p-0',
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ asChild = false, className, variant = 'default', size = 'default', type = 'button', ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return <Comp ref={ref} type={asChild ? undefined : type} className={cn('inline-flex items-center justify-center gap-2 rounded-[10px] border font-semibold transition-colors active:translate-y-px disabled:pointer-events-none disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary', variants[variant], sizes[size], className)} {...props} />
  },
)
Button.displayName = 'Button'
