import type { Branch } from '../api/client'

/**
 * A branch, shown as its code alone.
 *
 * Full names are what a branch list is for on paper, but in this UI a single
 * event can carry nine of them — "Artificial Intelligence and Medical
 * Engineering" nine times over pushes the rest of the row off screen and turns
 * a table cell into a paragraph. The code is the working vocabulary here.
 *
 * `<abbr>` rather than a span: this IS an abbreviation with an expansion, so
 * the element that means that gives a native hover tooltip and lets assistive
 * tech reach the full name — nothing is lost by shortening the visible label.
 *
 * Shared rather than repeated, because this markup previously existed
 * identically in three screens and could drift in any of them.
 */
export function BranchChip({ branch }: { branch: Pick<Branch, 'code' | 'name'> }) {
  return (
    <abbr className="branch-chip" title={branch.name}>
      {branch.code}
    </abbr>
  )
}
