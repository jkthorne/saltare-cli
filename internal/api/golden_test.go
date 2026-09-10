package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// The server's serializers are hand-written Ruby modules and these structs are
// hand-written Go; nothing generates either. Before sal lived in its own repo a
// renamed key broke one CI run. Now it breaks a shipped binary — silently, as a
// zero value — unless something pins the contract. This is that something.
//
// testdata/api_golden/*.json are copies of saltare's
// test/fixtures/files/api_golden/, which its own golden test regenerates:
//
//	WRITE_GOLDEN=1 bin/rails test test/serializers/api/v1/golden_payloads_test.rb
//
// Refresh them here with script/sync-goldens.sh when the sibling checkout is
// present.
//
// The assertion is deliberately one-directional: every field these structs
// declare must exist in the payload, but a server key nothing decodes is fine
// and only gets logged. A client that ignores fields it does not need is
// working as intended; a client reading a key the server stopped sending is
// broken.

type goldenCase struct {
	name string
	// target is a pointer to a zero value of the type that decodes this payload.
	target any
	// optional are json paths the server emits conditionally, so their absence
	// from this particular golden proves nothing.
	optional []string
	// why, for a payload no Go type decodes.
	unmapped string
}

func goldenCases() []goldenCase {
	return []goldenCase{
		{name: "agent", target: &Agent{}},
		{name: "channel", target: &Channel{}},
		{name: "database", target: &Database{}},
		{name: "device_session", target: &DeviceSession{}},
		{name: "document", target: &Document{}},
		{name: "message", target: &Message{}},
		{
			name:   "notification",
			target: &Notification{},
			// NotificationSerializer adds `message` or `task` by notifiable
			// type, never both. The golden pins a Task notification.
			optional: []string{"message"},
		},
		{name: "project", target: &Project{}},
		{name: "row", target: &DBRow{}},
		{name: "task", target: &Task{}},
		{
			name:   "upload",
			target: &Upload{},
			// UploadSerializer takes include_download_url:, and the golden is
			// written with it off — a presigned URL is not a stable shape.
			optional: []string{"download_url"},
		},
		{
			name:     "member",
			unmapped: "sal reads the workspace directory through /mentionables, not /members",
		},
	}
}

func TestGoldenPayloadsCoverEveryDeclaredField(t *testing.T) {
	for _, tc := range goldenCases() {
		t.Run(tc.name, func(t *testing.T) {
			raw := readGolden(t, tc.name)

			if tc.target == nil {
				t.Logf("no Go type decodes %s.json: %s", tc.name, tc.unmapped)
				return
			}

			if err := json.Unmarshal(raw, tc.target); err != nil {
				t.Fatalf("%s.json does not decode into %T: %v", tc.name, tc.target, err)
			}

			var decoded any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("%s.json is not valid JSON: %v", tc.name, err)
			}
			present, opaque := goldenPaths(decoded)

			optional := make(map[string]bool, len(tc.optional))
			for _, path := range tc.optional {
				optional[path] = true
			}

			declared := structPaths(reflect.TypeOf(tc.target), "")
			for _, path := range declared {
				switch {
				case optional[path], underPath(path, optional), present[path]:
					continue
				case underPath(path, opaque):
					// The golden's value here was null or an empty array, so
					// the shape below it is simply not on display.
					t.Logf("unverified: %s (parent is null or empty in the golden)", path)
				default:
					t.Errorf("%s: struct field %q has no key in %s.json — the server "+
						"renamed or dropped it, and this field now decodes to its zero "+
						"value at runtime.\n%s", tc.name, path, tc.name, driftHint)
				}
			}

			declaredSet := make(map[string]bool, len(declared))
			for _, path := range declared {
				declaredSet[path] = true
			}
			var ignored []string
			for path := range present {
				if !declaredSet[path] && !underPath(path, declaredSet) {
					ignored = append(ignored, path)
				}
			}
			sort.Strings(ignored)
			if len(ignored) > 0 {
				t.Logf("server keys sal ignores: %s", strings.Join(ignored, ", "))
			}
		})
	}
}

// A spot-check that the goldens carry real values, not just shape — the same
// pair the server's own golden test asserts.
func TestGoldenPayloadsCarryValues(t *testing.T) {
	var channel Channel
	if err := json.Unmarshal(readGolden(t, "channel"), &channel); err != nil {
		t.Fatal(err)
	}
	if channel.Name != "General" {
		t.Errorf("channel name = %q, want %q", channel.Name, "General")
	}

	var session DeviceSession
	if err := json.Unmarshal(readGolden(t, "device_session"), &session); err != nil {
		t.Fatal(err)
	}
	if session.Device.Platform != "android" {
		t.Errorf("device platform = %q, want %q", session.Device.Platform, "android")
	}
	if len(session.Scopes) == 0 {
		t.Error("device session decoded no scopes")
	}
}

const driftHint = "If the server change was intentional, update the struct here; " +
	"otherwise the server regressed. Goldens live in saltare at " +
	"test/fixtures/files/api_golden/ — resync with script/sync-goldens.sh."

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "api_golden", name+".json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	return raw
}

// structPaths lists the dotted json paths a type declares: "sender.type",
// "device.platform". Slice elements share their field's path, matching how
// goldenPaths unions an array. time.Time, maps and any are leaves — their
// interiors are not part of the key contract.
func structPaths(t reflect.Type, prefix string) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch {
	case t == reflect.TypeOf(time.Time{}):
		return nil
	case t.Kind() == reflect.Slice, t.Kind() == reflect.Array:
		return structPaths(t.Elem(), prefix)
	case t.Kind() != reflect.Struct:
		return nil
	}

	var paths []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, ok := jsonName(field)
		if !ok {
			continue
		}
		path := join(prefix, name)
		paths = append(paths, path)
		paths = append(paths, structPaths(field.Type, path)...)
	}
	return paths
}

func jsonName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		name = field.Name
	}
	return name, true
}

// goldenPaths walks a decoded payload and returns every key path in it, plus
// the paths whose value is null or an empty array — the ones nothing below can
// be checked against. Array elements are unioned, so an optional per-element
// key present on any one element counts as present.
func goldenPaths(value any) (present, opaque map[string]bool) {
	present, opaque = map[string]bool{}, map[string]bool{}

	var walk func(any, string)
	walk = func(v any, prefix string) {
		switch v := v.(type) {
		case map[string]any:
			for key, child := range v {
				path := join(prefix, key)
				present[path] = true
				walk(child, path)
			}
		case []any:
			if len(v) == 0 {
				opaque[prefix] = true
				return
			}
			for _, item := range v {
				walk(item, prefix)
			}
		case nil:
			if prefix != "" {
				opaque[prefix] = true
			}
		}
	}
	walk(value, "")

	return present, opaque
}

// underPath reports whether path sits below any path in set. Used three ways:
// under a null or empty golden value (nothing below it is on display), under
// an optional key the server withheld, and under a field the structs declare
// as a leaf — a map[string]any like `metadata` or a row's `data`, whose
// interior is data rather than contract.
func underPath(path string, set map[string]bool) bool {
	for parent := path; ; {
		cut := strings.LastIndex(parent, ".")
		if cut < 0 {
			return false
		}
		parent = parent[:cut]
		if set[parent] {
			return true
		}
	}
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}
