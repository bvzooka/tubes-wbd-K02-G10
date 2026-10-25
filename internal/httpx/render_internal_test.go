package httpx

import "testing"

func TestSectionOf(t *testing.T) {
	for path, want := range map[string]string{
		"/databases": "databases",
		"/databases/1/tables/public/t": "databases",
		"/admin/connections/new": "connections",
		"/admin/users": "users",
		"/admin/groups/3": "groups",
		"/login": "",
	} {
		if got := sectionOf(path); got != want {
			t.Errorf("sectionOf(%q) = %q, want %q", path, got, want)
		}
	}
}
