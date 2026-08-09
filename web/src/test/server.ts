import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { publicEmptyDayAttendance } from '../../tests/support/factories/public-attendance'

/**
 * The shared mock server.
 *
 * DEFAULT handlers for the two reads that happen on screens whose tests are
 * about something else — the public attendance board, and the evening-slots
 * switch. It renders unconditionally
 * on `/attendance`, so without a default every suite that touches that page —
 * including the ones about the dispute form and the search box — would show the
 * board's failure alert and collide with their own `role="alert"` locators.
 *
 * The default is an EMPTY day rather than a populated one, so a test that means
 * to assert something about the board has to say so by installing its own
 * handler. A populated default would let a board assertion pass on fixture data
 * the test never chose.
 */
export const server = setupServer(
  http.get('*/api/v1/public/od-records', () => HttpResponse.json(publicEmptyDayAttendance())),
  // The evening-slots switch, read by the events workspace and by the
  // teacher/admin marking screen. Defaulted OFF — the same value a freshly
  // migrated database seeds — so a test that means to assert something about it
  // has to install its own handler and say so.
  http.get('*/api/v1/admin/settings/extended-slots', () =>
    HttpResponse.json({ enabled: false, max_slot: 12, updated_at: null }),
  ),
  // The card populator's DURABLE progress. It loads unconditionally whenever a
  // section is opened, so without a default every populator test about something
  // else would render the "saved progress could not be loaded" alert and collide
  // with its own role="alert" locator.
  //
  // The default is an EMPTY section — no cards issued, none verified — so a test
  // that means to assert something about saved progress has to install its own
  // handler and say so. A populated default would let a count assertion pass on
  // fixture data the test never chose.
  http.get('*/api/v1/admin/card-tokens/status', () =>
    HttpResponse.json({ data: [], total: 0, issued: 0, verified: 0 }),
  ),
)
