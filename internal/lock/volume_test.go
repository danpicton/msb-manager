package lock

import (
	"errors"
	"sync"
	"testing"
)

func TestAcquire_FreeVolumeSucceeds(t *testing.T) {
	l := New()
	if err := l.Acquire("myvol", "alice"); err != nil {
		t.Fatalf("Acquire(myvol, alice): %v", err)
	}
}

func TestAcquire_ConflictReturnsErrVolumeBusy(t *testing.T) {
	l := New()
	_ = l.Acquire("myvol", "alice")

	err := l.Acquire("myvol", "bob")
	if !errors.Is(err, ErrVolumeBusy) {
		t.Fatalf("Acquire by second sandbox: got %v, want ErrVolumeBusy", err)
	}
	// The error message should name the current holder so HTTP responses can
	// surface "in use by <other-sandbox>".
	if !contains(err.Error(), "alice") {
		t.Errorf("error %q should mention current holder %q", err.Error(), "alice")
	}
}

func TestAcquire_IdempotentForSameSandbox(t *testing.T) {
	l := New()
	_ = l.Acquire("myvol", "alice")
	if err := l.Acquire("myvol", "alice"); err != nil {
		t.Errorf("re-Acquire by same holder: got %v, want nil (idempotent)", err)
	}
}

func TestAcquireMany_AtomicOnConflict(t *testing.T) {
	l := New()
	_ = l.Acquire("vol2", "alice")

	// bob asks for vol1 + vol2; vol2 is held by alice -> whole call fails.
	_, err := l.AcquireMany([]string{"vol1", "vol2"}, "bob")
	if !errors.Is(err, ErrVolumeBusy) {
		t.Fatalf("got %v, want ErrVolumeBusy", err)
	}
	// vol1 must NOT have been silently claimed by bob — the call is atomic.
	if err := l.Acquire("vol1", "carol"); err != nil {
		t.Errorf("vol1 should still be free after failed AcquireMany; got %v", err)
	}
}

// Issue #19: a failed create/start must roll back only the claims it newly
// acquired, never claims that pre-existed the request (e.g. a client retrying
// the create of an already-running sandbox). AcquireMany reports the
// newly-claimed subset; releasing exactly that subset must leave the running
// instance's claims intact.
func TestAcquireMany_RollbackOfNewClaimsKeepsPreExistingClaims(t *testing.T) {
	cases := []struct {
		name    string
		volumes []string
	}{
		{name: "retry with same volume", volumes: []string{"data"}},
		{name: "retry with no volume", volumes: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := New()
			_ = l.Acquire("data", "web") // web is already running, mounted on data

			newly, err := l.AcquireMany(tc.volumes, "web")
			if err != nil {
				t.Fatalf("AcquireMany(%v, web): %v", tc.volumes, err)
			}
			if len(newly) != 0 {
				t.Errorf("newly = %v, want none (data was already web's)", newly)
			}
			l.ReleaseVolumes(newly) // the failed-create rollback path

			if err := l.Acquire("data", "other"); !errors.Is(err, ErrVolumeBusy) {
				t.Errorf("data should still be web's after rollback; got %v", err)
			}
		})
	}
}

func TestAcquireMany_ReportsOnlyNewClaims(t *testing.T) {
	l := New()
	_ = l.Acquire("data", "web")

	newly, err := l.AcquireMany([]string{"data", "cache"}, "web")
	if err != nil {
		t.Fatalf("AcquireMany([data cache], web): %v", err)
	}
	if len(newly) != 1 || newly[0] != "cache" {
		t.Fatalf("newly = %v, want [cache]", newly)
	}
	l.ReleaseVolumes(newly)

	// The rollback freed cache but kept the pre-existing data claim.
	if err := l.Acquire("cache", "other"); err != nil {
		t.Errorf("cache should be free after rollback; got %v", err)
	}
	if err := l.Acquire("data", "other"); !errors.Is(err, ErrVolumeBusy) {
		t.Errorf("data should still be web's after rollback; got %v", err)
	}
}

func TestRelease_FreesAllVolumesOfSandbox(t *testing.T) {
	l := New()
	_, _ = l.AcquireMany([]string{"v1", "v2", "v3"}, "alice")
	_ = l.Acquire("vOther", "bob")

	l.Release("alice")

	// alice's volumes are free now
	for _, v := range []string{"v1", "v2", "v3"} {
		if err := l.Acquire(v, "carol"); err != nil {
			t.Errorf("%s should be free after Release(alice); got %v", v, err)
		}
	}
	// bob's volume is untouched
	if err := l.Acquire("vOther", "carol"); !errors.Is(err, ErrVolumeBusy) {
		t.Errorf("vOther should still be held by bob; got %v", err)
	}
}

func TestRelease_UnknownSandboxIsNoOp(t *testing.T) {
	l := New()
	// Should not panic, should not affect existing state.
	l.Release("noone")
	_ = l.Acquire("v1", "alice")
	l.Release("noone")
	if err := l.Acquire("v1", "bob"); !errors.Is(err, ErrVolumeBusy) {
		t.Errorf("after no-op Release, v1 should still be alice's; got %v", err)
	}
}

func TestReconcile_ReplacesState(t *testing.T) {
	l := New()
	_ = l.Acquire("v-stale", "ghost") // ghost is no longer running
	_ = l.Acquire("v-current", "alice")

	// Reconcile from "truth": only alice/v-current is real.
	l.Reconcile(map[string]string{"v-current": "alice"})

	// v-stale is free
	if err := l.Acquire("v-stale", "bob"); err != nil {
		t.Errorf("v-stale should be free after reconcile; got %v", err)
	}
	// v-current still held
	if err := l.Acquire("v-current", "bob"); !errors.Is(err, ErrVolumeBusy) {
		t.Errorf("v-current should still be alice's; got %v", err)
	}
}

// Concurrency smoke: hammer Acquire/Release from many goroutines; never panic,
// and end with no held volumes. Not a formal test of serialisation order, but
// would surface a missing/wrong mutex with -race.
func TestConcurrent_NoPanicOrLeak(t *testing.T) {
	l := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vol := "v" + string(rune('a'+i%5))
			sb := "sb" + string(rune('a'+i%3))
			_ = l.Acquire(vol, sb)
			l.Release(sb)
		}(i)
	}
	wg.Wait()
	// After everyone releases, nothing should be held.
	if held := l.heldForTest(); len(held) != 0 {
		t.Errorf("after all releases, %d volumes still held: %v", len(held), held)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
