/**
 * ATDD RED PHASE — Story 1.9, Task 2: the authenticated round-trip screen.
 *
 * These tests FAIL until `../RoundTripScreen` exists.
 *
 * WHY A RENDER TEST AT ALL (Decision D4): the 1.6 house rule is "logic in port-injected modules,
 * tested headless" — and it holds for everything else in this story. But AC 2 is an assertion
 * ABOUT THE SCREEN: "the screen renders values that can only have come from the backend."
 * That is not provable against a fake in a unit test; something has to actually render.
 *
 * THE LOAD-BEARING ASSERTION is 1.9-COMP-001: the screen displays the `role` from the server's
 * response. The device CANNOT compute the role — it is resolved server-side by `ResolveRole`
 * against `AUTH_BOOTSTRAP_ADMINS`. So a role on screen is proof the response is real.
 * Per AC 2, verbatim: "A hardcoded 'Success ✅' is an AC failure." These tests are what make
 * that sentence enforceable.
 *
 * SEAM CONTRACT these tests pin (the dev implements exactly this shape):
 *
 *   <RoundTripScreen apiRequest={...} onSignOut={...} />
 *
 * `apiRequest` is the INJECTED port with the same signature as the real one in `src/api/client.ts`
 * — `(path) => Promise<Response>`. The screen must consume the existing typed client (which owns
 * the 401 interceptor); it must NOT open a second fetch path. Injecting the port here is what
 * keeps the test off the network — it is not permission to write a new transport.
 */

import { render, screen, userEvent } from '@testing-library/react-native';

import { RoundTripScreen } from '../RoundTripScreen';

// ---------------------------------------------------------------------------
// Test doubles — real-shaped payloads, per data-factories.md (no hardcoded literals inline)
// ---------------------------------------------------------------------------

/** A real-shaped `AuthenticatedIdentity` — the 200 body of GET /api/v1/auth/me. */
function authenticatedIdentity(overrides: Partial<Record<string, unknown>> = {}) {
  return {
    email: 'cb.sc.u4cse24124@cb.students.amrita.edu',
    name: 'Test Student',
    subject: 'supabase-sub-abc123',
    role: 'student',
    ...overrides,
  };
}

/** The contract's `{code, message}` error envelope. */
function errorEnvelope(code: string, message: string) {
  return { code, message };
}

/** A fake `apiRequest` port returning a scripted JSON response. Records every path requested. */
function fakeApiRequest(status: number, body: unknown) {
  const calls: string[] = [];
  const apiRequest = async (path: string): Promise<Response> => {
    calls.push(path);
    return new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    });
  };
  return { apiRequest, calls };
}

// ---------------------------------------------------------------------------
// 1.9-COMP-001 [P0] — THE acceptance criterion: the role came from the server
// ---------------------------------------------------------------------------

test('[P0] the screen renders the server-resolved role — the value the device cannot compute', async () => {
  const user = userEvent.setup();
  const { apiRequest } = fakeApiRequest(200, authenticatedIdentity({ role: 'admin' }));

  render(<RoundTripScreen apiRequest={apiRequest} onSignOut={jest.fn()} />);
  await user.press(screen.getByRole('button', { name: /round trip/i }));

  // `admin` is used deliberately: it is NOT the default role. A screen that hardcoded a value,
  // or echoed something the device already knew, cannot produce it. Only the server can.
  expect(await screen.findByText(/admin/)).toBeOnTheScreen();
});

test('[P0] the round trip goes through the contract path on the existing typed client', async () => {
  const user = userEvent.setup();
  const { apiRequest, calls } = fakeApiRequest(200, authenticatedIdentity());

  render(<RoundTripScreen apiRequest={apiRequest} onSignOut={jest.fn()} />);
  await user.press(screen.getByRole('button', { name: /round trip/i }));

  await screen.findByText(/student/);
  expect(calls).toEqual(['/api/v1/auth/me']);
});

// ---------------------------------------------------------------------------
// 1.9-COMP-002 [P0] — a non-2xx renders the envelope's CODE, not a generic message
// ---------------------------------------------------------------------------

test('[P0] a 403 renders the error envelope code, not a generic failure message', async () => {
  const user = userEvent.setup();
  const { apiRequest } = fakeApiRequest(
    403,
    errorEnvelope('AUTH_DOMAIN_REJECTED', 'Only @amrita.edu accounts can access this system.'),
  );

  render(<RoundTripScreen apiRequest={apiRequest} onSignOut={jest.fn()} />);
  await user.press(screen.getByRole('button', { name: /round trip/i }));

  // AC 2: "A non-2xx must render the {code, message} error envelope's `code`."
  expect(await screen.findByText(/AUTH_DOMAIN_REJECTED/)).toBeOnTheScreen();
});

test('[P0] a 500 renders INTERNAL — the fail-closed audit path is visible to the user', async () => {
  const user = userEvent.setup();
  const { apiRequest } = fakeApiRequest(
    500,
    errorEnvelope('INTERNAL', 'An unexpected error occurred.'),
  );

  render(<RoundTripScreen apiRequest={apiRequest} onSignOut={jest.fn()} />);
  await user.press(screen.getByRole('button', { name: /round trip/i }));

  // This is what the user sees when the audit insert fails and the login is rolled back
  // (the api/test/roundtrip 1.9-INT-003 gate, from the device's side).
  expect(await screen.findByText(/INTERNAL/)).toBeOnTheScreen();
});

// ---------------------------------------------------------------------------
// 1.9-COMP-003 [P1] — the rest of the server-echoed identity
// ---------------------------------------------------------------------------

test('[P1] the screen renders the subject and email echoed from the verified JWT', async () => {
  const user = userEvent.setup();
  const identity = authenticatedIdentity();
  const { apiRequest } = fakeApiRequest(200, identity);

  render(<RoundTripScreen apiRequest={apiRequest} onSignOut={jest.fn()} />);
  await user.press(screen.getByRole('button', { name: /round trip/i }));

  expect(await screen.findByText(new RegExp(identity.email))).toBeOnTheScreen();
  expect(screen.getByText(new RegExp(identity.subject))).toBeOnTheScreen();
});

test('[P1] the screen renders the server-supplied name, per AC 2', async () => {
  const user = userEvent.setup();
  const identity = authenticatedIdentity({ name: 'Test Student' });
  const { apiRequest } = fakeApiRequest(200, identity);

  render(<RoundTripScreen apiRequest={apiRequest} onSignOut={jest.fn()} />);
  await user.press(screen.getByRole('button', { name: /round trip/i }));

  expect(await screen.findByText(/Test Student/)).toBeOnTheScreen();
});

// ---------------------------------------------------------------------------
// 1.9-COMP-004 [P2] — sign out
// ---------------------------------------------------------------------------

test('[P2] the sign-out button invokes the injected sign-out port', async () => {
  const user = userEvent.setup();
  const { apiRequest } = fakeApiRequest(200, authenticatedIdentity());
  const onSignOut = jest.fn();

  render(<RoundTripScreen apiRequest={apiRequest} onSignOut={onSignOut} />);
  await user.press(screen.getByRole('button', { name: /sign out/i }));

  expect(onSignOut).toHaveBeenCalledTimes(1);
});
