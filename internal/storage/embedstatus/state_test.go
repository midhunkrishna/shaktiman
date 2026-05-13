package embedstatus

import (
	"context"
	"errors"
	"testing"
)

// fakeOps is an in-memory ChunkOps used to drive the state machine
// without a real database. chunkEmbedded tracks per-chunk truth;
// chunkToFile maps chunk → file so AffectedFiles can answer. fileStatus
// records every SetFileStatus call so tests can assert which transitions
// the state machine took.
type fakeOps struct {
	chunkEmbedded map[int64]bool
	chunkToFile   map[int64]int64
	fileStatus    map[int64]string
	statusCalls   []statusCall
	failOn        string // method name that should fail; empty disables
}

type statusCall struct {
	fileID       int64
	status       string
	keepComplete bool
}

func newFakeOps() *fakeOps {
	return &fakeOps{
		chunkEmbedded: map[int64]bool{},
		chunkToFile:   map[int64]int64{},
		fileStatus:    map[int64]string{},
	}
}

func (o *fakeOps) addChunk(chunkID, fileID int64, embedded bool) {
	o.chunkEmbedded[chunkID] = embedded
	o.chunkToFile[chunkID] = fileID
}

func (o *fakeOps) SetEmbedded(_ context.Context, chunkIDs []int64, embedded bool) error {
	if o.failOn == "SetEmbedded" {
		return errors.New("boom")
	}
	for _, id := range chunkIDs {
		o.chunkEmbedded[id] = embedded
	}
	return nil
}

func (o *fakeOps) AffectedFiles(_ context.Context, chunkIDs []int64) ([]int64, error) {
	if o.failOn == "AffectedFiles" {
		return nil, errors.New("boom")
	}
	seen := map[int64]struct{}{}
	var out []int64
	for _, id := range chunkIDs {
		fid, ok := o.chunkToFile[id]
		if !ok {
			continue
		}
		if _, dup := seen[fid]; dup {
			continue
		}
		seen[fid] = struct{}{}
		out = append(out, fid)
	}
	return out, nil
}

func (o *fakeOps) CountRemaining(_ context.Context, fileID int64) (int, error) {
	if o.failOn == "CountRemaining" {
		return 0, errors.New("boom")
	}
	n := 0
	for cid, embedded := range o.chunkEmbedded {
		if o.chunkToFile[cid] == fileID && !embedded {
			n++
		}
	}
	return n, nil
}

func (o *fakeOps) CountEmbedded(_ context.Context, fileID int64) (int, error) {
	if o.failOn == "CountEmbedded" {
		return 0, errors.New("boom")
	}
	n := 0
	for cid, embedded := range o.chunkEmbedded {
		if o.chunkToFile[cid] == fileID && embedded {
			n++
		}
	}
	return n, nil
}

func (o *fakeOps) SetFileStatus(_ context.Context, fileID int64, status string, keepCompleteOnly bool) error {
	if o.failOn == "SetFileStatus" {
		return errors.New("boom")
	}
	o.statusCalls = append(o.statusCalls, statusCall{fileID, status, keepCompleteOnly})
	if keepCompleteOnly && o.fileStatus[fileID] == "complete" {
		return nil
	}
	o.fileStatus[fileID] = status
	return nil
}

func TestMarkEmbedded_AllChunksTransitionFileToComplete(t *testing.T) {
	t.Parallel()
	ops := newFakeOps()
	// file 100 has two chunks (1, 2). Mark both embedded → file becomes 'complete'.
	ops.addChunk(1, 100, false)
	ops.addChunk(2, 100, false)

	if err := MarkEmbedded(context.Background(), ops, []int64{1, 2}); err != nil {
		t.Fatalf("MarkEmbedded: %v", err)
	}
	if ops.fileStatus[100] != "complete" {
		t.Errorf("file 100 status = %q, want complete", ops.fileStatus[100])
	}
	if len(ops.statusCalls) != 1 || ops.statusCalls[0].keepComplete {
		t.Errorf("complete transition must be unconditional, got %+v", ops.statusCalls)
	}
}

func TestMarkEmbedded_PartialMarkUsesKeepComplete(t *testing.T) {
	t.Parallel()
	ops := newFakeOps()
	ops.addChunk(1, 100, false)
	ops.addChunk(2, 100, false) // still pending after mark of just chunk 1

	if err := MarkEmbedded(context.Background(), ops, []int64{1}); err != nil {
		t.Fatalf("MarkEmbedded: %v", err)
	}
	if ops.fileStatus[100] != "partial" {
		t.Errorf("file 100 status = %q, want partial", ops.fileStatus[100])
	}
	if !ops.statusCalls[0].keepComplete {
		t.Errorf("partial transition must preserve 'complete' (keepCompleteOnly=true), got %+v", ops.statusCalls)
	}
}

func TestMarkEmbedded_PartialDoesNotRegressComplete(t *testing.T) {
	t.Parallel()
	ops := newFakeOps()
	ops.fileStatus[100] = "complete" // file is already complete
	ops.addChunk(1, 100, true)       // both chunks already embedded
	ops.addChunk(2, 100, true)
	ops.addChunk(3, 100, false) // simulate a new unembedded chunk appearing

	// Re-mark chunk 1 — file should stay 'complete' even though chunk 3
	// is unembedded because keepCompleteOnly prevents regression.
	if err := MarkEmbedded(context.Background(), ops, []int64{1}); err != nil {
		t.Fatalf("MarkEmbedded: %v", err)
	}
	if ops.fileStatus[100] != "complete" {
		t.Errorf("file 100 regressed to %q (must stay complete)", ops.fileStatus[100])
	}
}

func TestReset_AllChunksUnembeddedReturnsToPending(t *testing.T) {
	t.Parallel()
	ops := newFakeOps()
	ops.fileStatus[100] = "complete"
	ops.addChunk(1, 100, true)
	ops.addChunk(2, 100, true)

	if err := Reset(context.Background(), ops, []int64{1, 2}); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if ops.fileStatus[100] != "pending" {
		t.Errorf("file 100 status = %q, want pending", ops.fileStatus[100])
	}
	if ops.statusCalls[0].keepComplete {
		t.Error("Reset must not preserve 'complete' — it is the path that drops stale completes")
	}
}

func TestReset_SomeEmbeddedRemainsPartial(t *testing.T) {
	t.Parallel()
	ops := newFakeOps()
	ops.fileStatus[100] = "complete"
	ops.addChunk(1, 100, true)
	ops.addChunk(2, 100, true)

	if err := Reset(context.Background(), ops, []int64{1}); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if ops.fileStatus[100] != "partial" {
		t.Errorf("file 100 status = %q, want partial", ops.fileStatus[100])
	}
}

func TestMarkEmbedded_EmptyChunkIDsIsNoop(t *testing.T) {
	t.Parallel()
	ops := newFakeOps()
	if err := MarkEmbedded(context.Background(), ops, nil); err != nil {
		t.Fatal(err)
	}
	if len(ops.statusCalls) != 0 {
		t.Errorf("empty input must not call SetFileStatus, got %d calls", len(ops.statusCalls))
	}
}

func TestMarkEmbedded_PropagatesErrors(t *testing.T) {
	t.Parallel()
	for _, failOn := range []string{"SetEmbedded", "AffectedFiles", "CountRemaining", "SetFileStatus"} {
		ops := newFakeOps()
		ops.failOn = failOn
		ops.addChunk(1, 100, false)
		err := MarkEmbedded(context.Background(), ops, []int64{1})
		if err == nil {
			t.Errorf("expected error when %s fails", failOn)
		}
	}
}

func TestReset_PropagatesErrors(t *testing.T) {
	t.Parallel()
	for _, failOn := range []string{"SetEmbedded", "AffectedFiles", "CountEmbedded", "SetFileStatus"} {
		ops := newFakeOps()
		ops.failOn = failOn
		ops.addChunk(1, 100, true)
		err := Reset(context.Background(), ops, []int64{1})
		if err == nil {
			t.Errorf("expected error when %s fails", failOn)
		}
	}
}
