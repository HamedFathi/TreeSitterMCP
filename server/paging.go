package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

type pageToken struct {
	Offset      int    `json:"o"`
	Fingerprint string `json:"f"`
}

func cursorOffset(cursor, fp string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	if len(cursor) > 1024 {
		return 0, errors.New("invalid cursor")
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, errors.New("invalid cursor")
	}
	var t pageToken
	if json.Unmarshal(b, &t) != nil || t.Offset < 0 || t.Fingerprint != digest([]byte(fp)) {
		return 0, errors.New("cursor is stale or belongs to another request")
	}
	return t.Offset, nil
}
func nextToken(offset int, fp string) string {
	b, _ := json.Marshal(pageToken{offset, digest([]byte(fp))})
	return base64.RawURLEncoding.EncodeToString(b)
}
func binding(tool string, in Input) string {
	in.Cursor = ""
	in.Limit = 0
	in.MaxBytes = 0
	return tool + ":" + hashJSON(in)
}
func (e *Engine) budget(in Input) int {
	if in.MaxBytes > 0 {
		return in.MaxBytes
	}
	return e.opts.MaxResponseBytes
}
func (e *Engine) page(items []any, in Input, fp string, incomplete bool, note string) (Page, error) {
	start, err := cursorOffset(in.Cursor, fp)
	if err != nil {
		return Page{}, err
	}
	if start > len(items) {
		return Page{}, errors.New("cursor offset is out of range")
	}
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	out := Page{Items: []any{}, Incomplete: incomplete, Note: note}
	next := start
	for next < len(items) && len(out.Items) < limit {
		out.Items = append(out.Items, items[next])
		out.Truncated = next+1 < len(items)
		if out.Truncated {
			out.NextCursor = nextToken(next+1, fp)
		} else {
			out.NextCursor = ""
		}
		b, _ := json.Marshal(out)
		if len(b) > e.budget(in) {
			out.Items = out.Items[:len(out.Items)-1]
			if len(out.Items) == 0 {
				return Page{}, errors.New("one result exceeds budget; increase max_bytes")
			}
			break
		}
		next++
	}
	out.Truncated = next < len(items)
	if out.Truncated {
		out.NextCursor = nextToken(next, fp)
	} else {
		out.NextCursor = ""
	}
	return out, nil
}
