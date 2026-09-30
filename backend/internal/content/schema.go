package content

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed schemas/*.json
var packSchemas embed.FS
var schemaOnce sync.Once
var storySchema, npcSchema *jsonschema.Schema
var schemaError error

func validatePackSchema(data []byte, npc bool) error {
	schemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		c.LoadURL = func(string) (io.ReadCloser, error) { return nil, fmt.Errorf("external schema references disabled") }
		for _, name := range []string{"story", "npc"} {
			b, e := packSchemas.ReadFile("schemas/" + name + ".schema.json")
			if e != nil {
				schemaError = e
				return
			}
			if e = c.AddResource(name+".json", bytes.NewReader(b)); e != nil {
				schemaError = e
				return
			}
		}
		storySchema, schemaError = c.Compile("story.json")
		if schemaError != nil {
			return
		}
		npcSchema, schemaError = c.Compile("npc.json")
	})
	if schemaError != nil {
		return schemaError
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return fmt.Errorf("invalid JSON")
	}
	schema := storySchema
	if npc {
		schema = npcSchema
	}
	if err := schema.Validate(value); err != nil {
		// Errors identify fields without echoing author text or secrets into the catalog.
		if v, ok := err.(*jsonschema.ValidationError); ok {
			for len(v.Causes) > 0 {
				v = v.Causes[0]
			}
			return fmt.Errorf("schema validation failed at %s (%s)", v.InstanceLocation, v.KeywordLocation)
		}
		return fmt.Errorf("schema validation failed")
	}
	return nil
}
