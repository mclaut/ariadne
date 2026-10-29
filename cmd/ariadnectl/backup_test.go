package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExportOffsetJSONNumberKeepsUint64Precision(t *testing.T) {
	const pointID = "18446744073709551614"
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(`{"result":{"next_page_offset":` + pointID + `}}`))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	result, _ := decoded["result"].(map[string]any)
	offset, ok := result["next_page_offset"].(json.Number)
	if !ok || offset.String() != pointID {
		t.Fatalf("offset = %#v", result["next_page_offset"])
	}
	body, err := json.Marshal(map[string]any{"offset": offset})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"offset":`+pointID+`}` {
		t.Fatalf("re-encoded offset = %s", body)
	}
}
