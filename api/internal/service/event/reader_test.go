package event

import "testing"

func TestPageOffsetReturnsEmptyBeforeExtremePageCanOverflow(t *testing.T) {
	if offset, ok := pageOffset(21, 2, 20); !ok || offset != 20 {
		t.Fatalf("pageOffset(21, 2, 20) = (%d, %v), want (20, true)", offset, ok)
	}
	if offset, ok := pageOffset(20, 2, 20); ok || offset != 0 {
		t.Fatalf("pageOffset(20, 2, 20) = (%d, %v), want empty page", offset, ok)
	}
	if offset, ok := pageOffset(int64(^uint64(0)>>1), int(^uint(0)>>1), 100); ok || offset != 0 {
		t.Fatalf("extreme page offset = (%d, %v), want empty page without overflow", offset, ok)
	}
}
