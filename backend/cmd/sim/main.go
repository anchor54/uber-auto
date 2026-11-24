// Plays the drivers against a running backend until the driver app exists.
//
//	sim offers   --drivers ravi                       a driver's open offers
//	sim interested --series <id> --drivers ravi,meena,anil
//	                                                  mark drivers interested in the series' open offer
//	sim withdraw   --series <id> --drivers ravi       withdraw their interest
//	sim close-offers [--advance 15]                   move the server clock forward (dev routes) and
//	                                                  run the sweep at once instead of waiting a minute
//	sim sweep                                         run the sweep once
//	sim run   --drivers ravi [--from 2026-11-18 --days 1]
//	                                                  a driver's run (D3.1 Today)
//	sim drive --drivers ravi [--ride <id>] [--pay upi] [--wait]
//	                                                  play the driver through a ride (D3): On my way, three
//	                                                  positions towards the pickup, I've arrived, Start ride,
//	                                                  End ride, payment mark. --ride defaults to the next
//	                                                  assigned ride today; with --wait the dev clock is moved
//	                                                  forward to when On my way opens (ENABLE_DEV_ROUTES=true)
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
	"time"

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
	from := fs.String("from", "", "first date of the run (YYYY-MM-DD; default today)")
	days := fs.Int("days", 1, "days in the run (max 14)")
	rideID := fs.String("ride", "", "ride id (or prefix) for drive")
	pay := fs.String("pay", "upi", "payment mark after End ride: upi, cash or not_paid")
	wait := fs.Bool("wait", false, "move the dev clock to when On my way opens (drive)")
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
	case "run":
		for _, d := range names {
			token, err := signIn(d)
			if err != nil {
				return err
			}
			run, err := driverRun(token, *from, *days)
			if err != nil {
				return err
			}
			fmt.Printf("%s: %s..%s, %d rides\n", d, run.From, run.To, len(run.Rides))
			for _, r := range run.Rides {
				fmt.Printf("  ride %s  %s %s  %-12s %s · %s → %s ₹%d\n", r.ID, r.LocalDate, r.LocalPickupTime, r.Status,
					r.Rider.FirstName, r.Pickup.Label, r.Drop.Label, r.Fare)
			}
		}
	case "drive":
		if len(names) != 1 {
			return errors.New("--drivers takes one driver for drive")
		}
		return drive(names[0], *rideID, *pay, *wait)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}

func driverRun(token, from string, days int) (offers.Run, error) {
	var run offers.Run
	path := fmt.Sprintf("/v1/driver/run?days=%d", days)
	if from != "" {
		path += "&from=" + from
	}
	err := call("GET", path, token, nil, &run)
	return run, err
}

// drive plays a driver through one ride, step by step.
func drive(driver, rideID, pay string, wait bool) error {
	token, err := signIn(driver)
	if err != nil {
		return err
	}
	run, err := driverRun(token, "", 1)
	if err != nil {
		return err
	}
	var ride *offers.RideView
	for i, r := range run.Rides {
		if (rideID != "" && strings.HasPrefix(r.ID.String(), strings.ToLower(rideID))) || (rideID == "" && r.Status == "assigned") {
			ride = &run.Rides[i]
			break
		}
	}
	if ride == nil {
		return errors.New("no matching ride in today's run (try --ride with an id from `sim run`)")
	}
	id := ride.ID.String()
	fmt.Printf("ride %s: %s %s, %s → %s\n", id, ride.LocalDate, ride.LocalPickupTime, ride.Pickup.Label, ride.Drop.Label)
	step := func(action string, body any) error {
		var v offers.RideView
		if err := call("POST", "/v1/driver/rides/"+id+"/"+action, token, body, &v); err != nil {
			return err
		}
		fmt.Printf("  %-10s → %s (%s)\n", action, v.Status, v.StateLabel)
		return nil
	}
	if ride.Status == "assigned" {
		err := step("on-my-way", nil)
		if err != nil && wait && strings.Contains(err.Error(), "too_early") {
			var now struct{ Now time.Time }
			if err := call("POST", "/dev/clock/advance", "", map[string]float64{"minutes": 1}, &now); err != nil {
				return err
			}
			mins := ride.OnMyWayOpensAt.Sub(now.Now).Minutes()
			if mins > 0 {
				if err := call("POST", "/dev/clock/advance", "", map[string]float64{"minutes": mins + 0.1}, nil); err != nil {
					return err
				}
			}
			err = step("on-my-way", nil)
		}
		if err != nil {
			return err
		}
	}
	// Three positions closing in on the pickup.
	for _, f := range []float64{0.02, 0.01, 0.002} {
		body := map[string]any{"lat": ride.Pickup.Lat + f, "lng": ride.Pickup.Lng, "accuracy": 10}
		if err := call("POST", "/v1/driver/rides/"+id+"/location", token, body, nil); err != nil {
			return err
		}
		fmt.Printf("  location   (%.4f, %.4f)\n", ride.Pickup.Lat+f, ride.Pickup.Lng)
	}
	for _, a := range []string{"arrived", "start", "end"} {
		if err := step(a, map[string]any{}); err != nil {
			return err
		}
	}
	if pay != "" {
		return step("payment", map[string]string{"mark": pay})
	}
	return nil
}
