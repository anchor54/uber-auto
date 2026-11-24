// The POC admin tool (D1): reviews drivers and payments through the admin
// endpoints, signed in as an admin account.
//
//	admin pending                               submitted drivers and claimed payments, oldest first
//	admin show <driver>                         each item, with a 10-minute signed link to the file
//	admin verify <driver> <item>
//	admin reject-item <driver> <item> "<reason>"
//	admin approve <driver>
//	admin reject <driver> "<reason>"
//	admin confirm-payment <payment>
//	admin reject-payment <payment> "<reason>"
//
// Items: profile_photo, id_proof, licence, rc, insurance, permit, upi,
// vehicle, vehicle_front, vehicle_back, vehicle_interior.
// ADMIN_URL picks the backend (default http://localhost:8080); ADMIN_PHONE
// the admin's number (default 9000000001); ADMIN_CODE the sign-in code
// (default 123456, dev builds); ADMIN_TOKEN skips the sign-in.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

var base = strings.TrimRight(env("ADMIN_URL", "http://localhost:8080"), "/")

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "admin:", err)
		os.Exit(1)
	}
}

func call(method, path, token string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode/100 != 2 {
		return b, fmt.Errorf("%s %s: %d %s", method, path, res.StatusCode, b)
	}
	return b, nil
}

func signIn() (string, error) {
	if t := os.Getenv("ADMIN_TOKEN"); t != "" {
		return t, nil
	}
	phone := env("ADMIN_PHONE", "9000000001")
	if _, err := call("POST", "/v1/auth/otp", "", map[string]string{"phone": phone}); err != nil {
		return "", err
	}
	b, err := call("POST", "/v1/auth/verify", "", map[string]string{"phone": phone, "code": env("ADMIN_CODE", "123456")})
	if err != nil {
		return "", err
	}
	var v struct {
		Token string `json:"token"`
		User  struct {
			Role string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	if v.User.Role != "admin" {
		return "", fmt.Errorf("%s is not an admin account", phone)
	}
	return v.Token, nil
}

func need(args []string, n int, usage string) error {
	if len(args) < n {
		return fmt.Errorf("usage: admin %s", usage)
	}
	return nil
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: admin pending | show | verify | reject-item | approve | reject | confirm-payment | reject-payment")
	}
	token, err := signIn()
	if err != nil {
		return err
	}
	var out []byte
	switch cmd, a := args[0], args[1:]; cmd {
	case "pending":
		out, err = call("GET", "/v1/admin/pending", token, nil)
		if err == nil {
			return printPending(out)
		}
	case "show":
		if err := need(a, 1, "show <driver>"); err != nil {
			return err
		}
		out, err = call("GET", "/v1/admin/drivers/"+a[0], token, nil)
		if err == nil {
			return printDriver(out)
		}
	case "verify":
		if err := need(a, 2, "verify <driver> <item>"); err != nil {
			return err
		}
		out, err = call("POST", "/v1/admin/drivers/"+a[0]+"/review-item", token, map[string]string{"item": a[1], "state": "verified"})
	case "reject-item":
		if err := need(a, 3, `reject-item <driver> <item> "<reason>"`); err != nil {
			return err
		}
		out, err = call("POST", "/v1/admin/drivers/"+a[0]+"/review-item", token,
			map[string]string{"item": a[1], "state": "rejected", "reason": a[2]})
	case "approve":
		if err := need(a, 1, "approve <driver>"); err != nil {
			return err
		}
		out, err = call("POST", "/v1/admin/drivers/"+a[0]+"/approve", token, nil)
	case "reject":
		if err := need(a, 2, `reject <driver> "<reason>"`); err != nil {
			return err
		}
		out, err = call("POST", "/v1/admin/drivers/"+a[0]+"/reject", token, map[string]string{"reason": a[1]})
	case "confirm-payment":
		if err := need(a, 1, "confirm-payment <payment>"); err != nil {
			return err
		}
		out, err = call("POST", "/v1/admin/payments/"+a[0]+"/confirm", token, nil)
	case "reject-payment":
		if err := need(a, 2, `reject-payment <payment> "<reason>"`); err != nil {
			return err
		}
		out, err = call("POST", "/v1/admin/payments/"+a[0]+"/reject", token, map[string]string{"reason": a[1]})
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, out, "", "  ") == nil {
		out = pretty.Bytes()
	}
	fmt.Println(string(out))
	return nil
}

func printPending(b []byte) error {
	var v struct {
		Drivers []struct {
			ID, Name, Reference, Phone string
			SubmittedAt                *string `json:"submitted_at"`
		} `json:"drivers"`
		Payments []struct {
			ID, DriverName, Reference string
			Amount                    int
			PeriodStart, PeriodEnd    string
			ClaimedAt                 string `json:"claimed_at"`
		} `json:"payments"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	fmt.Printf("Drivers waiting for review (%d)\n", len(v.Drivers))
	for _, d := range v.Drivers {
		at := ""
		if d.SubmittedAt != nil {
			at = *d.SubmittedAt
		}
		fmt.Printf("  %s  %-20s %s  %s  submitted %s\n", d.ID, d.Name, d.Reference, d.Phone, at)
	}
	fmt.Printf("Payments to confirm (%d)\n", len(v.Payments))
	for _, p := range v.Payments {
		fmt.Printf("  %s  %-20s %s  ₹%d  claimed %s\n", p.ID, p.DriverName, p.Reference, p.Amount, p.ClaimedAt)
	}
	return nil
}

func printDriver(b []byte) error {
	var v struct {
		ID, Name, Status, Reference, Phone, City string
		Items                                    []struct {
			Kind, Label, State string
			RejectReason       *string `json:"reject_reason"`
			URL                *string `json:"url"`
			Value              *string `json:"value"`
		} `json:"items"`
		Vehicle *struct {
			Type, Plate, Colour string
			MakeModel           string  `json:"make_model"`
			CabClass            *string `json:"cab_class"`
		} `json:"vehicle"`
		DayOffName *string `json:"day_off_name"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	fmt.Printf("%s (%s) %s · %s · %s\n", v.Name, v.Reference, v.Phone, v.City, v.Status)
	if v.Vehicle != nil {
		fmt.Printf("Vehicle: %s %s %s %s\n", v.Vehicle.Type, v.Vehicle.MakeModel, v.Vehicle.Colour, v.Vehicle.Plate)
	}
	day := "no fixed day off"
	if v.DayOffName != nil {
		day = *v.DayOffName
	}
	fmt.Println("Day off:", day)
	for _, it := range v.Items {
		line := fmt.Sprintf("  %-17s %-9s", it.Kind, it.State)
		if it.Value != nil {
			line += " " + *it.Value
		}
		if it.RejectReason != nil {
			line += " (" + *it.RejectReason + ")"
		}
		if it.URL != nil {
			line += "\n      " + *it.URL
		}
		fmt.Println(line)
	}
	return nil
}
