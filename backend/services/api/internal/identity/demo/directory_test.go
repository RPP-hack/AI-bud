package demo

import (
	"context"
	"testing"
)

func TestDirectory(t *testing.T) {
	d := Directory{}
	for id, team := range map[string]string{"alex": "notifications", "sam": "payments"} {
		a, err := d.Resolve(context.Background(), id)
		if err != nil || a.ID != id || a.Team != team {
			t.Fatal(a, err)
		}
	}
	if _, err := d.Resolve(context.Background(), "missing"); err != ErrUnknownActor {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Resolve(ctx, "alex"); err != context.Canceled {
		t.Fatal(err)
	}
}
