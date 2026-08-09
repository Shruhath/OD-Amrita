import { useState } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, test, vi } from 'vitest'
import { Select, type SelectOption } from './Select'

/**
 * The replacement for `<select>`.
 *
 * A native select gives you keyboard behaviour for free. This one implements it,
 * so these tests are not decoration — they are the difference between a
 * replacement and a downgrade. Every one of them describes something a native
 * select already did.
 */

const OPTIONS: SelectOption[] = [
  { value: 'proc', label: 'Procession' },
  { value: 'float', label: 'Float' },
  { value: 'sports', label: 'Sports' },
  { value: 'arts', label: 'Arts and Crafts' },
]

function Harness({
  options = OPTIONS,
  initial = '',
  onChange,
  disabled,
}: {
  options?: SelectOption[]
  initial?: string
  onChange?: (value: string) => void
  disabled?: boolean
}) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <label htmlFor="category">Category</label>
      <Select
        id="category"
        value={value}
        options={options}
        disabled={disabled}
        onChange={(next) => {
          setValue(next)
          onChange?.(next)
        }}
      />
    </>
  )
}

describe('select', () => {
  test('[P0] is reachable by label and by the combobox role, like the native control', () => {
    render(<Harness initial="float" />)

    const trigger = screen.getByLabelText('Category')
    expect(trigger).toBe(screen.getByRole('combobox'))
    expect(trigger).toHaveTextContent('Float')
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
  })

  test('[P0] opens on click and reports the current value as selected', async () => {
    const user = userEvent.setup()
    render(<Harness initial="float" />)

    await user.click(screen.getByRole('combobox'))

    const list = screen.getByRole('listbox')
    expect(list).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Float' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('option', { name: 'Procession' })).toHaveAttribute('aria-selected', 'false')
  })

  test('[P0] choosing an option reports it once and closes', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Harness initial="float" onChange={onChange} />)

    await user.click(screen.getByRole('combobox'))
    await user.click(screen.getByRole('option', { name: 'Sports' }))

    expect(onChange).toHaveBeenCalledExactlyOnceWith('sports')
    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument())
    expect(screen.getByRole('combobox')).toHaveTextContent('Sports')
  })

  test('[P0] re-picking the current value reports no change, as a native select does not', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Harness initial="float" onChange={onChange} />)

    await user.click(screen.getByRole('combobox'))
    await user.click(screen.getByRole('option', { name: 'Float' }))

    expect(onChange).not.toHaveBeenCalled()
  })

  test('[P0] arrow keys move the highlight and Enter commits', async () => {
    const user = userEvent.setup()
    render(<Harness initial="proc" />)

    const trigger = screen.getByRole('combobox')
    trigger.focus()
    await user.keyboard('{ArrowDown}') // opens, on the current value
    await user.keyboard('{ArrowDown}') // -> Float
    await user.keyboard('{Enter}')

    expect(trigger).toHaveTextContent('Float')
  })

  test('[P0] the highlight is announced through aria-activedescendant', async () => {
    // Without this a screen-reader user hears nothing move: DOM focus stays on
    // the trigger by design, so the highlight is only conveyed by this attribute.
    const user = userEvent.setup()
    render(<Harness initial="proc" />)

    const trigger = screen.getByRole('combobox')
    trigger.focus()
    await user.keyboard('{ArrowDown}{ArrowDown}')

    const active = trigger.getAttribute('aria-activedescendant')
    expect(active).toBeTruthy()
    expect(document.getElementById(active!)).toHaveTextContent('Float')
  })

  test('[P0] Home and End jump to the ends', async () => {
    const user = userEvent.setup()
    render(<Harness initial="float" />)

    const trigger = screen.getByRole('combobox')
    trigger.focus()
    await user.keyboard('{ArrowDown}{End}{Enter}')
    expect(trigger).toHaveTextContent('Arts and Crafts')

    await user.keyboard('{ArrowDown}{Home}{Enter}')
    expect(trigger).toHaveTextContent('Procession')
  })

  test('[P0] typing jumps to a match', async () => {
    const user = userEvent.setup()
    render(<Harness initial="proc" />)

    const trigger = screen.getByRole('combobox')
    trigger.focus()
    // Closed: typing changes the value directly, exactly as a native select does.
    await user.keyboard('s')

    expect(trigger).toHaveTextContent('Sports')
  })

  test('[P0] Escape cancels without changing the value and returns focus', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Harness initial="float" onChange={onChange} />)

    const trigger = screen.getByRole('combobox')
    await user.click(trigger)
    await user.keyboard('{ArrowDown}{Escape}')

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument())
    expect(onChange).not.toHaveBeenCalled()
    expect(trigger).toHaveTextContent('Float')
    expect(trigger).toHaveFocus()
  })

  test('[P0] a click outside closes without choosing', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(
      <>
        <Harness initial="float" onChange={onChange} />
        <button type="button">Elsewhere</button>
      </>,
    )

    await user.click(screen.getByRole('combobox'))
    await user.click(screen.getByRole('button', { name: 'Elsewhere' }))

    await waitFor(() => expect(screen.queryByRole('listbox')).not.toBeInTheDocument())
    expect(onChange).not.toHaveBeenCalled()
  })

  test('[P0] a disabled control does not open', async () => {
    const user = userEvent.setup()
    render(<Harness initial="float" disabled />)

    await user.click(screen.getByRole('combobox'))

    expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
  })

  test('[P0] a disabled option cannot be chosen and is skipped by the arrows', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(
      <Harness
        initial="proc"
        onChange={onChange}
        options={[
          { value: 'proc', label: 'Procession' },
          { value: 'float', label: 'Float', disabled: true },
          { value: 'sports', label: 'Sports' },
        ]}
      />,
    )

    const trigger = screen.getByRole('combobox')
    await user.click(trigger)
    await user.click(screen.getByRole('option', { name: 'Float' }))
    expect(onChange).not.toHaveBeenCalled()

    await user.keyboard('{ArrowDown}{Enter}')
    expect(trigger).toHaveTextContent('Sports')
  })

  test('[P0] a value matching no option shows the placeholder rather than going blank', () => {
    // Reachable for real: an event archived mid-session drops out of the list
    // while its id is still selected.
    render(<Harness initial="an-id-that-no-longer-exists" />)

    expect(screen.getByRole('combobox')).toHaveTextContent('Select…')
  })
})
