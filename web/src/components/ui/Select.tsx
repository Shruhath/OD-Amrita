import { useEffect, useId, useMemo, useState, type KeyboardEvent } from 'react'
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
    const hasEmptyOption = options.some((option) => option.value === '')
    return hasEmptyOption ? options : [{ value: '', label: placeholder, disabled: true }, ...options]
  }, [options, placeholder])
  const selectedIndex = Math.max(0, renderedOptions.findIndex((option) => option.value === value))
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(selectedIndex)
  const selectID = useId()
  const optionID = (index: number) => `${selectID}-option-${index}`
  const nextEnabled = (from: number, direction: 1 | -1) => {
    let next = from
    for (let count = 0; count < renderedOptions.length; count += 1) {
      next = (next + direction + renderedOptions.length) % renderedOptions.length
      if (!renderedOptions[next]?.disabled) return next
    }
    return from
  }
  const handleKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'ArrowDown') setActiveIndex((current) => nextEnabled(current, 1))
    if (event.key === 'ArrowUp') setActiveIndex((current) => nextEnabled(current, -1))
    if (event.key === 'Home') setActiveIndex(nextEnabled(-1, 1))
    if (event.key === 'End') setActiveIndex(nextEnabled(0, -1))
  }

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
    if (!open) setActiveIndex(selectedIndex)
  }, [open, selectedIndex])

  return (
    <RadixSelect
      open={open}
      onOpenChange={setOpen}
      value={selectedValue}
      onValueChange={(next) => onChange(next === emptyValue ? '' : next)}
      disabled={disabled}
    >
      <RadixSelectTrigger id={id} aria-label={aria['aria-label']} aria-labelledby={aria['aria-labelledby']} aria-describedby={aria['aria-describedby']} aria-activedescendant={open ? optionID(activeIndex) : undefined} onKeyDown={handleKeyDown} className={className}>
        <RadixSelectValue placeholder={placeholder} />
      </RadixSelectTrigger>
      <RadixSelectContent>
        {renderedOptions.map((option) => {
          const optionValue = option.value || emptyValue
          return <RadixSelectItem key={optionValue} id={optionID(renderedOptions.indexOf(option))} value={optionValue} disabled={option.disabled} onPointerDown={(event) => { if (option.disabled) { event.preventDefault(); setOpen(false) } }}>{option.label}</RadixSelectItem>
        })}
      </RadixSelectContent>
    </RadixSelect>
  )
}
