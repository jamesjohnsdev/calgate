package server

import "testing"

// FuzzParseTimeRange ensures parseTimeRange never panics on arbitrary input
// and that success implies both returned times are valid (non-negative unix).
func FuzzParseTimeRange(f *testing.F) {
	f.Add("", "")
	f.Add("2025-01-01T00:00:00Z", "")
	f.Add("", "2025-12-31T23:59:59Z")
	f.Add("2025-01-01T00:00:00Z", "2025-12-31T23:59:59Z")
	f.Add("not-a-date", "")
	f.Add("", "garbage")
	f.Add("2025-13-01T00:00:00Z", "")  // invalid month
	f.Add("2025-00-01T00:00:00Z", "")  // invalid month zero
	f.Add("2025-01-32T00:00:00Z", "")  // invalid day
	f.Add("9999-12-31T23:59:59Z", "")  // far future
	f.Add("0001-01-01T00:00:00Z", "")  // far past
	f.Add("2025-06-01T25:00:00Z", "")  // invalid hour
	f.Add("not-rfc3339", "also-not")
	f.Add("2025-01-01", "")             // date-only (not RFC 3339)
	f.Add("2025-01-01T00:00:00", "")   // missing timezone

	f.Fuzz(func(t *testing.T, start, end string) {
		tr, err := parseTimeRange(start, end)
		if err != nil {
			return
		}
		if !tr.Start.IsZero() && tr.Start.Unix() < 0 {
			t.Errorf("parseTimeRange(%q, %q): Start.Unix() < 0", start, end)
		}
		if !tr.End.IsZero() && tr.End.Unix() < 0 {
			t.Errorf("parseTimeRange(%q, %q): End.Unix() < 0", start, end)
		}
	})
}

// FuzzDecodeToken ensures decodeToken never panics on arbitrary input.
func FuzzDecodeToken(f *testing.F) {
	f.Add("")
	f.Add("not-base64!!!")
	f.Add("aGVsbG8=")                                      // "hello" — valid base64, not JSON
	f.Add("e30K")                                           // base64 of "{}\n" — valid JSON, empty object
	f.Add("eyJBY2Nlc3NUb2tlbiI6InRlc3QifQ")                // valid Token JSON
	f.Add("eyJBY2Nlc3NUb2tlbiI6IiIsIlJlZnJlc2hUb2tlbiI6IiJ9") // zero-value Token

	f.Fuzz(func(t *testing.T, s string) {
		_, _ = decodeToken(s) // must not panic
	})
}
