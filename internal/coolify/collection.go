package coolify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// maxCollectionDepth bounds envelope unwrapping. One level is all Coolify uses;
// the limit only exists so a malformed response cannot recurse forever.
const maxCollectionDepth = 4

// decodeCollection decodes a JSON value that Coolify may return in any of three
// shapes, and normalises them to a slice.
//
// Coolify's list endpoints are not consistent, and its own OpenAPI spec does not
// always match what the server sends:
//
//  1. A plain array — what most endpoints return, and what the spec claims for
//     all of them.
//
//  2. A paginated envelope, {"count": 12, "deployments": [...]}. This is what
//     GET /deployments/applications/{uuid} actually returns, despite the spec
//     documenting a bare array.
//
//  3. A key-indexed object, {"2": {...}, "0": {...}}. Laravel's Collection
//     methods such as sortBy preserve the original array keys, and PHP's
//     json_encode emits an object rather than an array whenever the keys are not
//     a sequential 0..n-1 run. So an endpoint that sorts its results can return
//     an array on one call and an object on the next, depending only on whether
//     the sort happened to reorder anything. Keys are numeric indices, so they
//     are sorted numerically to recover the intended order.
//
// envelopeKey names the field to unwrap for shape 2; pass "" to skip that check.
func decodeCollection[T any](data []byte, envelopeKey string) ([]T, error) {
	return decodeCollectionDepth[T](data, envelopeKey, 0)
}

func decodeCollectionDepth[T any](data []byte, envelopeKey string, depth int) ([]T, error) {
	if depth > maxCollectionDepth {
		return nil, fmt.Errorf("coolify: response nests %q more than %d deep",
			envelopeKey, maxCollectionDepth)
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	switch trimmed[0] {
	case '[':
		var list []T
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, fmt.Errorf("coolify: decode list: %w", err)
		}
		return list, nil

	case '{':
		// Shape 2: a paginated envelope. The inner value can itself be either an
		// array or a key-indexed object, so it goes back through this function.
		if envelopeKey != "" {
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(trimmed, &envelope); err == nil {
				if inner, ok := envelope[envelopeKey]; ok {
					return decodeCollectionDepth[T](inner, envelopeKey, depth+1)
				}
			}
		}

		// Shape 3: a key-indexed object.
		var keyed map[string]T
		if err := json.Unmarshal(trimmed, &keyed); err != nil {
			return nil, fmt.Errorf("coolify: decode keyed collection: %w", err)
		}
		keys := make([]string, 0, len(keyed))
		for key := range keyed {
			keys = append(keys, key)
		}
		// Numeric where possible, so PHP's preserved indices restore the order
		// the server intended; lexical otherwise so the result stays stable.
		sort.Slice(keys, func(i, j int) bool {
			a, errA := strconv.Atoi(keys[i])
			b, errB := strconv.Atoi(keys[j])
			if errA == nil && errB == nil {
				return a < b
			}
			return keys[i] < keys[j]
		})
		out := make([]T, 0, len(keyed))
		for _, key := range keys {
			out = append(out, keyed[key])
		}
		return out, nil
	}

	return nil, fmt.Errorf("coolify: expected a JSON array or object, got %q", firstByte(trimmed))
}

func firstByte(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if len(data) > 40 {
		return string(data[:40]) + "…"
	}
	return string(data)
}
