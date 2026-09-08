package freegames

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

type object map[string]any

func decode(body []byte) (object, error) {
	var v object
	d := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})))
	d.UseNumber()
	err := d.Decode(&v)

	return v, err
}

func node(v any, path ...string) any {
	for _, key := range path {
		switch m := v.(type) {
		case object:
			v = m[key]
		case map[string]any:
			v = m[key]
		default:
			return nil
		}
	}

	return v
}

func str(v any, path ...string) string {
	switch value := node(v, path...).(type) {
	case string:
		return value
	case json.Number:
		return string(value)
	}

	return ""
}

func list(v any, path ...string) []any {
	a, _ := node(v, path...).([]any)

	return a
}

func flag(v any, path ...string) bool {
	b, _ := node(v, path...).(bool)

	return b
}

func integer(v any, path ...string) (int64, bool) {
	s := str(v, path...)
	if s == "" || strings.IndexFunc(s, func(r rune) bool {
		return r < '0' || r > '9'
	}) >= 0 {
		return 0, false
	}

	n, err := strconv.ParseInt(s, 10, 64)

	return n, err == nil
}

func instant(v any, path ...string) time.Time {
	t, _ := time.Parse(time.RFC3339, str(v, path...))

	return t
}
