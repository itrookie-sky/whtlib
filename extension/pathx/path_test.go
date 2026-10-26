package pathx

import "testing"

func TestPathPrefix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prefix  string
		rest    string
		matched bool
	}{
		{name: "/api/v1/users", prefix: "/api/v1", rest: "users", matched: true},
		{name: "/api/v10/users", prefix: "/api/v1", rest: "/api/v10/users"},
		{name: "/api/v1", prefix: "/api/v1", matched: true},
		{name: "/api//v1/./users/", prefix: "/api/v1/", rest: "users", matched: true},
		{name: "/api/v1/../v2", prefix: "/api/v1", rest: "/api/v1/../v2"},
		{name: "/api/v1", prefix: "/", rest: "api/v1", matched: true},
		{name: "/", prefix: "/", matched: true},
		{name: "api/v1", prefix: "/", rest: "api/v1"},
		{name: "api/v1", prefix: "api", rest: "v1", matched: true},
		{name: "/api/v1", prefix: "api", rest: "/api/v1"},
		{name: "api/v1", prefix: "/api", rest: "api/v1"},
		{name: "./api/../users", prefix: ".", rest: "users", matched: true},
		{name: ".", prefix: ".", matched: true},
		{name: "../api", prefix: ".", rest: "../api"},
		{name: "..", prefix: ".", rest: ".."},
		{name: "/api", prefix: ".", rest: "/api"},
		{name: "../api", prefix: "..", rest: "api", matched: true},
		{name: "/api%2Fv1/users", prefix: "/api/v1", rest: "/api%2Fv1/users"},
		{name: "/api/v1/%2e%2e", prefix: "/api/v1", rest: "%2e%2e", matched: true},
		{name: `api\v1`, prefix: "api", rest: `api\v1`},
		{name: "", prefix: "."},
		{name: "", prefix: ""},
		{name: "a//b/", prefix: "", rest: "a//b/"},
		{name: "a//b/", prefix: "c", rest: "a//b/"},
	} {
		t.Run(tc.name+"|"+tc.prefix, func(t *testing.T) {
			rest, matched := TrimPathPrefix(tc.name, tc.prefix)
			if rest != tc.rest || matched != tc.matched {
				t.Fatalf("TrimPathPrefix = %q, %v; want %q, %v", rest, matched, tc.rest, tc.matched)
			}
			if HasPathPrefix(tc.name, tc.prefix) != tc.matched {
				t.Fatal("HasPathPrefix disagrees with TrimPathPrefix")
			}
		})
	}
}
