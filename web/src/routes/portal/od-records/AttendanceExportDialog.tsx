import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ApiFailure, type AttendanceRecord } from '../../../api/client'
import { useAuth } from '../../../features/auth/context'
import { Button } from '../../../components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '../../../components/ui/dialog'
import {
  EXPORT_PAGE_SIZE,
  ExportTooLarge,
  buildSlotWorkbooks,
  downloadFile,
  readDay,
  slotCounts,
  slotsWithRecords,
  type ExportFile,
} from '../../../features/export/attendanceExport'
import { DownloadSimple } from '@phosphor-icons/react/dist/icons/DownloadSimple'

/**
 * The on-duty export: pick a day, tick the slots, get one workbook per slot.
 *
 * THE SHAPE IS THE OFFICE'S, NOT THE DATABASE'S. What gets handed over is a
 * per-slot list — one period, every vertical in it, four columns — because that
 * is the unit a department checks against its own register. A single file for
 * the whole day would be filtered by hand once per slot before it could be used,
 * and a file per category would multiply the downloads by the number of
 * verticals and leave somebody joining them back together.
 *
 * THE SLOTS OFFERED ARE THE SLOTS THAT EXIST. The picker lists the slots that
 * actually hold marks on the chosen day, with their counts, rather than the
 * numbers 1 to 20 — so an administrator cannot select a slot and receive a file
 * with nothing in it, and can see before downloading that slot 6 has 42 students
 * in it and slot 7 has none.
 *
 * THE DAY IS READ ONCE. Every selected slot is cut from the same read, so the
 * count shown beside a slot and the number of rows in its file are the same
 * number by construction. Re-reading per slot would let a mark landing between
 * two downloads make the picker and the file disagree.
 *
 * WHAT IT CAN SEE IS WHAT THE CALLER CAN SEE. This goes through the ordinary
 * record list, which is already branch-scoped for a teacher and unscoped for an
 * admin — so the export has no access rule of its own to keep in step with the
 * screen's, and a teacher cannot download a student they are not allowed to
 * read.
 */
export function AttendanceExportDialog({
  eventID,
  eventName,
  defaultDate,
  download = downloadFile,
}: {
  eventID: string
  eventName: string
  /** Pre-selects the day the screen is already filtered to, when there is one. */
  defaultDate?: string
  download?: (file: ExportFile) => void
}) {
  const { api } = useAuth()
  const [open, setOpen] = useState(false)
  const [odDate, setODDate] = useState(defaultDate ?? '')
  const [chosen, setChosen] = useState<number[]>([])
  const [failure, setFailure] = useState<string | null>(null)
  const [downloaded, setDownloaded] = useState<string[]>([])

  // Reopening starts from the screen's current day rather than from whatever was
  // picked last time, which is almost never what is wanted the second time.
  useEffect(() => {
    if (open) {
      setODDate(defaultDate ?? '')
      setChosen([])
      setFailure(null)
      setDownloaded([])
    }
  }, [open, defaultDate])

  const day = useQuery<AttendanceRecord[]>({
    queryKey: ['portal', 'records', 'export', eventID, odDate],
    queryFn: () => readDay((page, pageSize) => api.portalRecords(eventID, { odDate, page, pageSize })),
    enabled: open && eventID !== '' && odDate !== '',
    retry: false,
  })

  const records = useMemo(() => day.data ?? [], [day.data])
  const availableSlots = useMemo(() => slotsWithRecords(records), [records])
  const counts = useMemo(() => slotCounts(records), [records])

  // A slot that stopped existing when the day changed must not stay ticked, or
  // the button offers to download a file that cannot be built.
  useEffect(() => {
    setChosen((current) => current.filter((slot) => availableSlots.includes(slot)))
  }, [availableSlots])

  const toggle = (slot: number) => {
    setFailure(null)
    setDownloaded([])
    setChosen((current) => (current.includes(slot) ? current.filter((value) => value !== slot) : [...current, slot]))
  }

  const allChosen = availableSlots.length > 0 && chosen.length === availableSlots.length

  const startDownload = () => {
    setFailure(null)
    try {
      // One file per slot, ascending, so a set of downloads lands in the order
      // the slots ran.
      const files = buildSlotWorkbooks(records, odDate, chosen)
      for (const file of files) download(file)
      setDownloaded(files.map((file) => file.fileName))
    } catch (cause) {
      setFailure(cause instanceof Error ? cause.message : 'The export could not be built.')
    }
  }

  const readFailure = day.error === null || day.error === undefined
    ? null
    : day.error instanceof ExportTooLarge
      ? day.error.message
      : day.error instanceof ApiFailure
        ? day.error.body.message
        : 'The records for this day could not be read.'

  return (
    <>
      <Button
        variant="outline"
        size="sm"
        onClick={() => setOpen(true)}
        disabled={eventID === ''}
        // Named for what it produces. "Export" alone leaves an administrator
        // guessing whether it is the page, the filters or the event.
        aria-label="Export on-duty lists"
      >
        <DownloadSimple size={16} aria-hidden="true" /> Export
      </Button>

      <Dialog open={open} onOpenChange={setOpen}>
        {open && (
          <DialogContent className="max-w-xl">
            <DialogHeader>
              <DialogTitle>Export on-duty lists</DialogTitle>
              <DialogDescription>
                One Excel file per slot for {eventName || 'this event'}, holding the roll number, name, slot and
                category of every student marked present in the categories visible to your account.
              </DialogDescription>
            </DialogHeader>

            <div className="flex flex-col gap-2">
              <label htmlFor="export-day" className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">
                Day
              </label>
              <input
                id="export-day"
                type="date"
                value={odDate}
                onChange={(event) => {
                  setODDate(event.target.value)
                  setDownloaded([])
                  setFailure(null)
                }}
                className="rounded-[10px] border border-border bg-canvas px-3 py-2 text-sm outline-none transition-all focus:border-foreground focus:ring-1 focus:ring-foreground"
              />
            </div>

            <div className="flex flex-col gap-3">
              <div className="flex items-center justify-between gap-3">
                <span className="text-xs font-semibold uppercase tracking-wider text-foreground-muted">Slots</span>
                {availableSlots.length > 0 && (
                  <button
                    type="button"
                    className="text-xs font-semibold text-primary underline-offset-4 hover:underline"
                    onClick={() => {
                      setDownloaded([])
                      setChosen(allChosen ? [] : availableSlots)
                    }}
                  >
                    {allChosen ? 'Clear all' : 'Select all'}
                  </button>
                )}
              </div>

              {odDate === '' && (
                <p className="text-sm text-foreground-muted">Choose a day to see which slots were marked.</p>
              )}

              {odDate !== '' && day.isPending && (
                <p className="flex items-center gap-2 text-sm text-status-info-fg" role="status">
                  <span className="inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent" aria-hidden="true" />
                  Reading this day, {EXPORT_PAGE_SIZE} records at a time…
                </p>
              )}

              {readFailure !== null && (
                <p className="rounded-[10px] border border-status-error-fg/20 bg-status-error-bg p-3 text-sm text-status-error-fg" role="alert">
                  {readFailure}
                </p>
              )}

              {odDate !== '' && day.isSuccess && availableSlots.length === 0 && (
                // Deliberately not an error, and deliberately not an empty list
                // of checkboxes: "no marks on this day" is an answer.
                <p className="text-sm text-foreground-muted">
                  No student was marked present on this day, so there is nothing to export.
                </p>
              )}

              {availableSlots.length > 0 && (
                <ul className="grid grid-cols-2 gap-2 sm:grid-cols-3">
                  {availableSlots.map((slot) => (
                    <li key={slot}>
                      <label className="flex min-h-11 cursor-pointer items-center gap-2 rounded-[10px] border border-border bg-canvas px-3 py-2 text-sm has-[:checked]:border-primary has-[:checked]:bg-accent">
                        <input
                          type="checkbox"
                          checked={chosen.includes(slot)}
                          onChange={() => toggle(slot)}
                          aria-label={`Slot ${slot}, ${counts.get(slot) ?? 0} students`}
                        />
                        <span className="font-semibold">Slot {slot}</span>
                        <span className="ml-auto text-xs text-foreground-muted">{counts.get(slot) ?? 0}</span>
                      </label>
                    </li>
                  ))}
                </ul>
              )}
            </div>

            {failure !== null && (
              <p className="rounded-[10px] border border-status-error-fg/20 bg-status-error-bg p-3 text-sm text-status-error-fg" role="alert">
                {failure}
              </p>
            )}

            {downloaded.length > 0 && (
              // Named individually rather than counted. A browser that silently
              // blocked the second and third of three downloads looks exactly
              // like a browser that saved them, and the file names are what an
              // administrator then goes looking for.
              <p className="rounded-[10px] border border-status-success-fg/20 bg-status-success-bg p-3 text-sm text-status-success-fg" role="status">
                Downloaded {downloaded.length} file{downloaded.length === 1 ? '' : 's'}: {downloaded.join(', ')}
              </p>
            )}

            <div className="flex flex-wrap justify-end gap-3">
              <Button variant="secondary" onClick={() => setOpen(false)}>Close</Button>
              <Button onClick={startDownload} disabled={chosen.length === 0}>
                <DownloadSimple size={16} aria-hidden="true" />
                {chosen.length <= 1 ? 'Download' : `Download ${chosen.length} files`}
              </Button>
            </div>
          </DialogContent>
        )}
      </Dialog>
    </>
  )
}
