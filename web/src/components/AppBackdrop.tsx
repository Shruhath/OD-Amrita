/**
 * The campus photograph, a black mask over it, and a faint dot lattice — the
 * three fixed layers the previous OD Manager app painted behind everything.
 *
 * It is rendered once at the application root and always present. The work
 * surfaces cover it by being opaque (`.portal-shell`); only the auth surfaces,
 * which have no opaque ancestor, let it show through. The image itself is
 * chosen per breakpoint in CSS so a phone never downloads the wide crop.
 */
export function AppBackdrop() {
  return (
    <div className="app-backdrop" aria-hidden="true">
      <div className="app-backdrop__image" />
      <div className="app-backdrop__mask" />
      <div className="app-backdrop__pattern" />
    </div>
  )
}
