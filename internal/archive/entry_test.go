package archive

import (
	"reflect"
	"testing"
)

func TestEntryRoundTrip(t *testing.T) {
	want := Entry{
		ID:       "2026-08-20-keycloak-client-scope",
		Title:    "Keycloak client scope",
		Category: "infra",
		Tags:     []string{"auth", "keycloak"},
		Created:  "2026-08-20",
		Updated:  "2026-08-20",
		Source:   "session:test",
		Raw:      "raw/2026-08-20-keycloak-client-scope.md",
		Body:     "First paragraph.\n\nSecond paragraph.\n",
	}

	data, err := marshalEntry(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseEntry(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch\n got: %#v\nwant: %#v\nfile:\n%s", got, want, data)
	}
}

func TestFTSQueryUsesORAndSplitsPunctuation(t *testing.T) {
	got, err := ftsQuery(`keycloak_openid_client_scope.gcip.name auth auth`)
	if err != nil {
		t.Fatal(err)
	}
	want := `"keycloak" OR "openid" OR "client" OR "scope" OR "gcip" OR "name" OR "auth"`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
