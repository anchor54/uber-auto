package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"commute-backend/internal/domain"
)

var (
	phoneRe = regexp.MustCompile(`^[6-9]\d{9}$`)
	codeRe  = regexp.MustCompile(`^\d{6}$`)
)

const (
	phoneMessage = "Enter a 10-digit mobile number starting with 6, 7, 8 or 9"
	otpTTL       = 5 * time.Minute
	sessionTTL   = 30 * 24 * time.Hour
	maxAttempts  = 5
)

// OTPSender delivers a sign-in code.
type OTPSender interface {
	Send(ctx context.Context, phone, code string) error
}

// OTPSenderFunc lets a function be an OTPSender.
type OTPSenderFunc func(ctx context.Context, phone, code string) error

func (f OTPSenderFunc) Send(ctx context.Context, phone, code string) error {
	return f(ctx, phone, code)
}

// ConsoleOTPSender logs codes instead of sending SMS. Development only.
var ConsoleOTPSender = OTPSenderFunc(func(_ context.Context, phone, code string) error {
	fmt.Printf("[otp] %s: %s\n", phone, code)
	return nil
})

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// codeHash keys codes with a server secret: a leaked table can't be
// reversed by trying all 10^6 codes.
func (a *App) codeHash(phone, code string) string {
	m := hmac.New(sha256.New, []byte(a.Config.OTPSecret))
	m.Write([]byte(phone + ":" + code))
	return hex.EncodeToString(m.Sum(nil))
}

// parsePhone reads {phone}; a bad number is `invalid_phone`, the same code
// the app's demo backend uses.
func parsePhone(r *http.Request) (string, error) {
	var body struct{ Phone *string }
	if err := decode(r, &body); err != nil || body.Phone == nil || !phoneRe.MatchString(*body.Phone) {
		return "", apiErr(400, "invalid_phone", phoneMessage)
	}
	return *body.Phone, nil
}

func randomDigits(n int) string {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(math.Pow10(n))))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%0*d", n, v.Int64())
}

// sendCode makes a code and sends it. Per phone: one send every
// OTPResendSeconds, at most OTPMaxSendsPerHour an hour, and wrong guesses
// count across resends until the hour is up.
func (a *App) sendCode(ctx context.Context, phone, role string) error {
	now := a.now()
	var sends int
	var windowStarted, lastSent time.Time
	err := a.DB.QueryRow(ctx, "select sends, window_started_at, last_sent_at from otp_codes where phone = $1 and role = $2",
		phone, role).Scan(&sends, &windowStarted, &lastSent)
	exists := err == nil
	if err != nil && !noRows(err) {
		return err
	}
	freshWindow := !exists || now.Sub(windowStarted) >= time.Hour
	if exists && !freshWindow {
		wait := int(math.Ceil(float64(lastSent.Add(time.Duration(a.Config.OTPResendSeconds)*time.Second).Sub(now).Milliseconds()) / 1000))
		if wait > 0 {
			return apiErr(429, "resend_too_soon", fmt.Sprintf("Wait %d seconds before asking for another code.", wait))
		}
		if sends >= a.Config.OTPMaxSendsPerHour {
			return apiErr(429, "too_many_codes", "Too many codes asked for. Try again in an hour.")
		}
	}
	code := a.Config.FixedOTP
	if code == "" {
		code = randomDigits(6)
	}
	_, err = a.DB.Exec(ctx, `insert into otp_codes (phone, role, code_hash, expires_at, attempts, sends, window_started_at, last_sent_at)
		values ($1, $2, $3, $4, 0, 1, $5, $5)
		on conflict (phone, role) do update set
		  code_hash = excluded.code_hash,
		  expires_at = excluded.expires_at,
		  last_sent_at = excluded.last_sent_at,
		  sends = case when $6 then 1 else otp_codes.sends + 1 end,
		  attempts = case when $6 then 0 else otp_codes.attempts end,
		  window_started_at = case when $6 then excluded.window_started_at else otp_codes.window_started_at end`,
		phone, role, a.codeHash(phone, code), now.Add(otpTTL), now, freshWindow)
	if err != nil {
		return err
	}
	return a.OTPSender.Send(ctx, phone, code)
}

// checkCode checks the code and uses it up. The same errors for riders and drivers.
func (a *App) checkCode(ctx context.Context, phone, role, code string) error {
	var hash string
	var expires time.Time
	var attempts int
	err := a.DB.QueryRow(ctx, "select code_hash, expires_at, attempts from otp_codes where phone = $1 and role = $2",
		phone, role).Scan(&hash, &expires, &attempts)
	if err != nil && !noRows(err) {
		return err
	}
	if noRows(err) || !expires.After(a.now()) {
		return apiErr(401, "code_expired", "That code has expired. Ask for a new one.")
	}
	if attempts >= maxAttempts {
		return apiErr(429, "too_many_attempts", "Too many wrong codes. Try again in an hour.")
	}
	if !hmac.Equal([]byte(hash), []byte(a.codeHash(phone, code))) {
		if _, err := a.DB.Exec(ctx, "update otp_codes set attempts = attempts + 1 where phone = $1 and role = $2", phone, role); err != nil {
			return err
		}
		return apiErr(401, "invalid_code", "That code isn't right. Check the SMS and try again.")
	}
	_, err = a.DB.Exec(ctx, "delete from otp_codes where phone = $1 and role = $2", phone, role)
	return err
}

func (a *App) newSession(ctx context.Context, role, ownerID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	var riderID, driverID *string
	if role == "rider" {
		riderID = &ownerID
	} else {
		driverID = &ownerID
	}
	_, err := a.DB.Exec(ctx, "insert into sessions (token_hash, role, rider_id, driver_id, expires_at) values ($1, $2, $3, $4, $5)",
		sha256Hex(token), role, riderID, driverID, a.now().Add(sessionTTL))
	return token, err
}

func parseVerify(r *http.Request) (phone, code string, err error) {
	var body struct{ Phone, Code *string }
	if err := decode(r, &body); err != nil {
		return "", "", err
	}
	var c domain.Check
	c.If(body.Phone == nil || !phoneRe.MatchString(*body.Phone), phoneMessage, "phone")
	c.If(body.Code == nil || !codeRe.MatchString(*body.Code), "Invalid", "code")
	if err := c.Err(); err != nil {
		return "", "", err
	}
	return *body.Phone, *body.Code, nil
}

type riderJSON struct {
	ID    string  `json:"id"`
	Phone string  `json:"phone"`
	Name  *string `json:"name"`
}

type driverAccountJSON struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

func (a *App) authRoutes(handle func(string, handler)) {
	handle("POST /v1/auth/otp", func(w http.ResponseWriter, r *http.Request) error {
		phone, err := parsePhone(r)
		if err != nil {
			return err
		}
		if err := a.sendCode(r.Context(), phone, "rider"); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})

	handle("POST /v1/auth/verify", func(w http.ResponseWriter, r *http.Request) error {
		phone, code, err := parseVerify(r)
		if err != nil {
			return err
		}
		if err := a.checkCode(r.Context(), phone, "rider", code); err != nil {
			return err
		}
		var rider riderJSON
		err = a.DB.QueryRow(r.Context(), `insert into riders (phone) values ($1)
			on conflict (phone) do update set phone = excluded.phone
			returning id, phone, name`, phone).Scan(&rider.ID, &rider.Phone, &rider.Name)
		if err != nil {
			return err
		}
		token, err := a.newSession(r.Context(), "rider", rider.ID)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"token": token, "rider": rider})
		return nil
	})

	// Drivers are onboarded by ops; an unknown phone gets no code (and no SMS).
	handle("POST /v1/driver/auth/otp", func(w http.ResponseWriter, r *http.Request) error {
		phone, err := parsePhone(r)
		if err != nil {
			return err
		}
		var known bool
		if err := a.DB.QueryRow(r.Context(), "select exists (select 1 from drivers where phone = $1 and active)", phone).Scan(&known); err != nil {
			return err
		}
		if !known {
			return apiErr(403, "not_a_driver", "This number isn't registered as a driver.")
		}
		if err := a.sendCode(r.Context(), phone, "driver"); err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})

	handle("POST /v1/driver/auth/verify", func(w http.ResponseWriter, r *http.Request) error {
		phone, code, err := parseVerify(r)
		if err != nil {
			return err
		}
		if err := a.checkCode(r.Context(), phone, "driver", code); err != nil {
			return err
		}
		var d driverAccountJSON
		err = a.DB.QueryRow(r.Context(), "select id, name, phone from drivers where phone = $1 and active", phone).Scan(&d.ID, &d.Name, &d.Phone)
		if noRows(err) {
			return apiErr(403, "not_a_driver", "This number isn't registered as a driver.")
		}
		if err != nil {
			return err
		}
		token, err := a.newSession(r.Context(), "driver", d.ID)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"token": token, "driver": d})
		return nil
	})
}

type session struct {
	Role     string
	RiderID  *string
	DriverID *string
}

func (a *App) sessionFor(r *http.Request) (session, error) {
	header := r.Header.Get("authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return session{}, apiErr(401, "unauthorized", "Sign in first")
	}
	var s session
	err := a.DB.QueryRow(r.Context(), "select role, rider_id, driver_id from sessions where token_hash = $1 and expires_at > $2",
		sha256Hex(token), a.now()).Scan(&s.Role, &s.RiderID, &s.DriverID)
	if noRows(err) {
		return s, apiErr(401, "unauthorized", "Your session has ended. Sign in again.")
	}
	return s, err
}

// requireSession is the signed-in rider or driver: their role and id.
func (a *App) requireSession(r *http.Request) (role, id string, err error) {
	s, err := a.sessionFor(r)
	if err != nil {
		return "", "", err
	}
	if s.Role == "rider" {
		return s.Role, str(s.RiderID), nil
	}
	return s.Role, str(s.DriverID), nil
}

// requireRider resolves the signed-in rider from `Authorization: Bearer <token>`.
func (a *App) requireRider(r *http.Request) (string, error) {
	s, err := a.sessionFor(r)
	if err != nil {
		return "", err
	}
	if s.Role != "rider" || s.RiderID == nil {
		return "", apiErr(403, "riders_only", "This needs a rider account.")
	}
	return *s.RiderID, nil
}

// requireDriver resolves the signed-in driver.
func (a *App) requireDriver(r *http.Request) (string, error) {
	s, err := a.sessionFor(r)
	if err != nil {
		return "", err
	}
	if s.Role != "driver" || s.DriverID == nil {
		return "", apiErr(403, "drivers_only", "This needs a driver account.")
	}
	return *s.DriverID, nil
}
