package hart

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishSendsTheDocumentedShape(t *testing.T) {
	var gotPath, gotKey, gotType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotKey = r.Header.Get("X-Hart-Owner-Key")
		gotType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"url":"https://h.example/a/o/rep"}`))
	}))
	defer srv.Close()

	got, err := Publish(context.Background(), Config{BaseURL: srv.URL, OwnerKey: "k"},
		"o", "rep", "unlisted", []byte("<h1>hi</h1>"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "/v1/publish?artifact=rep&owner=o&visibility=unlisted"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotKey != "k" {
		t.Errorf("owner key header = %q", gotKey)
	}
	if gotType != "text/html" {
		t.Errorf("content-type = %q", gotType)
	}
	if gotBody != "<h1>hi</h1>" {
		t.Errorf("body = %q", gotBody)
	}
	if got.URL != "https://h.example/a/o/rep" || got.Bytes != 11 {
		t.Errorf("result = %+v", got)
	}
}

func TestPublishFallsBackToCanonicalURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok, not json"))
	}))
	defer srv.Close()
	got, err := Publish(context.Background(), Config{BaseURL: srv.URL, OwnerKey: "k"},
		"o", "rep", "", []byte("<p>x</p>"))
	if err != nil {
		t.Fatalf("a 2xx with a non-JSON body still means published: %v", err)
	}
	if want := srv.URL + "/a/o/rep"; got.URL != want {
		t.Errorf("URL = %q, want %q", got.URL, want)
	}
}

func TestPublishSurfacesServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exceeded", http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := Publish(context.Background(), Config{BaseURL: srv.URL, OwnerKey: "k"},
		"o", "rep", "", []byte("<p>x</p>")); err == nil {
		t.Fatal("want an error on 403")
	}
}

func TestPublishRefusesBadInput(t *testing.T) {
	ok := Config{BaseURL: "http://x", OwnerKey: "k"}
	cases := []struct {
		name       string
		cfg        Config
		owner, art string
		html       []byte
	}{
		{"no key", Config{BaseURL: "http://x"}, "o", "a", []byte("<p>x</p>")},
		{"no owner", ok, "", "a", []byte("<p>x</p>")},
		{"no artifact", ok, "o", "", []byte("<p>x</p>")},
		{"empty html", ok, "o", "a", []byte("   \n ")},
	}
	for _, c := range cases {
		if _, err := Publish(context.Background(), c.cfg, c.owner, c.art, "", c.html); err == nil {
			t.Errorf("%s: want an error", c.name)
		}
	}
}

func TestOwnerFromRepo(t *testing.T) {
	for in, want := range map[string]string{
		"javimosch/am-fleet": "javimosch", "javimosch": "javimosch", "": "",
	} {
		if got := OwnerFromRepo(in); got != want {
			t.Errorf("OwnerFromRepo(%q) = %q, want %q", in, got, want)
		}
	}
}

// visibility: private needs a read key, which is a credential and so comes from
// the environment rather than the fleet definition.
func TestPublishPrivateSendsReadKey(t *testing.T) {
	var gotQuery string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusUnauthorized) // the gating check
			return
		}
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"url":"` + srv.URL + `/a/o/a"}`))
	}))
	defer srv.Close()

	cfg := Config{BaseURL: srv.URL, OwnerKey: "k", ReadKey: "rk"}
	if _, err := Publish(context.Background(), cfg, "o", "a", "private", []byte("<p>x</p>")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, "read_key=rk") || !strings.Contains(gotQuery, "visibility=private") {
		t.Errorf("query = %q, want visibility=private and read_key=rk", gotQuery)
	}

	// ...and must refuse rather than silently publish something unreadable.
	if _, err := Publish(context.Background(), Config{BaseURL: srv.URL, OwnerKey: "k"},
		"o", "a", "private", []byte("<p>x</p>")); err == nil {
		t.Error("want an error when HART_READ_KEY is missing")
	}
}

// A private artifact readable without credentials is worse than a failed
// publish, because it looks like success.
func TestPublishPrivateVerifiesTheGate(t *testing.T) {
	var anonStatus int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(anonStatus)
			return
		}
		// Point the artifact URL back at this server so the gating check hits it.
		w.Write([]byte(`{"url":"` + srv.URL + `/a/o/a"}`))
	}))
	defer srv.Close()
	cfg := Config{BaseURL: srv.URL, OwnerKey: "k", ReadKey: "rk"}

	for _, tc := range []struct {
		status  int
		wantErr bool
	}{
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusFound, false}, // a redirect to a login page is still a refusal
		{http.StatusOK, true},     // wide open — must fail loudly
	} {
		anonStatus = tc.status
		_, err := Publish(context.Background(), cfg, "o", "a", "private", []byte("<p>x</p>"))
		if tc.wantErr && err == nil {
			t.Errorf("anon %d: want an error", tc.status)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("anon %d: unexpected error: %v", tc.status, err)
		}
	}
}
