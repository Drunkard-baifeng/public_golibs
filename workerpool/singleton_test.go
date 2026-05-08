package workerpool

import (
	"sync/atomic"
	"testing"
	"time"
)

func resetDefaultPoolForTest() {
	Stop()
	SetDefaultMaxWorkers(1)
}

func TestPackageSubmitAndWaitIdle(t *testing.T) {
	resetDefaultPoolForTest()
	defer Stop()

	SetDefaultMaxWorkers(4)

	const total = 200
	var done atomic.Int32

	for i := 0; i < total; i++ {
		ok := Submit(func() {
			time.Sleep(1 * time.Millisecond)
			done.Add(1)
		})
		if !ok {
			t.Fatalf("Submit failed at i=%d", i)
		}
	}

	WaitIdle()
	if got := int(done.Load()); got != total {
		t.Fatalf("done mismatch, got=%d want=%d", got, total)
	}

	s := DefaultStats()
	if s.Stopped {
		t.Fatalf("default pool should not be stopped")
	}
}

func TestPackageStopAndRecreate(t *testing.T) {
	resetDefaultPoolForTest()
	defer Stop()

	var done atomic.Int32

	if !Submit(func() { done.Add(1) }) {
		t.Fatalf("first submit failed")
	}
	WaitIdle()
	Stop()

	if !Submit(func() { done.Add(1) }) {
		t.Fatalf("submit after Stop should recreate pool")
	}
	WaitIdle()

	if got := int(done.Load()); got != 2 {
		t.Fatalf("done mismatch, got=%d want=2", got)
	}
}

func TestSetDefaultMaxWorkersAffectsDefaultPool(t *testing.T) {
	resetDefaultPoolForTest()
	defer Stop()

	SetDefaultMaxWorkers(2)
	if got := Default().MaxWorkers(); got != 2 {
		t.Fatalf("max workers mismatch, got=%d want=2", got)
	}

	SetDefaultMaxWorkers(5)
	if got := Default().MaxWorkers(); got != 5 {
		t.Fatalf("max workers mismatch after update, got=%d want=5", got)
	}
}
