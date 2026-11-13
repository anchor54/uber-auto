// Plays the driver against a running backend started with ENABLE_DRIVER_SIM=true.
//
//	sim requests                 list commutes waiting for a driver
//	sim accept  [commuteId]      accept (default: the newest request)
//	sim decline [commuteId]      decline
//	sim expire  [commuteId]      act as if the driver never answered
//
//	sim rides [riderPhone]       open rides, soonest first
//	sim ride <rideId|next> <depart|arrive|start|end|no_show|contact>
//	sim play <rideId|next> [speed]   play a whole ride (default 30× real time)
//	sim clock <ISO time | pickup-<minutes>>   move the server clock
//	sim jobs                     run the worker jobs once
//
// SIM_URL picks the backend (default http://localhost:8080).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var base = func() string {
	if v := os.Getenv("SIM_URL"); v != "" {
		return v
	}
	return "http://localhost:8080"
}()

func call(method, path string, body any, out any) error {
	var reader io.Reader
	if method == "POST" {
		if body == nil {
			body = map[string]any{}
		}
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		return err
	}
	if method == "POST" {
		req.Header.Set("content-type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		hint := ""
		if res.StatusCode == 404 && strings.HasPrefix(path, "/dev/sim") {
			hint = " (is the backend running with ENABLE_DRIVER_SIM=true?)"
		}
		return fmt.Errorf("%s %s → %d %s%s", method, path, res.StatusCode, strings.TrimSpace(string(raw)), hint)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type place struct {
	Title string `json:"title"`
}

type request struct {
	ID         string `json:"id"`
	DriverID   string `json:"driverId"`
	Pickup     place  `json:"pickup"`
	Drop       place  `json:"drop"`
	PickupTime int    `json:"pickupTime"`
	StartDate  string `json:"startDate"`
	Fare       int    `json:"fare"`
}

type ride struct {
	ID        string `json:"id"`
	Date      string `json:"date"`
	Direction string `json:"direction"`
	State     string `json:"state"`
	PickupAt  string `json:"pickupAt"`
	Pickup    place  `json:"pickup"`
	Drop      place  `json:"drop"`
	Driver    struct {
		Name string `json:"name"`
	} `json:"driver"`
}

func requests() ([]request, error) {
	var out struct{ Requests []request }
	err := call("GET", "/dev/sim/requests", nil, &out)
	return out.Requests, err
}

func rides(riderPhone string) ([]ride, error) {
	q := ""
	if riderPhone != "" {
		q = "?riderPhone=" + riderPhone
	}
	var out struct{ Rides []ride }
	err := call("GET", "/dev/sim/rides"+q, nil, &out)
	return out.Rides, err
}

func findRide(arg string) (ride, error) {
	list, err := rides("")
	if err != nil {
		return ride{}, err
	}
	if arg == "" || arg == "next" {
		if len(list) == 0 {
			return ride{}, fmt.Errorf("no open rides")
		}
		return list[0], nil
	}
	for _, r := range list {
		if r.ID == arg {
			return r, nil
		}
	}
	return ride{}, fmt.Errorf("no open ride %s", arg)
}

func printJSON(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := arg(args, 0)
	if command == "" {
		command = "requests"
	}
	switch command {
	case "requests":
		list, err := requests()
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No commutes waiting for a driver.")
		}
		for _, r := range list {
			fmt.Printf("%s  %s  %s → %s  %02d:%02d from %s  ₹%d\n", r.ID, r.DriverID, r.Pickup.Title, r.Drop.Title,
				r.PickupTime/60, r.PickupTime%60, r.StartDate, r.Fare)
		}
	case "accept", "decline", "expire":
		target := arg(args, 1)
		if target == "" {
			list, err := requests()
			if err != nil {
				return err
			}
			if len(list) == 0 {
				return fmt.Errorf("no commutes waiting for a driver")
			}
			target = list[len(list)-1].ID
		}
		var out any
		if err := call("POST", "/dev/sim/commutes/"+target+"/"+command, nil, &out); err != nil {
			return err
		}
		printJSON(out)
	case "rides":
		list, err := rides(arg(args, 1))
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No open rides.")
		}
		for _, r := range list {
			fmt.Printf("%s  %s %-8s %-10s %s  %s → %s  %s\n", r.ID, r.Date, r.Direction, r.State, r.PickupAt, r.Pickup.Title, r.Drop.Title, r.Driver.Name)
		}
	case "ride":
		r, err := findRide(arg(args, 1))
		if err != nil {
			return err
		}
		move := arg(args, 2)
		if move == "" {
			return fmt.Errorf("say which move: depart, arrive, start, end, no_show or contact")
		}
		var out ride
		if err := call("POST", "/dev/sim/rides/"+r.ID+"/step", map[string]string{"move": move}, &out); err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", out.ID, out.State)
	case "play":
		r, err := findRide(arg(args, 1))
		if err != nil {
			return err
		}
		speed := 30.0
		if v := arg(args, 2); v != "" {
			if speed, err = strconv.ParseFloat(v, 64); err != nil {
				return fmt.Errorf("speed must be a number")
			}
		}
		var out any
		if err := call("POST", "/dev/sim/rides/"+r.ID+"/play", map[string]float64{"speed": speed}, &out); err != nil {
			return err
		}
		printJSON(out)
	case "clock":
		a := arg(args, 1)
		if a == "" {
			return fmt.Errorf("give an ISO time, or pickup-<minutes> to jump to before the next pickup")
		}
		now := a
		if m := regexp.MustCompile(`^pickup-(\d+)$`).FindStringSubmatch(a); m != nil {
			r, err := findRide("next")
			if err != nil {
				return err
			}
			at, err := time.Parse(time.RFC3339Nano, r.PickupAt)
			if err != nil {
				return err
			}
			mins, _ := strconv.Atoi(m[1])
			now = at.Add(-time.Duration(mins) * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
		}
		var out any
		if err := call("POST", "/dev/sim/clock", map[string]string{"now": now}, &out); err != nil {
			return err
		}
		printJSON(out)
	case "jobs":
		var out any
		if err := call("POST", "/dev/sim/jobs", nil, &out); err != nil {
			return err
		}
		printJSON(out)
	default:
		return fmt.Errorf("unknown command %q: use requests, accept, decline, expire, rides, ride, play, clock or jobs", command)
	}
	return nil
}
