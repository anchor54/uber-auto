package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Driver documents live in a private bucket, reached only through
// short-lived signed URLs: the app uploads straight to storage, and the
// admin reads through a link that expires. Paths are always
// "driver-docs/{driverId}/{kind}".

const (
	// DocsBucket is the private bucket for driver documents and photos.
	DocsBucket = "driver-docs"
	// MaxUploadBytes is the largest photo a driver can upload.
	MaxUploadBytes = 5 << 20
	signedURLTTL   = 10 * time.Minute
)

// UploadContentTypes are the photos storage accepts.
var UploadContentTypes = []string{"image/jpeg", "image/png"}

// Storage signs uploads and reads for driver documents.
type Storage interface {
	// SignUpload is a URL the app PUTs the file to, with the header
	// content-type: [contentType], within [ttl]. Uploading again overwrites.
	SignUpload(ctx context.Context, path, contentType string, ttl time.Duration) (string, error)
	// SignRead is a URL that serves the file for [ttl].
	SignRead(ctx context.Context, path string, ttl time.Duration) (string, error)
	// Exists tells whether a file has been uploaded to [path].
	Exists(ctx context.Context, path string) (bool, error)
}

// storage is App.Storage, or an in-memory store (made once) when none is set.
func (a *App) storage() Storage {
	a.storageOnce.Do(func() {
		if a.Storage == nil {
			secret := make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				panic(err)
			}
			m := NewMemStorage("", hex.EncodeToString(secret))
			m.Clock = a.now
			a.Storage = m
		}
	})
	return a.Storage
}

// absoluteURL makes a URL the in-memory store gave relative to this API
// ("/storage/upload?…") absolute, using the host the request came to.
func absoluteURL(r *http.Request, u string) string {
	if !strings.HasPrefix(u, "/") {
		return u
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("x-forwarded-proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + u
}

// bucketKey is the object key inside the bucket: the path without "driver-docs/".
func bucketKey(path string) string { return strings.TrimPrefix(path, DocsBucket+"/") }

// ---------------------------------------------------------------- Supabase

// SupabaseStorage is Supabase Storage's REST API with the service-role key.
// The bucket is private; its settings should also cap files at 5 MB and
// allow only image/jpeg and image/png.
type SupabaseStorage struct {
	URL            string // https://<project>.supabase.co
	ServiceRoleKey string
	Bucket         string // DocsBucket when empty
	HTTP           *http.Client
}

func (s *SupabaseStorage) bucket() string {
	if s.Bucket == "" {
		return DocsBucket
	}
	return s.Bucket
}

func (s *SupabaseStorage) client() *http.Client {
	if s.HTTP == nil {
		return &http.Client{Timeout: 10 * time.Second}
	}
	return s.HTTP
}

// objectURL is {url}/storage/v1/object/{prefix}{bucket}/{key}, each part escaped.
func (s *SupabaseStorage) objectURL(prefix, path string) string {
	parts := strings.Split(bucketKey(path), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.TrimRight(s.URL, "/") + "/storage/v1/object/" + prefix + url.PathEscape(s.bucket()) + "/" + strings.Join(parts, "/")
}

func (s *SupabaseStorage) call(ctx context.Context, method, u string, body any, headers map[string]string, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("authorization", "Bearer "+s.ServiceRoleKey)
	req.Header.Set("apikey", s.ServiceRoleKey)
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := s.client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("supabase storage: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if out != nil && res.StatusCode/100 == 2 {
		if err := json.Unmarshal(raw, out); err != nil {
			return res.StatusCode, fmt.Errorf("supabase storage: bad reply: %w", err)
		}
	}
	if res.StatusCode/100 != 2 && method != http.MethodHead {
		msg := string(raw)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return res.StatusCode, fmt.Errorf("supabase storage: %s %d: %s", method, res.StatusCode, msg)
	}
	return res.StatusCode, nil
}

func (s *SupabaseStorage) SignUpload(ctx context.Context, path, contentType string, ttl time.Duration) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	// x-upsert: uploading the same item again replaces the file.
	if _, err := s.call(ctx, http.MethodPost, s.objectURL("upload/sign/", path), map[string]any{},
		map[string]string{"x-upsert": "true"}, &out); err != nil {
		return "", err
	}
	if out.URL == "" {
		return "", errors.New("supabase storage: no upload URL in the reply")
	}
	return strings.TrimRight(s.URL, "/") + "/storage/v1" + out.URL, nil
}

func (s *SupabaseStorage) SignRead(ctx context.Context, path string, ttl time.Duration) (string, error) {
	var out struct {
		SignedURL string `json:"signedURL"`
	}
	if _, err := s.call(ctx, http.MethodPost, s.objectURL("sign/", path), map[string]any{"expiresIn": int(ttl.Seconds())},
		nil, &out); err != nil {
		return "", err
	}
	if out.SignedURL == "" {
		return "", errors.New("supabase storage: no signed URL in the reply")
	}
	return strings.TrimRight(s.URL, "/") + "/storage/v1" + out.SignedURL, nil
}

func (s *SupabaseStorage) Exists(ctx context.Context, path string) (bool, error) {
	code, err := s.call(ctx, http.MethodHead, s.objectURL("", path), nil, nil, nil)
	if err != nil {
		return false, err
	}
	switch {
	case code/100 == 2:
		return true, nil
	case code == 400 || code == 404:
		return false, nil
	}
	return false, fmt.Errorf("supabase storage: HEAD %d", code)
}

// ---------------------------------------------------------------- in memory

// MemStorage keeps files in memory and serves them from this API through
// HMAC-signed links (PUT /storage/upload, GET /storage/object). For
// development and tests: files are lost on restart, and production refuses it.
type MemStorage struct {
	// Prefix for the links it signs, e.g. "http://localhost:8080". Empty
	// gives links relative to the API, made absolute per request.
	BaseURL string
	// Expiry of the links it signs; real time when nil.
	Clock   func() time.Time
	secret  []byte
	mu      sync.Mutex
	objects map[string]memObject
}

type memObject struct {
	data        []byte
	contentType string
}

// NewMemStorage is an empty in-memory store whose links start with [baseURL]
// and are signed with [secret].
func NewMemStorage(baseURL, secret string) *MemStorage {
	return &MemStorage{BaseURL: strings.TrimRight(baseURL, "/"), secret: []byte(secret), objects: map[string]memObject{}}
}

func (m *MemStorage) now() time.Time {
	if m.Clock == nil {
		return time.Now()
	}
	return m.Clock()
}

// Put stores a file directly, as if uploaded (for tests and seeding).
func (m *MemStorage) Put(path string, data []byte) {
	m.put(path, data, http.DetectContentType(data))
}

func (m *MemStorage) put(path string, data []byte, contentType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[path] = memObject{append([]byte{}, data...), contentType}
}

// Get is the file at [path] and its content type.
func (m *MemStorage) Get(path string) (data []byte, contentType string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objects[path]
	return o.data, o.contentType, ok
}

func (m *MemStorage) token(op, path, contentType string, exp int64) string {
	h := hmac.New(sha256.New, m.secret)
	fmt.Fprintf(h, "%s\n%s\n%s\n%d", op, path, contentType, exp)
	return hex.EncodeToString(h.Sum(nil))
}

func (m *MemStorage) sign(route, op, path, contentType string, ttl time.Duration) string {
	exp := m.now().Add(ttl).Unix()
	q := url.Values{}
	q.Set("path", path)
	if contentType != "" {
		q.Set("type", contentType)
	}
	q.Set("exp", strconv.FormatInt(exp, 10))
	q.Set("token", m.token(op, path, contentType, exp))
	return m.BaseURL + route + "?" + q.Encode()
}

func (m *MemStorage) SignUpload(_ context.Context, path, contentType string, ttl time.Duration) (string, error) {
	return m.sign("/storage/upload", "put", path, contentType, ttl), nil
}

func (m *MemStorage) SignRead(_ context.Context, path string, ttl time.Duration) (string, error) {
	return m.sign("/storage/object", "get", path, "", ttl), nil
}

func (m *MemStorage) Exists(_ context.Context, path string) (bool, error) {
	_, _, ok := m.Get(path)
	return ok, nil
}

// check verifies a signed link's token and expiry.
func (m *MemStorage) check(op string, q url.Values) error {
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil || q.Get("path") == "" {
		return apiErr(403, "bad_signature", "This link isn't valid")
	}
	want := m.token(op, q.Get("path"), q.Get("type"), exp)
	if !hmac.Equal([]byte(want), []byte(q.Get("token"))) {
		return apiErr(403, "bad_signature", "This link isn't valid")
	}
	if m.now().Unix() > exp {
		return apiErr(403, "link_expired", "This link has expired")
	}
	return nil
}

// ServeUpload is PUT /storage/upload: the body is the file.
func (m *MemStorage) ServeUpload(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := m.check("put", q); err != nil {
		return err
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("content-type"))
	if ct != q.Get("type") {
		return apiErr(415, "wrong_content_type", "Upload with the content type the link was made for")
	}
	if !slices.Contains(UploadContentTypes, ct) {
		return apiErr(415, "wrong_content_type", "Photos must be JPEG or PNG")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxUploadBytes+1))
	if err != nil {
		return &badRequest{"Could not read the upload"}
	}
	if len(data) > MaxUploadBytes {
		return apiErr(413, "file_too_large", "Photos can be at most 5 MB")
	}
	if len(data) == 0 {
		return &badRequest{"The file is empty"}
	}
	m.put(q.Get("path"), data, ct)
	writeJSON(w, 200, map[string]string{"path": q.Get("path")})
	return nil
}

// ServeObject is GET /storage/object: the file.
func (m *MemStorage) ServeObject(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if err := m.check("get", q); err != nil {
		return err
	}
	data, ct, ok := m.Get(q.Get("path"))
	if !ok {
		return apiErr(404, "not_found", "No file at this path")
	}
	w.Header().Set("content-type", ct)
	w.Header().Set("cache-control", "private, no-store")
	w.WriteHeader(200)
	w.Write(data)
	return nil
}

// memStorageRoutes serves the in-memory store's links. Registered straight
// on the mux: uploads are bigger than the JSON body limit.
func (a *App) memStorageRoutes(mux *http.ServeMux) {
	serve := func(f func(m *MemStorage, w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			m, ok := a.storage().(*MemStorage)
			var err error
			if !ok {
				err = apiErr(404, "not_found", fmt.Sprintf("Route %s:%s not found", r.Method, r.URL.Path))
			} else {
				r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes+1)
				err = f(m, w, r)
			}
			if err != nil {
				writeError(a, w, r, err)
			}
		}
	}
	mux.HandleFunc("PUT /storage/upload", serve((*MemStorage).ServeUpload))
	mux.HandleFunc("GET /storage/object", serve((*MemStorage).ServeObject))
}
