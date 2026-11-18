package ports

import (
	"context"
	"testing"
	"time"
)

type slowVerifiedApply struct {
	Fake
	delay time.Duration
}

func (f *slowVerifiedApply) Apply(ctx context.Context, p Plan) error {
	timer := time.NewTimer(f.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	return f.Fake.Apply(ctx, p)
}
func TestReconcilerAllowsBoundedVerifiedRecovery(t *testing.T) {
	f := &slowVerifiedApply{delay: 3200 * time.Millisecond}
	r := Reconciler{Applier: f}
	v := Snapshot{}
	if e := r.apply(context.Background(), Plan{}, &v); e != nil {
		t.Fatal("verified provider recovery was cut off", e)
	}
}
func TestReconcilerRecoveryStillHonorsCallerCancellation(t *testing.T) {
	f := &slowVerifiedApply{delay: time.Hour}
	r := Reconciler{Applier: f}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	v := Snapshot{}
	if e := r.apply(ctx, Plan{}, &v); e == nil {
		t.Fatal("canceled recovery succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("caller cancellation ignored")
	}
	if len(f.Current().Mappings) != 0 {
		t.Fatal("cancellation exposed a mapping")
	}
}
