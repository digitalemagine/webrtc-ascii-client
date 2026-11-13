package latest

import (
	"context"
	"testing"
	"testing/synctest"
)

func TestSlotKeepsNewest(t *testing.T) {
	t.Parallel()

	s := NewSlot[int]()
	s.Put(1)
	s.Put(2)
	s.Put(3)
	if v, err := s.Get(t.Context()); err != nil || v != 3 {
		t.Fatalf("Get = %d, %v; want 3", v, err)
	}
	if n := s.Dropped(); n != 2 {
		t.Fatalf("Dropped = %d, want 2", n)
	}
	if _, ok := s.TryGet(); ok {
		t.Fatal("slot not empty after Get")
	}
}

func TestSlotGetWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewSlot[string]()
		got := make(chan string)
		go func() {
			v, _ := s.Get(context.Background())
			got <- v
		}()
		synctest.Wait() // Get is blocked
		s.Put("frame")
		if v := <-got; v != "frame" {
			t.Fatalf("got %q", v)
		}
	})
}

func TestSlotGetCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewSlot[int]().Get(ctx); err == nil {
		t.Fatal("Get on a canceled context returned no error")
	}
}
