package app

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MapStyle is the map the app draws: Ola Maps' vector style, trimmed and
// served through this backend so the Ola key never reaches the phone.
//
//   - Only the base map is kept: Ola's own data source (EV chargers and 3D
//     models) and its unused street overlay are dropped, so a screen loads
//     one set of tiles instead of three and shows no advertising.
//   - Tiles, fonts and the sprite are fetched here with the key and passed
//     on. Tiles and fonts are cached (most recent first, up to a size
//     limit): riders in one city view much the same tiles, so Ola sees far
//     fewer requests. Phones cache them for a day too.
//
// Check Ola Maps' terms on caching before relying on the server cache at
// scale; it can be turned off with MAP_TILE_CACHE_MB=0.
type MapStyle interface {
	// Style is the style; [publicBase] is this API's URL as the phone sees it.
	Style(ctx context.Context, publicBase string) (map[string]any, error)
	Sprite(ctx context.Context, suffix string) (Asset, error)
	Tile(ctx context.Context, source string, z, x, y int) (Asset, error)
	Font(ctx context.Context, fontstack, rng string) (Asset, error)
}

type Asset struct {
	Body        []byte
	ContentType string
}

var SpriteSuffixes = []string{".json", ".png", "@2x.json", "@2x.png"}

// Ola style sources the app doesn't use (chargers, 3D models, a hidden overlay).
var droppedSources = map[string]bool{"vectordata": true, "street": true}

// Layers the map never reaches (it stays at zoom 15 or below).
var droppedLayerTypes = map[string]bool{"fill-extrusion": true}

// MaxZoom: the app's map doesn't zoom past this, so neither does the tile proxy.
const MaxZoom = 15

const styleCacheFor = 6 * time.Hour

// byteLRU is a least-recently-used cache bounded by total bytes.
type byteLRU struct {
	mu       sync.Mutex
	maxBytes int
	bytes    int
	order    *list.List // front = most recent
	items    map[string]*list.Element
}

type lruEntry struct {
	key   string
	asset Asset
}

func newByteLRU(maxBytes int) *byteLRU {
	return &byteLRU{maxBytes: maxBytes, order: list.New(), items: map[string]*list.Element{}}
}

func (c *byteLRU) get(key string) (Asset, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.order.MoveToFront(e)
		return e.Value.(*lruEntry).asset, true
	}
	return Asset{}, false
}

func (c *byteLRU) set(key string, a Asset) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(a.Body) > c.maxBytes {
		return
	}
	if e, ok := c.items[key]; ok {
		c.bytes -= len(e.Value.(*lruEntry).asset.Body)
		c.order.Remove(e)
	}
	c.items[key] = c.order.PushFront(&lruEntry{key, a})
	c.bytes += len(a.Body)
	for c.bytes > c.maxBytes {
		e := c.order.Back()
		old := e.Value.(*lruEntry)
		c.order.Remove(e)
		delete(c.items, old.key)
		c.bytes -= len(old.asset.Body)
	}
}

// OlaMapStyle serves Ola Maps' style through this backend.
type OlaMapStyle struct {
	apiKey    string
	styleName string
	base      string
	client    *http.Client
	now       func() time.Time
	cache     *byteLRU

	mu     sync.Mutex
	cached *loadedStyle
}

type loadedStyle struct {
	at    time.Time
	style map[string]any
	tiles map[string]tileSource
}

type tileSource struct {
	url     string
	maxZoom int
}

type OlaMapStyleOptions struct {
	StyleName string
	Client    *http.Client
	Now       func() time.Time
	// CacheMB sizes the tile and font cache (default 64; 0 turns it off).
	CacheMB *float64
}

func NewOlaMapStyle(apiKey string, o OlaMapStyleOptions) *OlaMapStyle {
	s := &OlaMapStyle{apiKey: apiKey, styleName: o.StyleName, client: o.Client, now: o.Now,
		base: "https://api.olamaps.io/tiles/vector/v1"}
	if s.styleName == "" {
		s.styleName = "default-light-standard"
	}
	if s.client == nil {
		s.client = &http.Client{Timeout: 10 * time.Second}
	}
	if s.now == nil {
		s.now = time.Now
	}
	mb := 64.0
	if o.CacheMB != nil {
		mb = *o.CacheMB
	}
	s.cache = newByteLRU(int(mb * 1024 * 1024))
	return s
}

func mapUnavailable() error {
	return apiErr(502, "map_unavailable", "The map isn't loading right now.")
}

func (s *OlaMapStyle) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL+sep+"api_key="+url.QueryEscape(s.apiKey), nil)
	if err != nil {
		return nil, mapUnavailable()
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, mapUnavailable()
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, mapUnavailable()
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, mapUnavailable()
	}
	return body, nil
}

func (s *OlaMapStyle) fetchJSON(ctx context.Context, rawURL string) (map[string]any, error) {
	body, err := s.fetch(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, mapUnavailable()
	}
	return v, nil
}

func num(v any, fallback float64) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return fallback
}

// load is Ola's style, trimmed, with the tile URL template of each kept
// source. Cached for 6 hours.
func (s *OlaMapStyle) load(ctx context.Context) (*loadedStyle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && s.now().Sub(s.cached.at) < styleCacheFor {
		return s.cached, nil
	}
	style, err := s.fetchJSON(ctx, fmt.Sprintf("%s/styles/%s/style.json", s.base, s.styleName))
	if err != nil {
		return nil, err
	}
	tiles := map[string]tileSource{}
	sources := map[string]any{}
	srcs, _ := style["sources"].(map[string]any)
	for name, raw := range srcs {
		if droppedSources[name] {
			continue
		}
		src, _ := raw.(map[string]any)
		srcURL, isString := src["url"].(string)
		if src["type"] == "vector" && isString {
			tilejson, err := s.fetchJSON(ctx, srcURL)
			if err != nil {
				return nil, err
			}
			list, _ := tilejson["tiles"].([]any)
			first, _ := dig(list, 0).(string)
			maxZoom := int(min(num(tilejson["maxzoom"], 14), MaxZoom))
			tiles[name] = tileSource{first, maxZoom}
			out := map[string]any{"type": "vector", "minzoom": num(tilejson["minzoom"], 0), "maxzoom": maxZoom}
			if attr, ok := tilejson["attribution"]; ok && attr != nil && attr != "" {
				out["attribution"] = attr
			}
			sources[name] = out
		} else {
			sources[name] = raw
		}
	}
	layers := []any{}
	all, _ := style["layers"].([]any)
	for _, raw := range all {
		l, _ := raw.(map[string]any)
		src, _ := l["source"].(string)
		typ, _ := l["type"].(string)
		if droppedSources[src] || droppedLayerTypes[typ] {
			continue
		}
		layers = append(layers, raw)
	}
	trimmed := map[string]any{}
	for k, v := range style {
		trimmed[k] = v
	}
	trimmed["sources"] = sources
	trimmed["layers"] = layers
	s.cached = &loadedStyle{at: s.now(), style: trimmed, tiles: tiles}
	return s.cached, nil
}

func (s *OlaMapStyle) cachedAsset(ctx context.Context, key, rawURL, contentType string) (Asset, error) {
	if a, ok := s.cache.get(key); ok {
		return a, nil
	}
	body, err := s.fetch(ctx, rawURL)
	if err != nil {
		return Asset{}, err
	}
	a := Asset{body, contentType}
	s.cache.set(key, a)
	return a, nil
}

func (s *OlaMapStyle) Style(ctx context.Context, publicBase string) (map[string]any, error) {
	loaded, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for k, v := range loaded.style {
		out[k] = v
	}
	sources := map[string]any{}
	for name, raw := range loaded.style["sources"].(map[string]any) {
		src, _ := raw.(map[string]any)
		if src["type"] == "vector" {
			withTiles := map[string]any{}
			for k, v := range src {
				withTiles[k] = v
			}
			withTiles["tiles"] = []string{fmt.Sprintf("%s/v1/map/tiles/%s/{z}/{x}/{y}.pbf", publicBase, name)}
			sources[name] = withTiles
		} else {
			sources[name] = raw
		}
	}
	out["sources"] = sources
	out["sprite"] = publicBase + "/v1/map/sprite"
	out["glyphs"] = publicBase + "/v1/map/fonts/{fontstack}/{range}.pbf"
	return out, nil
}

func (s *OlaMapStyle) Sprite(ctx context.Context, suffix string) (Asset, error) {
	contentType := "application/json"
	if strings.HasSuffix(suffix, ".png") {
		contentType = "image/png"
	}
	return s.cachedAsset(ctx, "sprite"+suffix, fmt.Sprintf("%s/styles/%s/sprite%s", s.base, s.styleName, suffix), contentType)
}

func (s *OlaMapStyle) Tile(ctx context.Context, source string, z, x, y int) (Asset, error) {
	loaded, err := s.load(ctx)
	if err != nil {
		return Asset{}, err
	}
	t, ok := loaded.tiles[source]
	if !ok || z < 0 || z > t.maxZoom || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
		return Asset{}, apiErr(404, "no_tile", "No such tile")
	}
	u := strings.NewReplacer("{z}", strconv.Itoa(z), "{x}", strconv.Itoa(x), "{y}", strconv.Itoa(y)).Replace(t.url)
	return s.cachedAsset(ctx, fmt.Sprintf("t/%s/%d/%d/%d", source, z, x, y), u, "application/x-protobuf")
}

var (
	fontstackRe = regexp.MustCompile(`^[\w ,.-]{1,200}$`)
	rangeRe     = regexp.MustCompile(`^\d{1,5}-\d{1,5}$`)
)

func (s *OlaMapStyle) Font(ctx context.Context, fontstack, rng string) (Asset, error) {
	if !fontstackRe.MatchString(fontstack) || !rangeRe.MatchString(rng) {
		return Asset{}, apiErr(404, "no_font", "No such font")
	}
	return s.cachedAsset(ctx, "f/"+fontstack+"/"+rng,
		fmt.Sprintf("%s/fonts/%s/%s.pbf", s.base, url.PathEscape(fontstack), rng), "application/x-protobuf")
}

var (
	tileFileRe = regexp.MustCompile(`^(\d+)\.pbf$`)
	fontFileRe = regexp.MustCompile(`^(\d+-\d+)\.pbf$`)
)

// mapStyleRoutes serves GET /v1/map/style.json, /v1/map/sprite{.json,.png,
// @2x.json,@2x.png}, /v1/map/tiles/{source}/{z}/{x}/{y}.pbf and
// /v1/map/fonts/{fontstack}/{range}.pbf. Open (no session): map views fetch
// these without our auth header, and they hold nothing about any rider. 404
// when no maps provider is configured: the app then draws its simple map.
func (a *App) mapStyleRoutes(handle func(string, handler)) {
	style := func() (MapStyle, error) {
		if a.MapStyle == nil {
			return nil, apiErr(404, "no_map", "No map provider is configured")
		}
		return a.MapStyle, nil
	}
	send := func(w http.ResponseWriter, as Asset, maxAge int) {
		w.Header().Set("content-type", as.ContentType)
		w.Header().Set("cache-control", fmt.Sprintf("public, max-age=%d", maxAge))
		w.Write(as.Body)
	}

	handle("GET /v1/map/style.json", func(w http.ResponseWriter, r *http.Request) error {
		ms, err := style()
		if err != nil {
			return err
		}
		proto, _, _ := strings.Cut(r.Header.Get("x-forwarded-proto"), ",")
		if proto = strings.TrimSpace(proto); proto == "" {
			proto = "http"
			if r.TLS != nil {
				proto = "https"
			}
		}
		out, err := ms.Style(r.Context(), proto+"://"+r.Host)
		if err != nil {
			return err
		}
		// Short: map views cache the style by URL, so a changed style must not linger.
		w.Header().Set("cache-control", "public, max-age=600")
		writeJSON(w, 200, out)
		return nil
	})

	for _, suffix := range SpriteSuffixes {
		handle("GET /v1/map/sprite"+suffix, func(w http.ResponseWriter, r *http.Request) error {
			ms, err := style()
			if err != nil {
				return err
			}
			as, err := ms.Sprite(r.Context(), suffix)
			if err != nil {
				return err
			}
			send(w, as, 86_400)
			return nil
		})
	}

	handle("GET /v1/map/tiles/{source}/{z}/{x}/{file}", func(w http.ResponseWriter, r *http.Request) error {
		ms, err := style()
		if err != nil {
			return err
		}
		m := tileFileRe.FindStringSubmatch(r.PathValue("file"))
		z, errZ := strconv.Atoi(r.PathValue("z"))
		x, errX := strconv.Atoi(r.PathValue("x"))
		if m == nil || errZ != nil || errX != nil {
			return apiErr(404, "no_tile", "No such tile")
		}
		y, _ := strconv.Atoi(m[1])
		as, err := ms.Tile(r.Context(), r.PathValue("source"), z, x, y)
		if err != nil {
			return err
		}
		send(w, as, 86_400)
		return nil
	})

	handle("GET /v1/map/fonts/{fontstack}/{file}", func(w http.ResponseWriter, r *http.Request) error {
		ms, err := style()
		if err != nil {
			return err
		}
		m := fontFileRe.FindStringSubmatch(r.PathValue("file"))
		if m == nil {
			return apiErr(404, "no_font", "No such font")
		}
		as, err := ms.Font(r.Context(), r.PathValue("fontstack"), m[1])
		if err != nil {
			return err
		}
		send(w, as, 7*86_400)
		return nil
	})
}
