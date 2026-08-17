package odrecord

// cached_event_reader.go — the status poll's event lookup, served from memory.
//
// GET /api/v1/mark/status re-reads the event row on every poll to decide whether
// a slot is open, and a coordinator with the screen open polls every ten seconds.
// The monitoring export counted 8,862 GetMarkEvent calls against 319 marks. The
// row itself changes only when an administrator edits the event.
//
// WHY THIS IS SAFE TO CACHE AND THE MARK PATH'S LOOKUP IS NOT: this decorator is
// applied to the reader instance handed to StatusService ALONE, which is a
// different instance from the one Service holds for the mark write. The write
// therefore keeps reading the event live, and the two cannot be confused by
// editing one line — they are separate objects, not one object used carefully.
// See the note in cmd/api/main.go where both are built.

import (
	"context"
	"errors"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/cache"
)

// CachedEventReader serves EventForMark from memory and passes everything else
// straight through.
type CachedEventReader struct {
	inner  Reader
	events *cache.TTL[cachedEvent]
}

var _ Reader = (*CachedEventReader)(nil)

// cachedEvent holds the settled answer, refusal included. "This event does not
// exist" will not stop being true inside the lifetime, so it is worth holding;
// "the database was unreachable" is not an answer and is never stored.
type cachedEvent struct {
	event Event
	err   error
}

// NewCachedEventReader wraps inner, holding each event for ttl. A ttl of zero
// calls straight through, which is how caching is switched off without a deploy.
func NewCachedEventReader(inner Reader, ttl time.Duration) *CachedEventReader {
	return &CachedEventReader{inner: inner, events: cache.New[cachedEvent](ttl)}
}

func (r *CachedEventReader) EventForMark(ctx context.Context, eventID string) (Event, error) {
	got, err := r.events.Do(ctx, eventID, func(ctx context.Context) (cachedEvent, error) {
		event, loadErr := r.inner.EventForMark(ctx, eventID)
		if loadErr == nil {
			return cachedEvent{event: event}, nil
		}
		if errors.Is(loadErr, ErrEventNotFound) {
			return cachedEvent{err: loadErr}, nil
		}
		// Anything else is a failure rather than an answer. Returning it here is
		// how cache.TTL is told to store nothing, so the next poll retries.
		return cachedEvent{}, loadErr
	})
	if err != nil {
		return Event{}, err
	}
	return got.event, got.err
}

// CategoryForEvent and StudentForMark are NOT cached. Neither is on the polled
// path — the status read never calls them — so wrapping them would add staleness
// to the mark path for no saving at all.
func (r *CachedEventReader) CategoryForEvent(ctx context.Context, eventID, categoryID string) (Category, error) {
	return r.inner.CategoryForEvent(ctx, eventID, categoryID)
}

func (r *CachedEventReader) StudentForMark(ctx context.Context, studentID string) (Student, error) {
	return r.inner.StudentForMark(ctx, studentID)
}

// Clear drops every held event.
func (r *CachedEventReader) Clear() { r.events.Clear() }
