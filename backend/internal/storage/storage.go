// Package storage keeps the drivers' uploaded files (the private bucket
// driver-docs). Files are reached only through short-lived signed URLs: the
// app PUTs a file to an upload URL, and the admin opens a read URL.
//
// Local is the dev and test store: files live in memory or on disk and are
// served by ride-api itself under /storage/, behind HMAC-signed URLs. A
// Supabase Storage implementation can sit behind the same interface.
package storage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"commute-backend/internal/clock"
)

// Bucket is the private bucket's name, the first part of every path.
const Bucket = "driver-docs"

// MaxFileBytes is the largest file accepted (5 MB).
const MaxFileBytes = 5 << 20

// Storage issues signed URLs for the private bucket.
type Storage interface {
	// UploadURL is a URL the app PUTs one file of [contentType] to, valid for [ttl].
	UploadURL(ctx context.Context, path, contentType string, ttl time.Duration) (string, error)
	// ReadURL is a URL that serves the file at [path], valid for [ttl].
	ReadURL(ctx context.Context, path string, ttl time.Duration) (string, error)
	// Exists reports whether a file was uploaded to [path].
	Exists(ctx context.Context, path string) (bool, error)
}

// AllowedContentTypes are the types the bucket accepts.
var AllowedContentTypes = map[string]bool{"image/jpeg": true, "image/png": true}

// SniffImage is the content type of a JPEG or PNG file, or "" for anything else.
func SniffImage(b []byte) string {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return "image/png"
	}
	return ""
}

// Local is a Storage served by ride-api: in memory when Dir is empty,
// otherwise under Dir.
type Local struct {
	// BaseURL is ride-api's public URL; signed URLs point at BaseURL/storage/....
	BaseURL string
	Secret  string
	Dir     string
	Clock   clock.Clock

	mu    sync.Mutex
	files map[string]file
}

type file struct {
	contentType string
	data        []byte
}

func (l *Local) now() time.Time {
	if l.Clock == nil {
		return time.Now()
	}
	return l.Clock.Now()
}

func (l *Local) sign(op, path, contentType string, exp int64) string {
	m := hmac.New(sha256.New, []byte(l.Secret))
	fmt.Fprintf(m, "%s\n%s\n%s\n%d", op, path, contentType, exp)
	return hex.EncodeToString(m.Sum(nil))
}

func (l *Local) signed(op, path, contentType string, ttl time.Duration) string {
	exp := l.now().Add(ttl).Unix()
	q := url.Values{}
	q.Set("op", op)
	q.Set("exp", strconv.FormatInt(exp, 10))
	if contentType != "" {
		q.Set("ct", contentType)
	}
	q.Set("sig", l.sign(op, path, contentType, exp))
	return strings.TrimRight(l.BaseURL, "/") + "/storage/" + path + "?" + q.Encode()
}

func (l *Local) UploadURL(_ context.Context, path, contentType string, ttl time.Duration) (string, error) {
	return l.signed("put", path, contentType, ttl), nil
}

func (l *Local) ReadURL(_ context.Context, path string, ttl time.Duration) (string, error) {
	return l.signed("get", path, "", ttl), nil
}

func (l *Local) Exists(_ context.Context, path string) (bool, error) {
	if l.Dir == "" {
		l.mu.Lock()
		defer l.mu.Unlock()
		_, ok := l.files[path]
		return ok, nil
	}
	_, err := os.Stat(l.diskPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Put stores a file directly (tests and seeds).
func (l *Local) Put(path, contentType string, data []byte) error {
	if l.Dir == "" {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.files == nil {
			l.files = map[string]file{}
		}
		l.files[path] = file{contentType, bytes.Clone(data)}
		return nil
	}
	p := l.diskPath(path)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

func (l *Local) get(path string) (file, bool, error) {
	if l.Dir == "" {
		l.mu.Lock()
		defer l.mu.Unlock()
		f, ok := l.files[path]
		return f, ok, nil
	}
	b, err := os.ReadFile(l.diskPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return file{}, false, nil
	}
	if err != nil {
		return file{}, false, err
	}
	return file{SniffImage(b), b}, true, nil
}

func (l *Local) diskPath(path string) string {
	return filepath.Join(l.Dir, filepath.FromSlash(path))
}

// validPath is driver-docs/<segments> with no empty, "." or ".." segments.
func validPath(p string) bool {
	parts := strings.Split(p, "/")
	if len(parts) < 2 || parts[0] != Bucket {
		return false
	}
	for _, s := range parts[1:] {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "\\\x00") {
			return false
		}
	}
	return true
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"code":%q,"message":%q,"state":null}`+"\n", code, msg)
}

// ServeHTTP serves PUT and GET /storage/<path>?op=&exp=&ct=&sig=.
func (l *Local) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/storage/")
	if !validPath(path) {
		writeErr(w, 404, "not_found", "No such file")
		return
	}
	q := r.URL.Query()
	op, ct := q.Get("op"), q.Get("ct")
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	want := map[string]string{http.MethodPut: "put", http.MethodGet: "get"}[r.Method]
	if err != nil || op != want || !hmac.Equal([]byte(q.Get("sig")), []byte(l.sign(op, path, ct, exp))) {
		writeErr(w, 403, "bad_signature", "This link isn't valid")
		return
	}
	if l.now().Unix() > exp {
		writeErr(w, 403, "link_expired", "This link has expired")
		return
	}
	switch op {
	case "put":
		data, err := io.ReadAll(io.LimitReader(r.Body, MaxFileBytes+1))
		if err != nil {
			writeErr(w, 400, "bad_request", "Could not read the file")
			return
		}
		if len(data) > MaxFileBytes {
			writeErr(w, 413, "file_too_large", "Files can be at most 5 MB")
			return
		}
		sniffed := SniffImage(data)
		if sniffed == "" || (ct != "" && sniffed != ct) {
			writeErr(w, 415, "bad_file_type", "Upload a JPEG or PNG photo")
			return
		}
		if err := l.Put(path, sniffed, data); err != nil {
			writeErr(w, 500, "internal", "Could not save the file")
			return
		}
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"path":%q}`+"\n", path)
	case "get":
		f, ok, err := l.get(path)
		if err != nil {
			writeErr(w, 500, "internal", "Could not read the file")
			return
		}
		if !ok {
			writeErr(w, 404, "not_found", "No such file")
			return
		}
		if f.contentType != "" {
			w.Header().Set("content-type", f.contentType)
		}
		w.Header().Set("cache-control", "private, no-store")
		w.Write(f.data)
	}
}
