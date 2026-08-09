// Package publiccache decorates the three UNAUTHENTICATED public reads with the
// bounded in-process cache.
//
// A DECORATOR RATHER THAN CACHING INSIDE THE SERVICES, so the read path stays
// one thing and the caching stays another: publicattendance.Service remains a
// plain database read that the integration suite can exercise without a cache in
// the way, and the "does a public read ever write?" invariant is still provable
// by reading one package. It also keeps this out of the authenticated surface by
// construction — there is nothing here to accidentally wrap an admin read with.
//
// Each decorator is defined against a one-method interface it declares itself,
// so this package imports the public service for its result types only and can
// never reach a scoped or actor-bearing read.
package publiccache

import (
	"context"
	"time"

	"github.com/Shruhath/OD-Amrita/api/internal/cache"
	"github.com/Shruhath/OD-Amrita/api/internal/service/publicattendance"
	svcstudent "github.com/Shruhath/OD-Amrita/api/internal/service/student"
)

// TTLs is the caching policy for the public surface.
//
// The three reads get DIFFERENT lifetimes because they answer differently to
// being stale, and a single number would have to be as short as the least
// tolerant of them:
//
//   - Events change only when an administrator edits them, and a student reading
//     a five-minute-old category list loses nothing.
//   - The roster changes only on an import.
//   - Records are the one a student is actively watching. This is the number
//     that decides how long "I was just marked" can still read as "no OD", so it
//     is the shortest by a wide margin.
//
// Any of them may be set to zero to switch that cache off outright.
type TTLs struct {
	Records time.Duration
	Search  time.Duration
	Events  time.Duration
}

// RecordsReader is one student's public OD records.
type RecordsReader interface {
	Records(ctx context.Context, studentID string) (publicattendance.RecordCollection, error)
}

// EventsReader is the public event/category option list.
type EventsReader interface {
	PublicEvents(ctx context.Context) (publicattendance.EventCollection, error)
}

// SearchReader is the actor-less roster prefix search.
type SearchReader interface {
	PublicSearch(ctx context.Context, query string) ([]svcstudent.SearchResult, error)
}

// Records wraps a records reader.
type Records struct {
	inner RecordsReader
	cache *cache.TTL[publicattendance.RecordCollection]
}

func NewRecords(inner RecordsReader, ttl time.Duration) *Records {
	return &Records{inner: inner, cache: cache.New[publicattendance.RecordCollection](ttl)}
}

func (r *Records) Records(ctx context.Context, studentID string) (publicattendance.RecordCollection, error) {
	return r.cache.Do(ctx, studentID, func(ctx context.Context) (publicattendance.RecordCollection, error) {
		return r.inner.Records(ctx, studentID)
	})
}

// Events wraps the public event list.
type Events struct {
	inner EventsReader
	cache *cache.TTL[publicattendance.EventCollection]
}

func NewEvents(inner EventsReader, ttl time.Duration) *Events {
	return &Events{inner: inner, cache: cache.New[publicattendance.EventCollection](ttl)}
}

// PublicEvents caches under a single fixed key — the read takes no parameters,
// so every caller wants the same answer and there is nothing to key on.
func (e *Events) PublicEvents(ctx context.Context) (publicattendance.EventCollection, error) {
	return e.cache.Do(ctx, "public-events", func(ctx context.Context) (publicattendance.EventCollection, error) {
		return e.inner.PublicEvents(ctx)
	})
}

// Search wraps the anonymous roster search.
type Search struct {
	inner SearchReader
	cache *cache.TTL[[]svcstudent.SearchResult]
}

func NewSearch(inner SearchReader, ttl time.Duration) *Search {
	return &Search{inner: inner, cache: cache.New[[]svcstudent.SearchResult](ttl)}
}

// PublicSearch caches on the RAW query string.
//
// Deliberately not normalised here. The service owns trimming and bounding, and
// a cache that normalised differently from the service would key two queries
// together that the service treats as distinct — a student typing one roll
// number and being shown the results for another. Keying on exactly what
// arrived can only ever cost a duplicate entry, never a wrong answer.
func (s *Search) PublicSearch(ctx context.Context, query string) ([]svcstudent.SearchResult, error) {
	return s.cache.Do(ctx, query, func(ctx context.Context) ([]svcstudent.SearchResult, error) {
		return s.inner.PublicSearch(ctx, query)
	})
}
