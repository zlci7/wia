package idgen

import "testing"

func TestNewGeneratesUniqueIDs(t *testing.T) {
	ids := make(map[string]struct{}, 1000)

	for i := 0; i < 1000; i++ {
		id := New("test")
		if _, exists := ids[id]; exists {
			t.Fatalf("duplicate id generated: %s", id)
		}
		ids[id] = struct{}{}
	}
}
