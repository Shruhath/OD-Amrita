import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { RadixSelect, RadixSelectContent, RadixSelectItem, RadixSelectTrigger, RadixSelectValue } from './select-radix'

export type SelectOption = {
  value: string
  label: string
  disabled?: boolean
}

export type SelectProps = {
  id?: string
  value: string
  onChange: (value: string) => void
  options: readonly SelectOption[]
  disabled?: boolean
  placeholder?: string
  className?: string
  'aria-label'?: string
  'aria-labelledby'?: string
  'aria-describedby'?: string
}

/**
 * Website-wide select primitive. Every dropdown uses Radix Select so the
 * trigger, portal, collision handling, keyboard navigation, outside-close,
 * focus return, and dark/light styling stay identical across the app.
 */
export function Select({
  id,
  value,
  onChange,
  options,
  disabled = false,
  placeholder = 'Select…',
  className,
  ...aria
}: SelectProps) {
  const emptyValue = '__empty__'
  const hasValue = options.some((option) => option.value === value)
  const selectedValue = hasValue && value ? value : emptyValue
  const renderedOptions = useMemo(() => {
    if (value && hasValue) return options
    if (options.some((option) => option.value === '')) return options
    return [{ value: '', label: placeholder, disabled: true }, ...options]
  }, [hasValue, options, placeholder, value])
  const selectedIndex = Math.max(0, renderedOptions.findIndex((option) => option.value === value))
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(selectedIndex)
  const activeIndexRef = useRef(selectedIndex)
  const selectID = useId()
  const triggerRef = useRef<HTMLButtonElement>(null)
  const optionID = (index: number) => `${selectID}-option-${index}`
  const nextEnabled = useCallback((from: number, direction: 1 | -1) => {
    let next = from
    for (let count = 0; count < renderedOptions.length; count += 1) {
      next = (next + direction + renderedOptions.length) % renderedOptions.length
      if (!renderedOptions[next]?.disabled) return next
    }
    return from
  }, [renderedOptions])
  const handleKeyDown = useCallback((event: KeyboardEvent) => {
    const keepKeyboardFocus = () => window.setTimeout(() => triggerRef.current?.focus(), 0)
    const move = (direction: 1 | -1, start = activeIndex) => {
      event.preventDefault()
      event.stopPropagation()
      setOpen(true)
      keepKeyboardFocus()
      // The first arrow key opens the list at the current value, matching a
      // native select. Subsequent arrows move the active option.
      const next = open ? nextEnabled(start, direction) : selectedIndex
      activeIndexRef.current = next
      setActiveIndex(next)
    }

    if (event.key === 'ArrowDown') return move(1)
    if (event.key === 'ArrowUp') return move(-1)
    if (event.key === 'Home') {
      event.preventDefault()
      event.stopPropagation()
      setOpen(true)
      keepKeyboardFocus()
      const next = nextEnabled(-1, 1)
      activeIndexRef.current = next
      setActiveIndex(next)
      return
    }
    if (event.key === 'End') {
      event.preventDefault()
      event.stopPropagation()
      setOpen(true)
      keepKeyboardFocus()
      const next = nextEnabled(0, -1)
      activeIndexRef.current = next
      setActiveIndex(next)
      return
    }
    if (event.key === 'Enter' || event.key === ' ') {
      if (!open) return
      event.preventDefault()
      event.stopPropagation()
      const option = renderedOptions[activeIndexRef.current]
      if (option && !option.disabled) {
        onChange(option.value)
        setOpen(false)
      }
      return
    }
    if (event.key === 'Escape' && open) {
      event.preventDefault()
      event.stopPropagation()
      setOpen(false)
      return
    }

    if (!open && event.key.length === 1 && /\S/.test(event.key)) {
      const match = renderedOptions.find((option) => !option.disabled && option.label.toLowerCase().startsWith(event.key.toLowerCase()))
      if (match) {
        event.preventDefault()
        event.stopPropagation()
        onChange(match.value)
      }
    }
  }, [activeIndex, nextEnabled, onChange, open, renderedOptions, selectedIndex])

  useEffect(() => {
    if (open) {
      document.body.dataset.selectOpen = 'true'
      const restorePointerEvents = window.setTimeout(() => {
        if (document.body.dataset.selectOpen === 'true') document.body.style.pointerEvents = ''
      }, 0)
      return () => window.clearTimeout(restorePointerEvents)
    }
    else delete document.body.dataset.selectOpen
    return () => { delete document.body.dataset.selectOpen }
  }, [open])

  useEffect(() => {
    if (!open) {
      activeIndexRef.current = selectedIndex
      setActiveIndex(selectedIndex)
    }
  }, [open, selectedIndex])

  useEffect(() => {
    if (!open) return
    const onDocumentKeyDown = (event: globalThis.KeyboardEvent) => {
      if (document.activeElement === triggerRef.current) return
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' ', 'Escape'].includes(event.key)) return
      triggerRef.current?.focus()
      handleKeyDown(event as unknown as KeyboardEvent)
    }
    document.addEventListener('keydown', onDocumentKeyDown, true)
    return () => document.removeEventListener('keydown', onDocumentKeyDown, true)
  }, [open, activeIndex, selectedIndex, renderedOptions, handleKeyDown])

  return (
    <RadixSelect
      open={open}
      onOpenChange={setOpen}
      value={selectedValue}
      onValueChange={(next) => onChange(next === emptyValue ? '' : next)}
      disabled={disabled}
    >
      <RadixSelectTrigger ref={triggerRef} id={id} data-value={value} aria-label={aria['aria-label']} aria-labelledby={aria['aria-labelledby']} aria-describedby={aria['aria-describedby']} aria-activedescendant={open ? optionID(activeIndex) : undefined} onKeyDownCapture={handleKeyDown} className={className}>
        <RadixSelectValue placeholder={placeholder} />
      </RadixSelectTrigger>
      <RadixSelectContent>
        {renderedOptions.map((option) => {
          const optionValue = option.value || emptyValue
          return <RadixSelectItem
            key={optionValue}
            id={optionID(renderedOptions.indexOf(option))}
            value={optionValue}
            disabled={option.disabled}
            aria-disabled={option.disabled || undefined}
            data-disabled={option.disabled ? '' : undefined}
            className={option.disabled ? 'pointer-events-auto cursor-not-allowed opacity-50' : undefined}
            onPointerDown={(event) => {
              if (option.disabled) {
                event.preventDefault()
                triggerRef.current?.focus()
              }
            }}
            onSelect={(event) => {
              if (option.disabled) {
                event.preventDefault()
                triggerRef.current?.focus()
              }
            }}
          >
            {option.label}
          </RadixSelectItem>
        })}
      </RadixSelectContent>
    </RadixSelect>
  )
}
