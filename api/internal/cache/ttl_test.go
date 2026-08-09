package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServesASecondReadFromTheCache(t *testing.T) {
	var loads atomic.Int64
	c := New[string](time.Minute)
	load := func(context.Context) (string, error) {
		loads.Add(1)
		return "records", nil
	}

	for range 5 {
		value, err := c.Do(context.Background(), "student-1", load)
		if err != nil || value != "records" {
			t.Fatalf("Do = %q, %v", value, err)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("loaded %d times, want 1", loads.Load())
	}
}

func TestReloadsOnceTheEntryHasExpired(t *testing.T) {
	var loads atomic.Int64
	clock := time.Now()
	c := New[string](30 * time.Second)
	c.now = func() time.Time { return clock }
	load := func(context.Context) (string, error) {
		loads.Add(1)
		return "records", nil
	}

	if _, err := c.Do(context.Background(), "student-1", load); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(31 * time.Second)
	if _, err := c.Do(context.Background(), "student-1", load); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 2 {
		t.Fatalf("loaded %d times, want 2 — a stale entry was served past its ttl", loads.Load())
	}
}

// The property that actually absorbs the 18:15 spike.
//
// A TTL alone does nothing for a COLD cache under a simultaneous burst: every
// request misses at the same instant and every one of them queries. Collapsing
// concurrent misses for the same key into one load is what turns a thousand
// refreshes into a single database read.
func TestCollapsesConcurrentMissesForOneKeyIntoASingleLoad(t *testing.T) {
	var loads atomic.Int64
	release := make(chan struct{})
	c := New[string](time.Minute)
	load := func(context.Context) (string, error) {
		loads.Add(1)
		<-release // hold the flight open so every goroutine piles onto it
		return "records", nil
	}

	var started, done sync.WaitGroup
	started.Add(50)
	done.Add(50)
	for range 50 {
		go func() {
			started.Done()
			defer done.Done()
			if _, err := c.Do(context.Background(), "student-1", load); err != nil {
				t.Error(err)
			}
		}()
	}
	started.Wait()
	// Give the goroutines a moment to reach the singleflight before releasing;
	// without this the first one could finish before the rest even call Do.
	time.Sleep(50 * time.Millisecond)
	close(release)
	done.Wait()

	if got := loads.Load(); got != 1 {
		t.Fatalf("%d concurrent readers caused %d loads, want 1", 50, got)
	}
}

// A failure must not be pinned for the length of the TTL. One bad second would
// otherwise become a full TTL of failures for everyone holding that key.
func TestNeverCachesAnError(t *testing.T) {
	var loads atomic.Int64
	c := New[string](time.Minute)
	failing := func(context.Context) (string, error) {
		loads.Add(1)
		return "", errors.New("database is unreachable")
	}

	for range 3 {
		if _, err := c.Do(context.Background(), "student-1", failing); err == nil {
			t.Fatal("expected the load error to surface")
		}
	}
	if loads.Load() != 3 {
		t.Fatalf("loaded %d times, want 3 — an error was cached", loads.Load())
	}

	// And the recovery is immediate, not TTL-delayed.
	value, err := c.Do(context.Background(), "student-1", func(context.Context) (string, error) {
		return "records", nil
	})
	if err != nil || value != "records" {
		t.Fatalf("recovery read = %q, %v", value, err)
	}
}

// The memory-safety bound. The records cache is keyed on a student UUID taken
// from the path of an endpoint anyone may call, so without a ceiling an attacker
// enumerating UUIDs allocates an entry per request and the process dies.
func TestRefusesToGrowPastItsEntryBound(t *testing.T) {
	c := New[string](time.Hour)
	c.maxEntries = 10
	load := func(context.Context) (string, error) { return "records", nil }

	for i := range 500 {
		key := "student-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if _, err := c.Do(context.Background(), key, load); err != nil {
			t.Fatal(err)
		}
	}

	if got := c.Len(); got > 10 {
		t.Fatalf("cache holds %d entries, want at most its bound of 10", got)
	}
}

// Declining to cache must still return the right answer — a full cache degrades
// to the uncached behaviour rather than failing or serving another key's value.
func TestStillAnswersCorrectlyWhenFull(t *testing.T) {
	c := New[string](time.Hour)
	c.maxEntries = 2
	answer := func(value string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return value, nil }
	}

	for _, key := range []string{"a", "b", "c", "d"} {
		value, err := c.Do(context.Background(), key, answer("value-"+key))
		if err != nil {
			t.Fatal(err)
		}
		if value != "value-"+key {
			t.Fatalf("key %q returned %q", key, value)
		}
	}
}

// A non-positive TTL is the documented off switch, so it must call through every
// time rather than caching forever.
func TestZeroTTLDisablesCachingEntirely(t *testing.T) {
	var loads atomic.Int64
	c := New[string](0)
	load := func(context.Context) (string, error) {
		loads.Add(1)
		return "records", nil
	}

	for range 4 {
		if _, err := c.Do(context.Background(), "student-1", load); err != nil {
			t.Fatal(err)
		}
	}
	if loads.Load() != 4 {
		t.Fatalf("loaded %d times, want 4 — a zero ttl still cached", loads.Load())
	}
	if c.Len() != 0 {
		t.Fatalf("a disabled cache stored %d entries", c.Len())
	}
}

func TestKeysDoNotBleedIntoEachOther(t *testing.T) {
	c := New[string](time.Minute)
	answer := func(value string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) { return value, nil }
	}

	first, err := c.Do(context.Background(), "student-1", answer("one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Do(context.Background(), "student-2", answer("two"))
	if err != nil {
		t.Fatal(err)
	}
	if first != "one" || second != "two" {
		t.Fatalf("got %q and %q — one student was served another's records", first, second)
	}
}
