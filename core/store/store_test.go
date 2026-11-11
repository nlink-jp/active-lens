package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nlink-jp/active-lens/core/activity"
)

func openTemp(t *testing.T) Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestAppendAndQuery(t *testing.T) {
	st := openTemp(t)
	base := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	want := []activity.Sample{
		{TS: base, State: activity.Operating},
		{TS: base.Add(15 * time.Second), State: activity.Present},
		{TS: base.Add(30 * time.Second), State: activity.Away},
	}
	// Append out of order to confirm ORDER BY.
	for _, i := range []int{2, 0, 1} {
		if err := st.Append(want[i]); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	got, err := st.Query(time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d samples, want 3", len(got))
	}
	for i := range want {
		if !got[i].TS.Equal(want[i].TS) || got[i].State != want[i].State {
			t.Errorf("sample %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestQueryRange(t *testing.T) {
	st := openTemp(t)
	base := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		st.Append(activity.Sample{TS: base.Add(time.Duration(i) * time.Minute), State: activity.Operating})
	}
	got, err := st.Query(base.Add(time.Minute), base.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d in range, want 3 (minutes 1,2,3)", len(got))
	}
}

func TestLast(t *testing.T) {
	st := openTemp(t)
	if _, ok, err := st.Last(); err != nil || ok {
		t.Fatalf("Last on empty = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	base := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	st.Append(activity.Sample{TS: base, State: activity.Operating})
	st.Append(activity.Sample{TS: base.Add(time.Minute), State: activity.Away})
	got, ok, err := st.Last()
	if err != nil || !ok {
		t.Fatalf("Last = (ok=%v, err=%v)", ok, err)
	}
	if got.State != activity.Away || !got.TS.Equal(base.Add(time.Minute)) {
		t.Errorf("Last = %+v, want the away sample at +1m", got)
	}
}

func TestLastBeforeAndFirstAfterAreStrict(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, ok, err := st.LastBefore(time.Unix(100, 0)); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	if _, ok, err := st.FirstAfter(time.Unix(100, 0)); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	for _, ts := range []int64{100, 200, 300} {
		if err := st.Append(activity.Sample{TS: time.Unix(ts, 0), State: activity.Operating}); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		at     int64
		before int64 // 0 = none
		after  int64
	}{
		{50, 0, 100}, {100, 0, 200}, {150, 100, 200}, {200, 100, 300}, {300, 200, 0}, {350, 300, 0},
	} {
		b, ok, err := st.LastBefore(time.Unix(c.at, 0))
		if err != nil || ok != (c.before != 0) || (ok && b.TS.Unix() != c.before) {
			t.Errorf("LastBefore(%d) = %d, %v, %v; want %d", c.at, b.TS.Unix(), ok, err, c.before)
		}
		a, ok, err := st.FirstAfter(time.Unix(c.at, 0))
		if err != nil || ok != (c.after != 0) || (ok && a.TS.Unix() != c.after) {
			t.Errorf("FirstAfter(%d) = %d, %v, %v; want %d", c.at, a.TS.Unix(), ok, err, c.after)
		}
	}
}
