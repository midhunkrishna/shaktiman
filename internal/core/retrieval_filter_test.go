package core

import (
	"context"
	"errors"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// fakeIsTestStore lets the helper tests drive AcceptByFileID without a
// real WriterStore. Only the GetFileIsTestByID signature is needed.
type fakeIsTestStore struct {
	isTest map[int64]bool
	err    error
}

func (s *fakeIsTestStore) GetFileIsTestByID(_ context.Context, id int64) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.isTest[id], nil
}

func TestTestFilter_AcceptIsTest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		f      TestFilter
		isTest bool
		accept bool
	}{
		{"all-accepts-impl", TestFilter{}, false, true},
		{"all-accepts-test", TestFilter{}, true, true},
		{"exclude-tests-keeps-impl", TestFilter{ExcludeTests: true}, false, true},
		{"exclude-tests-drops-test", TestFilter{ExcludeTests: true}, true, false},
		{"test-only-drops-impl", TestFilter{TestOnly: true}, false, false},
		{"test-only-keeps-test", TestFilter{TestOnly: true}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.AcceptIsTest(c.isTest); got != c.accept {
				t.Errorf("AcceptIsTest(%v) = %v, want %v", c.isTest, got, c.accept)
			}
		})
	}
}

func TestTestFilter_AcceptByFileID_SkipsLookupWhenInactive(t *testing.T) {
	t.Parallel()
	// Inactive filter must not even touch the store — otherwise a hot
	// query path pays for a per-result DB hop just to be told "yes".
	store := &fakeIsTestStore{err: errors.New("should not be called")}
	ok, err := TestFilter{}.AcceptByFileID(context.Background(), store, 1)
	if err != nil || !ok {
		t.Errorf("inactive filter must short-circuit: ok=%v err=%v", ok, err)
	}
}

func TestTestFilter_AcceptByFileID_ActiveFilter(t *testing.T) {
	t.Parallel()
	store := &fakeIsTestStore{isTest: map[int64]bool{42: true, 43: false}}

	// ExcludeTests: drops 42 (test), keeps 43 (impl).
	ok, err := TestFilter{ExcludeTests: true}.AcceptByFileID(context.Background(), store, 42)
	if err != nil || ok {
		t.Errorf("ExcludeTests on test file: ok=%v err=%v", ok, err)
	}
	ok, err = TestFilter{ExcludeTests: true}.AcceptByFileID(context.Background(), store, 43)
	if err != nil || !ok {
		t.Errorf("ExcludeTests on impl file: ok=%v err=%v", ok, err)
	}
}

func TestTestFilter_AcceptByFileID_PropagatesLookupError(t *testing.T) {
	t.Parallel()
	store := &fakeIsTestStore{err: errors.New("db down")}
	ok, err := TestFilter{ExcludeTests: true}.AcceptByFileID(context.Background(), store, 1)
	if err == nil {
		t.Fatal("lookup error must propagate")
	}
	if ok {
		t.Error("error must produce accept=false so callers can drop the row")
	}
}

func TestScoredFromHydrated_CopiesEveryField(t *testing.T) {
	t.Parallel()
	h := types.HydratedChunk{
		ChunkID: 7, FileID: 3, Path: "p.go", SymbolName: "Foo", Kind: "function",
		StartLine: 10, EndLine: 20, Content: "func Foo() {}", TokenCount: 5,
	}
	r := scoredFromHydrated(h, 0.42)
	if r.ChunkID != 7 || r.FileID != 3 || r.Path != "p.go" || r.SymbolName != "Foo" ||
		r.Kind != "function" || r.StartLine != 10 || r.EndLine != 20 ||
		r.Content != "func Foo() {}" || r.TokenCount != 5 || r.Score != 0.42 {
		t.Errorf("scoredFromHydrated dropped a field: %+v", r)
	}
}

func TestScoredFromChunk_CopiesEveryField(t *testing.T) {
	t.Parallel()
	c := &types.ChunkRecord{
		ID: 7, FileID: 3, SymbolName: "Foo", Kind: "function",
		StartLine: 10, EndLine: 20, Content: "func Foo() {}", TokenCount: 5,
	}
	r := scoredFromChunk(c, "p.go", 0.42)
	if r.ChunkID != 7 || r.FileID != 3 || r.Path != "p.go" || r.SymbolName != "Foo" ||
		r.Kind != "function" || r.StartLine != 10 || r.EndLine != 20 ||
		r.Content != "func Foo() {}" || r.TokenCount != 5 || r.Score != 0.42 {
		t.Errorf("scoredFromChunk dropped a field: %+v", r)
	}
}
