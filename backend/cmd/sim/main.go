// Plays the drivers against a running backend until the driver app exists.
//
//	sim offers   --drivers ravi                       a driver's open offers
//	sim interested --series <id> --drivers ravi,meena,anil
//	                                                  mark drivers interested in the series' open offer
//	sim withdraw   --series <id> --drivers ravi       withdraw their interest
//	sim close-offers [--advance 15]                   move the server clock forward (dev routes) and
//	                                                  run the sweep at once instead of waiting a minute
//	sim sweep                                         run the sweep once
//
// Drivers are seeded first names (ravi, meena, anil, suresh, vijay) or phone
// numbers. SIM_URL picks the backend (default http://localhost:8080);
// FIXED_OTP is the sign-in code (default 123456); SWEEP_TOKEN is sent to
// /internal/sweep when set. --series accepts an id prefix.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"commute-backend/internal/offers"
	"commute-backend/internal/seed"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sim:", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var base = env("SIM_URL", "http://localhost:8080")

func call(method, path, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func phoneOf(driver string) (string, error) {
	for _, p := range seed.SpecPeople {
		if p.Role == "driver" && strings.EqualFold(offers.FirstName(p.Name), driver) {
			return p.Phone, nil
		}
	}
	if len(driver) == 10 {
		return driver, nil
	}
	return "", fmt.Errorf("unknown driver %q", driver)
}

func signIn(driver string) (string, error) {
	phone, err := phoneOf(driver)
	if err != nil {
		return "", err
	}
	if err := call("POST", "/v1/driver/auth/otp", "", map[string]string{"phone": phone}, nil); err != nil {
		return "", err
	}
	var out struct{ Token string }
	err = call("POST", "/v1/driver/auth/verify", "", map[string]string{"phone": phone, "code": env("FIXED_OTP", "123456")}, &out)
	return out.Token, err
}

func openOffer(token, series string) (string, error) {
	var list offers.OffersList
	if err := call("GET", "/v1/offers", token, nil, &list); err != nil {
		return "", err
	}
	for _, o := range list.Offers {
		if strings.HasPrefix(o.SeriesID.String(), strings.ToLower(series)) {
			return o.ID.String(), nil
		}
	}
	return "", fmt.Errorf("no open offer for series %s", series)
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: sim offers | interested | withdraw | close-offers | sweep (see the file comment)")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	series := fs.String("series", "", "series id (or prefix)")
	drivers := fs.String("drivers", "", "comma-separated driver first names or phones")
	advance := fs.Float64("advance", 15, "minutes to move the server clock before sweeping (close-offers)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	names := strings.FieldsFunc(*drivers, func(r rune) bool { return r == ',' || r == ' ' })
	switch args[0] {
	case "offers":
		for _, d := range names {
			token, err := signIn(d)
			if err != nil {
				return err
			}
			var list offers.OffersList
			if err := call("GET", "/v1/offers", token, nil, &list); err != nil {
				return err
			}
			fmt.Printf("%s: %d open\n", d, len(list.Offers))
			for _, o := range list.Offers {
				fmt.Printf("  offer %s  series %s  round %d  closes %s  interested %v  %s → %s ₹%d\n", o.ID, o.SeriesID, o.Round,
					o.ExpiresAt.Format("15:04:05"), o.Interested, o.Pickup.Label, o.Drop.Label, o.FarePerRide)
			}
		}
	case "interested", "withdraw":
		if *series == "" || len(names) == 0 {
			return errors.New("--series and --drivers are required")
		}
		path := "/mark-interested"
		if args[0] == "withdraw" {
			path = "/withdraw-interest"
		}
		for _, d := range names {
			token, err := signIn(d)
			if err != nil {
				return err
			}
			id, err := openOffer(token, *series)
			if err != nil {
				return fmt.Errorf("%s: %w", d, err)
			}
			if err := call("POST", "/v1/offers/"+id+path, token, nil, nil); err != nil {
				return fmt.Errorf("%s: %w", d, err)
			}
			fmt.Printf("%s: %s on offer %s\n", d, args[0], id)
		}
	case "close-offers", "sweep":
		if args[0] == "close-offers" && *advance > 0 {
			if err := call("POST", "/dev/clock/advance", "", map[string]float64{"minutes": *advance}, nil); err != nil {
				return fmt.Errorf("moving the clock needs ENABLE_DEV_ROUTES=true on the server: %w", err)
			}
		}
		var counts any
		if err := call("POST", "/internal/sweep", env("SWEEP_TOKEN", ""), nil, &counts); err != nil {
			return err
		}
		b, _ := json.Marshal(counts)
		fmt.Println("swept:", string(b))
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}
