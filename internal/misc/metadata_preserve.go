package misc

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"

	log "github.com/sirupsen/logrus"
)

// preserveExclusions lists the keys that must never be carried over from an
// existing auth file. Only the disabled flag qualifies: re-binding an account is
// how an operator re-enables it, so the stale flag has to die with the old file.
//
// Credential and identity keys need no entry here — they are part of every fresh
// payload and are therefore excluded by construction (see ApplyPreservedMetadata).
var preserveExclusions = map[string]struct{}{
	"disabled": {},
}

// metadataSetter is implemented by token storages that accept injected metadata,
// which their own save routine then flattens into the top-level JSON object.
type metadataSetter interface {
	SetMetadata(map[string]any)
}

// ApplyPreservedMetadata injects recordMeta into storage, plus any top-level keys
// found in the auth file at path that the upcoming write would otherwise drop.
//
// Logins and credential imports build a brand new token storage and save it by
// truncating the target file, so operator-set keys (claude_usage_mode, priority,
// note, headers, prefix, proxy_url, excluded_models, ...) are silently erased on
// every re-bind. This carries them forward.
//
// Precedence is old-file keys < fresh payload < recordMeta, and it holds by
// construction rather than by convention: the preserved set is defined as the old
// file's keys minus every key the fresh payload already defines, so a rotated
// access token can never be shadowed by the stale one it replaced.
//
// It is best effort and never fails a save: a missing, empty or corrupt file, a
// storage that cannot accept metadata, or an unmarshalable storage all degrade to
// today's behavior of injecting recordMeta unchanged.
func ApplyPreservedMetadata(path string, storage any, recordMeta map[string]any) {
	setter, ok := storage.(metadataSetter)
	if !ok {
		return
	}

	preserved := preservableKeys(path, storage, recordMeta)
	if len(preserved) == 0 {
		// Nothing to carry over (the common first-bind case): inject the caller's
		// map exactly as the pre-existing code did, side effects included.
		setter.SetMetadata(recordMeta)
		return
	}

	merged := make(map[string]any, len(preserved)+len(recordMeta))
	for k, v := range preserved {
		merged[k] = v
	}
	for k, v := range recordMeta {
		merged[k] = v
	}
	setter.SetMetadata(merged)
}

// preservableKeys returns the entries of the auth file at path that the upcoming
// write would drop: its top-level keys minus the fresh payload's keys minus the
// exclusion list. It returns nil whenever the file cannot be read or understood.
func preservableKeys(path string, storage any, recordMeta map[string]any) map[string]any {
	if strings.TrimSpace(path) == "" {
		return nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		// A missing file is the normal first-bind case; anything else is equally
		// non-fatal here — there is simply nothing to preserve.
		return nil
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}

	var existing map[string]any
	if err = json.Unmarshal(raw, &existing); err != nil {
		log.Debugf("auth metadata preserve: skipping unparsable auth file %s: %v", path, err)
		return nil
	}
	if len(existing) == 0 {
		return nil
	}

	// fresh is the key set the save is about to write, so every key in it is owned
	// by the new payload and must not be resurrected from the old file.
	fresh, err := MergeMetadata(storage, recordMeta)
	if err != nil {
		log.Debugf("auth metadata preserve: cannot determine fresh payload for %s: %v", path, err)
		return nil
	}
	// Ownership follows the storage's schema, not the bytes this particular login
	// happened to produce: an `omitempty` field that came back empty is missing
	// from fresh, and treating it as unowned would carry the old file's value for
	// it — a stale expiry landing on a brand new token.
	declared := declaredJSONKeys(storage)

	preserved := make(map[string]any, len(existing))
	for k, v := range existing {
		if _, isFresh := fresh[k]; isFresh {
			continue
		}
		if _, isDeclared := declared[k]; isDeclared {
			continue
		}
		if _, excluded := preserveExclusions[k]; excluded {
			continue
		}
		preserved[k] = v
	}
	return preserved
}

// declaredJSONKeys returns every top-level JSON key the value's type declares,
// whether or not this instance would actually serialize it. Fields tagged
// `omitempty` are included precisely because an empty one disappears from the
// marshaled output while still belonging to the schema; fields tagged "-" are
// excluded because they never reach the file.
//
// Non-struct values (a plain map, say) declare nothing and yield an empty set —
// the marshaled key set is the only ownership signal available for those.
func declaredJSONKeys(value any) map[string]struct{} {
	keys := make(map[string]struct{})
	if value == nil {
		return keys
	}
	collectJSONKeys(reflect.TypeOf(value), keys, 0)
	return keys
}

// maxEmbedDepth bounds the walk through embedded structs. Auth storages nest one
// level at most; the bound just keeps a pathological type from spinning.
const maxEmbedDepth = 8

func collectJSONKeys(t reflect.Type, keys map[string]struct{}, depth int) {
	if t == nil || depth > maxEmbedDepth {
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")

		// An untagged embedded struct promotes its fields to the top level, so its
		// keys are top-level keys too.
		if field.Anonymous && name == "" {
			collectJSONKeys(field.Type, keys, depth+1)
			continue
		}
		if name == "-" || !field.IsExported() {
			continue
		}
		if name == "" {
			name = field.Name
		}
		keys[name] = struct{}{}
	}
}
