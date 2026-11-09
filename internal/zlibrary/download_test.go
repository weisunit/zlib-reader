// s-library: 下载流式传输/进度回调/魔数校验单测。
package zlibrary

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDownloadStreamsAndReportsProgress(t *testing.T) {
	payload := bytes.Repeat([]byte("zlib-payload-"), 4096) // ~53KB
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("remix-userid") == "" {
			t.Errorf("download request missing remix headers")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer ts.Close()

	hc := ts.Client()
	hc.Timeout = 5 * time.Second
	c := NewClient([]string{ts.URL}, WithHTTPClient(hc), WithMinInterval(0))
	c.SetAuth("42", "k")

	var buf bytes.Buffer
	var calls int
	var lastDone, lastTotal int64
	err := c.Download(context.Background(), ts.URL+"/file.epub", &buf, func(done, total int64) {
		calls++
		lastDone, lastTotal = done, total
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), payload) {
		t.Fatalf("downloaded %d bytes, want %d", buf.Len(), len(payload))
	}
	if calls == 0 || lastDone != int64(len(payload)) || lastTotal != int64(len(payload)) {
		t.Errorf("progress calls=%d last=%d/%d", calls, lastDone, lastTotal)
	}
}

func TestDownloadHTTPErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{http.StatusNotFound, CodeNotFound},
		{http.StatusGone, CodeNotFound},
		{http.StatusUnauthorized, CodeAuthRequired},
		{http.StatusTooManyRequests, CodeRateLimited},
	}
	for _, tc := range cases {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))
		hc := ts.Client()
		hc.Timeout = 3 * time.Second
		c := NewClient([]string{ts.URL}, WithHTTPClient(hc), WithMinInterval(0))
		err := c.Download(context.Background(), ts.URL+"/x", io.Discard, nil)
		if ze := AsZError(err); ze == nil || ze.Code != tc.want {
			t.Errorf("status %d: err = %v, want %s", tc.status, err, tc.want)
		}
		ts.Close()
	}
}

func TestDownloadShortBodyDetected(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write(bytes.Repeat([]byte("a"), 100)) // 短包
	}))
	defer ts.Close()
	hc := ts.Client()
	hc.Timeout = 3 * time.Second
	c := NewClient([]string{ts.URL}, WithHTTPClient(hc), WithMinInterval(0))
	err := c.Download(context.Background(), ts.URL+"/x", io.Discard, nil)
	if ze := AsZError(err); ze == nil || ze.Code != CodeUpstream {
		t.Fatalf("err = %v, want Z_UPSTREAM（短包检测）", err)
	}
}

func TestDownloadEmptyURL(t *testing.T) {
	c := NewClient(nil, WithMinInterval(0))
	err := c.Download(context.Background(), " ", io.Discard, nil)
	if ze := AsZError(err); ze == nil || ze.Code != CodeNotFound {
		t.Fatalf("err = %v, want Z_NOT_FOUND", err)
	}
}

func TestValidateMagic(t *testing.T) {
	cases := []struct {
		name    string
		ext     string
		content string
		wantErr bool
	}{
		{"epub PK ok", "epub", "PK\x03\x04rest-of-file", false},
		{"epub wrong magic", "epub", "NOTZIP....", true},
		{"cbz PK ok", "cbz", "PK\x05\x06", false},
		{"pdf ok", "pdf", "%PDF-1.7 rest", false},
		{"pdf wrong", "pdf", "PK\x03\x04", true},
		{"mobi BOOKMOBI ok", "mobi", strings.Repeat("\x00", 60) + "BOOKMOBI", false},
		{"azw3 wrong", "azw3", strings.Repeat("\x00", 60) + "BOOKMOBZ", true},
		{"txt no magic check", "txt", "anything at all", false},
		{"html no magic check", "html", "<html></html>", false},
		{"unknown ext lenient", "xyz", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMagic(tc.ext, strings.NewReader(tc.content))
			if tc.wantErr && err == nil {
				t.Fatalf("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
		})
	}
}
