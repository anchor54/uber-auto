// The admin tool for flow D1: review drivers and confirm their payments by
// calling the API's admin endpoints.
//
//	go run ./cmd/admin login <phone>                       sign in (asks for the code)
//	go run ./cmd/admin pending                             drivers waiting and payments claimed
//	go run ./cmd/admin show <driver>                       every item, with 10-minute links to files
//	go run ./cmd/admin verify <driver> <item>
//	go run ./cmd/admin reject-item <driver> <item> "<reason>"
//	go run ./cmd/admin approve <driver>
//	go run ./cmd/admin reject <driver> "<reason>"
//	go run ./cmd/admin confirm-payment <payment>
//	go run ./cmd/admin reject-payment <payment> "<reason>"
//
// API_URL is the API (default http://localhost:8080). The token comes from
// ADMIN_TOKEN, else from ~/.commute-admin-token (written by login).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const usage = `usage: admin <command>
  login <phone>                          sign in; saves the token to ~/.commute-admin-token
  pending                                drivers waiting for review and payments claimed
  show <driver>                          a driver's items, with 10-minute links to the files
  verify <driver> <item>                 mark an item verified (item: licence, rc, vehicle, ...)
  reject-item <driver> <item> "<reason>" mark an item rejected
  approve <driver>                       approve a driver whose items are all verified
  reject <driver> "<reason>"             turn the application down for good
  confirm-payment <payment>              the money arrived: the driver goes live
  reject-payment <payment> "<reason>"    the money didn't arrive

env: API_URL (default http://localhost:8080), ADMIN_TOKEN`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type client struct {
	base  string
	token string
	http  *http.Client
}

// apiError is an error reply from the API.
type apiError struct {
	Status  int
	Code    string `json:"error"`
	Message string `json:"message"`
	Missing []string
	Items   []string `json:"items"`
}

func (e *apiError) Error() string {
	s := fmt.Sprintf("%s (%d %s)", e.Message, e.Status, e.Code)
	if len(e.Items) > 0 {
		s += "\n  not verified: " + strings.Join(e.Items, ", ")
	}
	return s
}

func (c *client) call(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		e := &apiError{Status: res.StatusCode}
		if json.Unmarshal(raw, e) != nil || e.Code == "" {
			return fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, strings.TrimSpace(string(raw)))
		}
		return e
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func tokenFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".commute-admin-token"
	}
	return filepath.Join(home, ".commute-admin-token")
}

func run(args []string, stdin io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(out, usage)
		return nil
	}
	base := strings.TrimRight(os.Getenv("API_URL"), "/")
	if base == "" {
		base = "http://localhost:8080"
	}
	c := &client{base: base, token: os.Getenv("ADMIN_TOKEN"), http: &http.Client{Timeout: 20 * time.Second}}
	if c.token == "" && args[0] != "login" {
		if b, err := os.ReadFile(tokenFile()); err == nil {
			c.token = strings.TrimSpace(string(b))
		}
		if c.token == "" {
			return errors.New("not signed in: run `admin login <phone>` or set ADMIN_TOKEN")
		}
	}
	need := func(n int, what string) error {
		if len(args)-1 != n {
			return fmt.Errorf("usage: admin %s %s", args[0], what)
		}
		return nil
	}
	esc := url.PathEscape
	switch args[0] {
	case "login":
		if err := need(1, "<phone>"); err != nil {
			return err
		}
		return login(c, args[1], stdin, out)
	case "pending":
		if err := need(0, ""); err != nil {
			return err
		}
		return pending(c, out)
	case "show":
		if err := need(1, "<driver>"); err != nil {
			return err
		}
		var d driver
		if err := c.call("GET", "/v1/admin/drivers/"+esc(args[1]), nil, &d); err != nil {
			return err
		}
		printDriver(out, d, true)
	case "verify", "reject-item":
		body := map[string]any{"decision": "verified"}
		if args[0] == "verify" {
			if err := need(2, "<driver> <item>"); err != nil {
				return err
			}
		} else {
			if err := need(3, `<driver> <item> "<reason>"`); err != nil {
				return err
			}
			body = map[string]any{"decision": "rejected", "reason": args[3]}
		}
		var res struct {
			Item   item   `json:"item"`
			Driver driver `json:"driver"`
		}
		if err := c.call("POST", "/v1/admin/drivers/"+esc(args[1])+"/items/"+esc(args[2])+"/review", body, &res); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s: %s\n", res.Item.Label, res.Item.State)
		fmt.Fprintf(out, "%s is now %s\n", res.Driver.Name, res.Driver.Status)
	case "approve":
		if err := need(1, "<driver>"); err != nil {
			return err
		}
		var d driver
		if err := c.call("POST", "/v1/admin/drivers/"+esc(args[1])+"/approve", map[string]any{}, &d); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s is %s. They've been told to choose their plan.\n", d.Name, d.Status)
	case "reject":
		if err := need(2, `<driver> "<reason>"`); err != nil {
			return err
		}
		var d driver
		if err := c.call("POST", "/v1/admin/drivers/"+esc(args[1])+"/reject", map[string]any{"reason": args[2]}, &d); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s is %s: %s\n", d.Name, d.Status, deref(d.RejectReason))
	case "confirm-payment", "reject-payment":
		path, body := "/confirm", map[string]any{}
		if args[0] == "confirm-payment" {
			if err := need(1, "<payment>"); err != nil {
				return err
			}
		} else {
			if err := need(2, `<payment> "<reason>"`); err != nil {
				return err
			}
			path, body = "/reject", map[string]any{"reason": args[2]}
		}
		var res struct {
			Payment payment `json:"payment"`
			Driver  driver  `json:"driver"`
		}
		if err := c.call("POST", "/v1/admin/payments/"+esc(args[1])+path, body, &res); err != nil {
			return err
		}
		fmt.Fprintf(out, "Payment of ₹%d (%s – %s) %s\n", res.Payment.Amount, res.Payment.PeriodStart, res.Payment.PeriodEnd, res.Payment.Status)
		fmt.Fprintf(out, "%s is %s, paid until %s\n", res.Driver.Name, res.Driver.Status, orDash(res.Driver.SubscriptionUntil))
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
	return nil
}

func login(c *client, phone string, stdin io.Reader, out io.Writer) error {
	if err := c.call("POST", "/v1/admin/auth/otp", map[string]string{"phone": phone}, nil); err != nil {
		return err
	}
	fmt.Fprint(out, "Code sent. Enter the code: ")
	code, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && code == "" {
		return errors.New("no code entered")
	}
	var res struct{ Token string }
	if err := c.call("POST", "/v1/admin/auth/verify", map[string]string{"phone": phone, "code": strings.TrimSpace(code)}, &res); err != nil {
		return err
	}
	if err := os.WriteFile(tokenFile(), []byte(res.Token+"\n"), 0o600); err != nil {
		fmt.Fprintf(out, "Signed in. Could not save the token (%v); use ADMIN_TOKEN=%s\n", err, res.Token)
		return nil
	}
	fmt.Fprintf(out, "Signed in. Token saved to %s\n", tokenFile())
	return nil
}

type item struct {
	Kind         string  `json:"kind"`
	Label        string  `json:"label"`
	State        string  `json:"state"`
	RejectReason *string `json:"rejectReason"`
	Value        *string `json:"value"`
	URL          *string `json:"url"`
}

type payment struct {
	ID           string  `json:"id"`
	PeriodStart  string  `json:"periodStart"`
	PeriodEnd    string  `json:"periodEnd"`
	Amount       int     `json:"amount"`
	Status       string  `json:"status"`
	ClaimedAt    string  `json:"claimedAt"`
	RejectReason *string `json:"rejectReason"`
}

type driver struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Phone             string   `json:"phone"`
	City              string   `json:"city"`
	Status            string   `json:"status"`
	RejectReason      *string  `json:"rejectReason"`
	Reference         *string  `json:"reference"`
	DayOff            *int     `json:"dayOff"`
	DayOffSet         bool     `json:"dayOffSet"`
	SubscriptionUntil *string  `json:"subscriptionUntil"`
	Items             []item   `json:"items"`
	Missing           []string `json:"missing"`
	Vehicle           *struct {
		Type          string  `json:"type"`
		CabClass      *string `json:"cabClass"`
		MakeModel     string  `json:"makeModel"`
		Year          int     `json:"year"`
		Plate         string  `json:"plate"`
		Colour        string  `json:"colour"`
		AC            bool    `json:"ac"`
		CngOrElectric bool    `json:"cngOrElectric"`
		ReviewState   string  `json:"reviewState"`
		RejectReason  *string `json:"rejectReason"`
	} `json:"vehicle"`
	Plan *struct {
		Name         string `json:"name"`
		MonthlyPrice int    `json:"monthlyPrice"`
	} `json:"plan"`
	LatestPayment *payment `json:"latestPayment"`
}

func pending(c *client, out io.Writer) error {
	var res struct {
		Drivers []struct {
			ID, Name, Phone, City, Status string
			SubmittedAt                   *string
		}
		Payments []struct {
			ID, DriverID, DriverName string
			Reference                *string
			PeriodStart, PeriodEnd   string
			Amount                   int
			ClaimedAt                string
		}
	}
	if err := c.call("GET", "/v1/admin/pending", nil, &res); err != nil {
		return err
	}
	fmt.Fprintf(out, "Drivers waiting for review (%d)\n", len(res.Drivers))
	for _, d := range res.Drivers {
		fmt.Fprintf(out, "  %-22s %-20s %s  %s  submitted %s\n", d.ID, d.Name, d.Phone, d.City, when(d.SubmittedAt))
	}
	fmt.Fprintf(out, "\nPayments claimed (%d)\n", len(res.Payments))
	for _, p := range res.Payments {
		fmt.Fprintf(out, "  %s  ₹%-6d %s  %s (%s)  %s – %s  claimed %s\n", p.ID, p.Amount, orDash(p.Reference), p.DriverName,
			p.DriverID, p.PeriodStart, p.PeriodEnd, when(&p.ClaimedAt))
	}
	return nil
}

var weekdays = []string{"", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func printDriver(out io.Writer, d driver, links bool) {
	fmt.Fprintf(out, "%s (%s)  %s  %s\n", d.Name, d.ID, d.Phone, d.City)
	fmt.Fprintf(out, "Status: %s", d.Status)
	if d.RejectReason != nil {
		fmt.Fprintf(out, " (%s)", *d.RejectReason)
	}
	fmt.Fprintf(out, "   Reference: %s\n", orDash(d.Reference))
	switch {
	case !d.DayOffSet:
		fmt.Fprintln(out, "Day off: not chosen")
	case d.DayOff == nil:
		fmt.Fprintln(out, "Day off: no fixed day off")
	case *d.DayOff >= 1 && *d.DayOff <= 7:
		fmt.Fprintf(out, "Day off: %s\n", weekdays[*d.DayOff])
	}
	fmt.Fprintln(out, "\nItems")
	for _, it := range d.Items {
		line := fmt.Sprintf("  %-17s %-16s %-9s", it.Kind, it.Label, it.State)
		if it.Value != nil {
			line += " " + *it.Value
		}
		if it.RejectReason != nil {
			line += "  reason: " + *it.RejectReason
		}
		fmt.Fprintln(out, strings.TrimRight(line, " "))
		if links && it.URL != nil {
			fmt.Fprintf(out, "      %s\n", *it.URL)
		}
	}
	if v := d.Vehicle; v != nil {
		class := ""
		if v.CabClass != nil {
			class = " " + *v.CabClass
		}
		fmt.Fprintf(out, "  %-17s %-16s %-9s %s%s, %s, %d, %s, AC %s, CNG/electric %s", "vehicle", "Vehicle details", v.ReviewState,
			v.Type, class, v.MakeModel, v.Year, v.Plate, yesNo(v.AC), yesNo(v.CngOrElectric))
		fmt.Fprintf(out, ", %s", v.Colour)
		if v.RejectReason != nil {
			fmt.Fprintf(out, "  reason: %s", *v.RejectReason)
		}
		fmt.Fprintln(out)
	} else {
		fmt.Fprintf(out, "  %-17s %-16s %s\n", "vehicle", "Vehicle details", "missing")
	}
	if len(d.Missing) > 0 {
		fmt.Fprintf(out, "\nStill missing: %s\n", strings.Join(d.Missing, ", "))
	}
	if d.Plan != nil {
		fmt.Fprintf(out, "\nPlan: %s, ₹%d/month   Paid until: %s\n", d.Plan.Name, d.Plan.MonthlyPrice, orDash(d.SubscriptionUntil))
	}
	if p := d.LatestPayment; p != nil {
		fmt.Fprintf(out, "Latest payment: %s  ₹%d  %s – %s  %s", p.ID, p.Amount, p.PeriodStart, p.PeriodEnd, p.Status)
		if p.RejectReason != nil {
			fmt.Fprintf(out, " (%s)", *p.RejectReason)
		}
		fmt.Fprintln(out)
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "–"
	}
	return *s
}

// when is a timestamp in IST, as the admin reads it.
func when(s *string) string {
	if s == nil {
		return "–"
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return *s
	}
	return t.In(time.FixedZone("IST", 330*60)).Format("Mon 2 Jan 15:04")
}
