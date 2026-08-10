import type { Locator, Page } from '@playwright/test'

/**
 * Reads the contrast of the controls a person actually has to see.
 *
 * WHY IT LIVES HERE. Two suites need it — the front door's own journey and the
 * app-wide sweep — and the hard part is not the WCAG formula but working out
 * what colour is really behind a control. Getting that wrong in two places
 * independently is how a measurement starts reporting confidently wrong numbers.
 *
 * BACKGROUNDS ARE COMPOSITED, NOT TAKEN AS FOUND. The first version of this took
 * the nearest ancestor whose background was not fully transparent and used it as
 * given — which read the navigation rail's `rgba(23, 32, 43, 0.04)` as though it
 * were near-black, and reported seven perfectly legible links as 3.07:1
 * failures. A 4%-opaque dark wash over white IS white, near enough, and the only
 * way to know that is to blend it over what is behind it. Every layer is now
 * composited upward until one is opaque.
 */

export type ContrastFinding = {
  label: string
  contrast: number
  color: string
  background: string
}

/** WCAG AA for ordinary text. */
export const AA_CONTRAST = 4.5

/**
 * The browser-side implementation, as a string so it can be handed to
 * `page.evaluate` from either suite without bundling.
 */
const MEASURE = `(() => {
  const parse = (value) => {
    const parts = (value.match(/[\\d.]+/g) ?? []).map(Number)
    return { r: parts[0] ?? 0, g: parts[1] ?? 0, b: parts[2] ?? 0, a: parts.length > 3 ? parts[3] : 1 }
  }
  const over = (top, bottom) => ({
    r: top.r * top.a + bottom.r * (1 - top.a),
    g: top.g * top.a + bottom.g * (1 - top.a),
    b: top.b * top.a + bottom.b * (1 - top.a),
    a: 1,
  })
  // Every background from the element up, blended in paint order. Stops at the
  // first opaque layer, and falls back to white the way a browser does.
  const painted = (element) => {
    const stack = []
    for (let node = element; node !== null; node = node.parentElement) {
      const colour = parse(getComputedStyle(node).backgroundColor)
      if (colour.a === 0) continue
      stack.push(colour)
      if (colour.a === 1) break
    }
    let result = stack.length > 0 && stack[stack.length - 1].a === 1
      ? stack.pop()
      : { r: 255, g: 255, b: 255, a: 1 }
    for (let index = stack.length - 1; index >= 0; index -= 1) result = over(stack[index], result)
    return result
  }
  const luminance = (colour) => {
    const channel = (value) => {
      const ratio = value / 255
      return ratio <= 0.03928 ? ratio / 12.92 : Math.pow((ratio + 0.055) / 1.055, 2.4)
    }
    return 0.2126 * channel(colour.r) + 0.7152 * channel(colour.g) + 0.0722 * channel(colour.b)
  }
  const ratio = (foreground, background) => {
    // Text can be translucent too, and it paints over the same ground.
    const solid = foreground.a === 1 ? foreground : over(foreground, background)
    const first = luminance(solid)
    const second = luminance(background)
    const lighter = Math.max(first, second)
    const darker = Math.min(first, second)
    return (lighter + 0.05) / (darker + 0.05)
  }
  const show = (colour) => 'rgb(' + [colour.r, colour.g, colour.b].map((value) => Math.round(value)).join(', ') + ')'

  return { parse, painted, ratio, show }
})()`

/**
 * Contrast of one control's label against what is painted behind it.
 *
 * Takes a LOCATOR rather than a selector string, so the caller can address the
 * control the way the rest of the suite does — by role and accessible name. The
 * first version took a string and ran it through `querySelector`, which quietly
 * rejects Playwright's own `:has-text()` and threw from inside the browser.
 */
export async function labelContrast(control: Locator): Promise<number> {
  return control.evaluate((element, source) => {
    const helpers = (0, eval)(source) as {
      parse: (value: string) => { r: number; g: number; b: number; a: number }
      painted: (node: Element) => { r: number; g: number; b: number; a: number }
      ratio: (f: { r: number; g: number; b: number; a: number }, b: { r: number; g: number; b: number; a: number }) => number
    }
    return helpers.ratio(helpers.parse(getComputedStyle(element).color), helpers.painted(element))
  }, MEASURE)
}

/**
 * Every visible, ENABLED control on the page whose label falls below AA.
 *
 * Disabled controls are skipped deliberately: they are faded on purpose, and
 * holding them to the same bar teaches the next person to lower the threshold
 * for everything. Icon-only controls are skipped too — they have no label to
 * measure, and their legibility is a different question from this one.
 */
export async function unreadableControls(page: Page): Promise<ContrastFinding[]> {
  return page.evaluate(
    ([source, threshold]) => {
      const helpers = (0, eval)(source as string) as {
        parse: (value: string) => { r: number; g: number; b: number; a: number }
        painted: (element: Element) => { r: number; g: number; b: number; a: number }
        ratio: (f: { r: number; g: number; b: number; a: number }, b: { r: number; g: number; b: number; a: number }) => number
        show: (colour: { r: number; g: number; b: number; a: number }) => string
      }
      const findings: { label: string; contrast: number; color: string; background: string }[] = []

      for (const node of document.querySelectorAll('button, a[href], [role="button"]')) {
        const element = node as HTMLElement
        if (element.hasAttribute('disabled') || element.getAttribute('aria-disabled') === 'true') continue
        const style = getComputedStyle(element)
        if (style.visibility === 'hidden' || style.display === 'none') continue
        if (Number(style.opacity) < 0.9) continue
        const box = element.getBoundingClientRect()
        if (box.width < 8 || box.height < 8) continue
        const label = (element.textContent ?? '').trim()
        if (label === '') continue

        const background = helpers.painted(element)
        const contrast = helpers.ratio(helpers.parse(style.color), background)
        if (contrast < (threshold as number)) {
          findings.push({
            label: label.slice(0, 60),
            contrast: Math.round(contrast * 100) / 100,
            color: style.color,
            background: helpers.show(background),
          })
        }
      }
      return findings
    },
    [MEASURE, AA_CONTRAST] as const,
  )
}
