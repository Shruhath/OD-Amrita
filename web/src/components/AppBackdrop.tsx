/**
 * The campus photograph, a mask over it, and a faint dot lattice — the three
 * fixed layers the previous OD Manager app painted behind everything.
 *
 * Rendered once at the application root and always present. Every shell above
 * it is transparent and every panel is translucent, so it is the ground for the
 * whole app — administration, marking and sign-in alike. The image is chosen
 * per breakpoint in CSS so a phone never downloads the wide crop, and it is
 * blurred in CSS rather than baked: the supplied crops have lettering across
 * the middle of the frame that would otherwise compete with the content.
 */
export function AppBackdrop() {
  return null
}
