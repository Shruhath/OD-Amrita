import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

export type SelectOption = {
  value: string
  label: string
  disabled?: boolean
}

export type SelectProps = {
  /** Mirrors the native attribute so an existing <label htmlFor> keeps working. */
  id?: string
  value: string
  onChange: (value: string) => void
  options: readonly SelectOption[]
  disabled?: boolean
  /** Shown when `value` matches no option. */
  placeholder?: string
  className?: string
  'aria-label'?: string
  'aria-labelledby'?: string
  'aria-describedby'?: string
}

/**
 * The application's dropdown, replacing the browser's `<select>`.
 *
 * WHY THIS EXISTS. A native `<select>` renders its list with the operating
 * system, not the page: the options are drawn by Android, iOS or the desktop
 * shell, ignore every colour in this app, and ignore the dark theme entirely.
 * On a phone the whole thing becomes a full-screen system sheet. There is no CSS
 * that reaches inside it — this is the one control that cannot be styled — so
 * replacing it is the only way for the picker to look like the rest of the app.
 *
 * IT IS BUILT ON THE ARIA COMBOBOX PATTERN, NOT ON DIVS. The trigger keeps
 * `role="combobox"`, so it is still found by the same accessibility queries a
 * native select answers to, and the list is a real `listbox` of `option`s.
 * Keyboard behaviour is implemented rather than inherited, because that is what
 * a native select actually gives you and dropping it would make this a
 * downgrade: arrows move, Home and End jump, typing jumps to a match, Enter and
 * Space commit, Escape cancels, and focus returns to the trigger.
 *
 * `aria-activedescendant` keeps DOM focus on the trigger while the visual
 * highlight moves through the list, which is what lets a screen reader announce
 * the option without the focus ring leaving the control.
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
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(-1)
  const containerRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const listRef = useRef<HTMLUListElement>(null)
  const listboxID = useId()
  const optionID = useCallback((index: number) => `${listboxID}-option-${index}`, [listboxID])

  // Type-ahead buffer. A ref, not state: it must not cause a render, and it is
  // read synchronously inside the key handler that just wrote it.
  const typedRef = useRef({ query: '', at: 0 })

  const selectedIndex = useMemo(
    () => options.findIndex((option) => option.value === value),
    [options, value],
  )
  const selected = selectedIndex >= 0 ? options[selectedIndex] : undefined

  const close = useCallback((returnFocus = true) => {
    setOpen(false)
    setActiveIndex(-1)
    if (returnFocus) triggerRef.current?.focus()
  }, [])

  const openList = useCallback(() => {
    if (disabled || options.length === 0) return
    setOpen(true)
    // Open ON the current value, so arrowing starts where the user is rather
    // than at the top of a long list.
    setActiveIndex(selectedIndex >= 0 ? selectedIndex : 0)
  }, [disabled, options.length, selectedIndex])

  const commit = useCallback(
    (index: number) => {
      const option = options[index]
      if (!option || option.disabled) return
      // Fire only on an actual change, matching a native select — a re-selection
      // of the same value is not a change event there either.
      if (option.value !== value) onChange(option.value)
      close()
    },
    [close, onChange, options, value],
  )

  /** Next selectable index in `step` direction, skipping disabled options. */
  const step = useCallback(
    (from: number, direction: 1 | -1): number => {
      if (options.length === 0) return -1
      let next = from
      for (let moved = 0; moved < options.length; moved += 1) {
        next += direction
        if (next < 0) next = options.length - 1
        if (next >= options.length) next = 0
        if (!options[next]?.disabled) return next
      }
      return from
    },
    [options],
  )

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) close(false)
    }
    document.addEventListener('pointerdown', onPointerDown)
    return () => document.removeEventListener('pointerdown', onPointerDown)
  }, [open, close])

  // Keep the highlighted option in view when the arrows walk past the edge of a
  // scrolling list.
  useEffect(() => {
    if (!open || activeIndex < 0) return
    const node = listRef.current?.querySelector<HTMLElement>(`#${CSS.escape(optionID(activeIndex))}`)
    // Feature-detected: keeping the highlight in view is an enhancement, and a
    // control that threw where scrollIntoView is missing would be unusable
    // rather than merely unscrolled.
    node?.scrollIntoView?.({ block: 'nearest' })
  }, [open, activeIndex, optionID])

  const jumpToTyped = useCallback(
    (character: string, from: number) => {
      const now = Date.now()
      // A pause resets the buffer, so "ab" finds Abhinav while a later "b"
      // finds Bala rather than searching for "abb".
      const typed = now - typedRef.current.at > 700 ? character : typedRef.current.query + character
      typedRef.current = { query: typed, at: now }

      const lowered = typed.toLowerCase()
      // Search AFTER the current position first, so repeatedly pressing the same
      // letter walks through every option starting with it.
      const order = [...options.keys()].slice(from + 1).concat([...options.keys()].slice(0, from + 1))
      const found = order.find(
        (index) => !options[index]?.disabled && options[index]!.label.toLowerCase().startsWith(lowered),
      )
      return found ?? -1
    },
    [options],
  )

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (disabled) return

    if (!open) {
      if (['ArrowDown', 'ArrowUp', 'Enter', ' ', 'Spacebar'].includes(event.key)) {
        event.preventDefault()
        openList()
        return
      }
      // Type-ahead on a closed control changes the value directly, exactly as a
      // native select does.
      if (event.key.length === 1 && !event.metaKey && !event.ctrlKey && !event.altKey) {
        const found = jumpToTyped(event.key, selectedIndex)
        if (found >= 0) {
          event.preventDefault()
          commit(found)
        }
      }
      return
    }

    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        setActiveIndex((current) => step(current < 0 ? selectedIndex : current, 1))
        break
      case 'ArrowUp':
        event.preventDefault()
        setActiveIndex((current) => step(current < 0 ? selectedIndex : current, -1))
        break
      case 'Home':
        event.preventDefault()
        setActiveIndex(step(-1, 1))
        break
      case 'End':
        event.preventDefault()
        setActiveIndex(step(options.length, -1))
        break
      case 'Enter':
      case ' ':
      case 'Spacebar':
        event.preventDefault()
        if (activeIndex >= 0) commit(activeIndex)
        break
      case 'Escape':
        event.preventDefault()
        close()
        break
      case 'Tab':
        // Let focus leave, but do not leave an orphaned list behind it.
        close(false)
        break
      default:
        if (event.key.length === 1 && !event.metaKey && !event.ctrlKey && !event.altKey) {
          const found = jumpToTyped(event.key, activeIndex)
          if (found >= 0) {
            event.preventDefault()
            setActiveIndex(found)
          }
        }
    }
  }

  return (
    <div className={['ui-select', className].filter(Boolean).join(' ')} ref={containerRef}>
      <button
        ref={triggerRef}
        id={id}
        type="button"
        className="ui-select__trigger"
        role="combobox"
        // The stand-in for a native select's `value` property. The trigger shows
        // a LABEL, so without this the chosen id is not present anywhere in the
        // DOM while the list is closed — and the id is what the screen actually
        // acts on. Anything driving or inspecting the control reads it here.
        data-value={value}
        aria-expanded={open}
        aria-controls={listboxID}
        aria-haspopup="listbox"
        aria-activedescendant={open && activeIndex >= 0 ? optionID(activeIndex) : undefined}
        aria-disabled={disabled || undefined}
        disabled={disabled}
        onClick={() => (open ? close() : openList())}
        onKeyDown={onKeyDown}
        {...aria}
      >
        <span className="ui-select__value">{selected?.label ?? placeholder}</span>
        <span className="ui-select__arrow" aria-hidden="true" />
      </button>

      {open ? (
        <ul className="ui-select__list" id={listboxID} role="listbox" ref={listRef} tabIndex={-1}>
          {options.map((option, index) => (
            <li
              key={option.value}
              id={optionID(index)}
              role="option"
              // The native `value` attribute has no equivalent on a listbox
              // option, and the value is what identifies a choice — the label is
              // a display string that can change or repeat. Exposing it keeps a
              // test (and anything else driving the control) able to pick by the
              // id it actually means, rather than by wording.
              data-value={option.value}
              aria-selected={option.value === value}
              aria-disabled={option.disabled || undefined}
              className={[
                'ui-select__option',
                index === activeIndex ? 'is-active' : '',
                option.value === value ? 'is-selected' : '',
                option.disabled ? 'is-disabled' : '',
              ]
                .filter(Boolean)
                .join(' ')}
              // pointerdown, not click: the outside-click listener runs on
              // pointerdown, and a click handler would fire after the list had
              // already been torn down by it.
              onPointerDown={(event) => {
                event.preventDefault()
                commit(index)
              }}
              onPointerEnter={() => setActiveIndex(index)}
            >
              {option.label}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}
