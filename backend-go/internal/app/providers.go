package app

import (
	"errors"
	"log/slog"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Providers are the outside services.
type Providers struct {
	OTPSender OTPSender
	Maps      MapsProvider
	MapStyle  MapStyle
}

// ProvidersFromEnv chooses the outside services from the environment:
//
//	SMS   MSG91_AUTH_KEY + MSG91_OTP_TEMPLATE_ID → MSG91; else codes are logged
//	Maps  OLA_MAPS_API_KEY → Ola Maps; else the seeded demo city.
//	      MAPS_PROVIDER=demo forces the demo city (tests that need its places).
//	      MAP_TILE_CACHE_MB sizes the map tile cache (default 64; 0 turns it off).
//
// In production, logged codes would let anyone with log access sign in as
// anyone, so the server refuses to start without SMS.
func ProvidersFromEnv(getenv func(string) string, pool *pgxpool.Pool, log *slog.Logger) (Providers, error) {
	production := IsProduction(getenv)
	p := Providers{OTPSender: ConsoleOTPSender, Maps: DemoMaps(pool)}

	if key, tmpl := getenv("MSG91_AUTH_KEY"), getenv("MSG91_OTP_TEMPLATE_ID"); key != "" && tmpl != "" {
		p.OTPSender = &MSG91{AuthKey: key, TemplateID: tmpl, Log: log}
	} else if production {
		return p, errors.New("refusing to start in production without MSG91_AUTH_KEY and MSG91_OTP_TEMPLATE_ID")
	}

	if key := getenv("OLA_MAPS_API_KEY"); key != "" && getenv("MAPS_PROVIDER") != "demo" {
		p.Maps = &OlaMaps{APIKey: key}
		mb := 64.0
		if v := getenv("MAP_TILE_CACHE_MB"); v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return p, errors.New("MAP_TILE_CACHE_MB must be a number")
			}
			mb = n
		}
		p.MapStyle = NewOlaMapStyle(key, OlaMapStyleOptions{CacheMB: &mb})
	} else if production && log != nil {
		log.Warn("OLA_MAPS_API_KEY is not set: using the seeded demo places and straight-line distances")
	}
	return p, nil
}
