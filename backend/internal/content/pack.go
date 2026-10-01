package content

import (
	"bytes"
	"encoding/json"
	"errors"
)

// The pack schema generations. v1 described bystanders as bare display names; v2
// gives them a stable identity plus optional description, home location and avatar.
// Loading normalizes both into the v2 shape, and the rest of the program reads only
// that shape.
const (
	SchemaV1 = 1
	SchemaV2 = 2
	SchemaV3 = 3
	SchemaV4 = 4
)

// UnmarshalJSON accepts both the v1 display string and the v2 object, so one type
// can represent either generation of a pack on disk.
func (b *PackBystander) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return errors.New("empty bystander")
	}
	if trimmed[0] == '"' {
		var name string
		if err := json.Unmarshal(trimmed, &name); err != nil {
			return err
		}
		*b = PackBystander{Name: name}
		return nil
	}
	type plain PackBystander
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*b = PackBystander(value)
	return nil
}

// MarshalJSON always writes the v2 object, so exported and republished content has
// one representation regardless of which generation it was read from.
func (b PackBystander) MarshalJSON() ([]byte, error) {
	type plain PackBystander
	return json.Marshal(plain(b))
}
