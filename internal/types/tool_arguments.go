package types

import (
	"bytes"
	"encoding/json"
)

// UnmarshalToolArguments preserves opaque numeric IDs and decimal values while
// retaining json.Unmarshal's whole-document validation and map merge behavior.
// Callers execute the original JSON; the parsed form must replay those values
// without a float64 round trip.
func UnmarshalToolArguments(data []byte, args *map[string]any) error {
	if !json.Valid(data) {
		var raw json.RawMessage
		return json.Unmarshal(data, &raw)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(args)
}

// This private field type limits UseNumber to tool arguments. Result.Data and
// unrelated legacy snapshot fields retain their existing decoding behavior.
type exactToolArguments map[string]any

func (a *exactToolArguments) UnmarshalJSON(data []byte) error {
	return UnmarshalToolArguments(data, (*map[string]any)(a))
}

// UnmarshalJSON preserves arguments through both DB AgentSteps.Scan and run
// snapshot decoding, including ordinary json.Unmarshal callers.
func (c *ToolCall) UnmarshalJSON(data []byte) error {
	type plain ToolCall
	value := struct {
		*plain
		Args exactToolArguments `json:"args"`
	}{plain: (*plain)(c), Args: exactToolArguments(c.Args)}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	c.Args = map[string]any(value.Args)
	return nil
}

// UnmarshalJSON decodes a ToolCallTarget while preserving exact JSON number representations in Args.
func (c *ToolCallTarget) UnmarshalJSON(data []byte) error {
	type plain ToolCallTarget
	value := struct {
		*plain
		Args exactToolArguments `json:"args"`
	}{plain: (*plain)(c), Args: exactToolArguments(c.Args)}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	c.Args = map[string]any(value.Args)
	return nil
}
