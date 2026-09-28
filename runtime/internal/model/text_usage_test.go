package model

import "testing"

func TestTextUsageKnownCounters(t *testing.T) {
	i, o, r, h, m := 100, 20, 5, 60, 40
	d := TextDiagnostic{}
	d.SetUsage(&i, &o, &r, &h, &m)
	if !d.CacheKnown || d.CacheMissTokens != 40 || !d.ReasoningKnown {
		t.Fatalf("usage: %+v", d)
	}
	for _, test := range []struct {
		hit, miss *int
		known     bool
	}{{nil, nil, false}, {&h, nil, true}, {&i, &m, false}, {&h, &h, false}} {
		d := TextDiagnostic{}
		d.SetUsage(&i, &o, nil, test.hit, test.miss)
		if d.CacheKnown != test.known || d.ReasoningKnown {
			t.Fatalf("usage: %+v", d)
		}
	}
	z := 0
	d = TextDiagnostic{}
	d.SetUsage(&z, &z, &z, &z, &z)
	if !d.InputKnown || !d.OutputKnown || !d.ReasoningKnown || !d.CacheKnown {
		t.Fatal("zero is known")
	}
}
