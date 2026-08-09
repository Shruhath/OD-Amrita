import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiFailure } from '../../../api/client'
import { Spinner } from '../../../components/ui/Spinner'
import { useAuth } from '../../../features/auth/context'

/**
 * The evening-slots switch.
 *
 * WHAT IT IS FOR. The teaching day is slots 1-12 and ends at 18:15, so after
 * quarter past six no slot is open and a coordinator cannot mark anybody. That
 * makes it impossible to rehearse the real marking flow in the evening — which
 * is exactly when there is time to rehearse it. Turning this on admits slots
 * 13-20, evening windows of forty minutes each, ending at 23:35.
 *
 * IT DEFAULTS OFF AND SAYS SO. The screen states the ceiling currently in force
 * rather than implying it, because the difference between 12 and 20 is not
 * visible anywhere else until a coordinator is refused.
 *
 * THE CEILING COMES FROM THE SERVER, never from a constant here. A screen that
 * hardcoded 20 would silently disagree with the API the day that number changed,
 * and the disagreement would surface as a slot the picker offers and the server
 * refuses — which reads to a coordinator as the app being broken.
 */
export default function ExtendedSlotsToggle() {
  const { api } = useAuth()
  const queryClient = useQueryClient()

  const setting = useQuery({
    queryKey: ['admin', 'settings', 'extended-slots'],
    queryFn: () => api.extendedSlotsSetting(),
  })

  const update = useMutation({
    mutationFn: (enabled: boolean) => api.setExtendedSlots(enabled),
    onSuccess: (result) => {
      // Written straight into the cache rather than invalidated, so the switch
      // does not flick back to its old position for the length of a refetch —
      // on a control whose whole job is to say what is in force, that reads as
      // the click having failed.
      queryClient.setQueryData(['admin', 'settings', 'extended-slots'], result)
      // The marking screens hold their own slot lists derived from this.
      void queryClient.invalidateQueries({ queryKey: ['portal', 'mark'] })
    },
  })

  const enabled = setting.data?.enabled ?? false
  const maxSlot = setting.data?.max_slot

  return (
    <section className="detail-section">
      <div className="page-heading">
        <div>
          <h2>Evening slots</h2>
          <p>
            The teaching day ends at 18:15, so slots 1–12 are all that exist during it. Turning this
            on adds slots 13–20 — forty minutes each, through to 23:35 — so marking can be exercised
            outside teaching hours.
          </p>
        </div>
      </div>

      {setting.isPending && <Spinner label="Loading the setting" size="small" />}

      {setting.isError && (
        <div className="notice error" role="alert">
          <span>
            {setting.error instanceof ApiFailure
              ? setting.error.body.message
              : 'The setting could not be loaded.'}
          </span>
          <button type="button" onClick={() => void setting.refetch()}>Try again</button>
        </div>
      )}

      {setting.isSuccess && (
        <>
          <label className="switch-row">
            <input
              type="checkbox"
              role="switch"
              checked={enabled}
              disabled={update.isPending}
              onChange={(event) => update.mutate(event.target.checked)}
            />
            <span>Allow slots 13–20</span>
          </label>

          {/*
            The state in FORCE, in words. A checkbox alone says what was clicked,
            not what the server accepted — and those differ for as long as the
            request is in flight, and permanently if it failed.
          */}
          <p className="empty-note" role="status">
            {update.isPending
              ? 'Saving…'
              : `Slots 1–${maxSlot ?? (enabled ? 20 : 12)} are currently accepted.`}
          </p>

          {update.isError && (
            <p className="notice error" role="alert">
              {update.error instanceof ApiFailure
                ? update.error.body.message
                : 'The setting could not be saved.'}
            </p>
          )}
        </>
      )}
    </section>
  )
}
