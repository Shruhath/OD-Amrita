import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { publicEmptyDayAttendance } from '../../tests/support/factories/public-attendance'

/**
 * The shared mock server.
 *
 * One DEFAULT handler: the public attendance board. It renders unconditionally
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
)
