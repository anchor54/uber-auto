package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"commute-backend/internal/offers"
)

type contextT = context.Context

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

// ConsoleOTPSender prints codes instead of sending SMS. Development only.
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
func (s *Server) codeHash(phone, code string) string {
	m := hmac.New(sha256.New, []byte(s.OTPSecret))
	m.Write([]byte(phone + ":" + code))
	return hex.EncodeToString(m.Sum(nil))
}

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
// OTPResendSeconds, at most OTPMaxSendsPerHour an hour.
func (s *Server) sendCode(ctx context.Context, phone, role string) error {
	now := s.now()
	var sends int
	var windowStarted, lastSent time.Time
	err := s.DB.QueryRow(ctx, "select sends, window_started_at, last_sent_at from otp_codes where phone = $1 and role = $2",
		phone, role).Scan(&sends, &windowStarted, &lastSent)
	exists := err == nil
	if err != nil && !noRows(err) {
		return err
	}
	freshWindow := !exists || now.Sub(windowStarted) >= time.Hour
	if exists && !freshWindow {
		wait := int(math.Ceil(float64(lastSent.Add(time.Duration(s.OTPResendSeconds)*time.Second).Sub(now).Milliseconds()) / 1000))
		if wait > 0 {
			return apiErr(429, "resend_too_soon", fmt.Sprintf("Wait %d seconds before asking for another code.", wait))
		}
		if sends >= s.OTPMaxSendsPerHour {
			return apiErr(429, "too_many_codes", "Too many codes asked for. Try again in an hour.")
		}
	}
	code := s.FixedOTP
	if code == "" {
		code = randomDigits(6)
	}
	_, err = s.DB.Exec(ctx, `insert into otp_codes (phone, role, code_hash, expires_at, attempts, sends, window_started_at, last_sent_at)
		values ($1, $2, $3, $4, 0, 1, $5, $5)
		on conflict (phone, role) do update set
		  code_hash = excluded.code_hash,
		  expires_at = excluded.expires_at,
		  last_sent_at = excluded.last_sent_at,
		  sends = case when $6 then 1 else otp_codes.sends + 1 end,
		  attempts = case when $6 then 0 else otp_codes.attempts end,
		  window_started_at = case when $6 then excluded.window_started_at else otp_codes.window_started_at end`,
		phone, role, s.codeHash(phone, code), now.Add(otpTTL), now, freshWindow)
	if err != nil {
		return err
	}
	sender := s.OTPSender
	if sender == nil {
		sender = ConsoleOTPSender
	}
	return sender.Send(ctx, phone, code)
}

// checkCode checks the code and uses it up.
func (s *Server) checkCode(ctx context.Context, phone, role, code string) error {
	var hash string
	var expires time.Time
	var attempts int
	err := s.DB.QueryRow(ctx, "select code_hash, expires_at, attempts from otp_codes where phone = $1 and role = $2",
		phone, role).Scan(&hash, &expires, &attempts)
	if err != nil && !noRows(err) {
		return err
	}
	if noRows(err) || !expires.After(s.now()) {
		return apiErr(401, "code_expired", "That code has expired. Ask for a new one.")
	}
	if attempts >= maxAttempts {
		return apiErr(429, "too_many_attempts", "Too many wrong codes. Try again in an hour.")
	}
	if !hmac.Equal([]byte(hash), []byte(s.codeHash(phone, code))) {
		if _, err := s.DB.Exec(ctx, "update otp_codes set attempts = attempts + 1 where phone = $1 and role = $2", phone, role); err != nil {
			return err
		}
		return apiErr(401, "invalid_code", "That code isn't right. Check the SMS and try again.")
	}
	_, err = s.DB.Exec(ctx, "delete from otp_codes where phone = $1 and role = $2", phone, role)
	return err
}

func (s *Server) newSession(ctx context.Context, userID uuid.UUID, role string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	_, err := s.DB.Exec(ctx, "insert into sessions (token_hash, user_id, role, expires_at, created_at) values ($1, $2, $3, $4, $5)",
		sha256Hex(token), userID, role, s.now().Add(sessionTTL), s.now())
	return token, err
}

func parseVerify(r *http.Request) (phone, code string, err error) {
	var body struct{ Phone, Code *string }
	if err := decode(r, &body); err != nil {
		return "", "", err
	}
	if body.Phone == nil || !phoneRe.MatchString(*body.Phone) {
		return "", "", apiErr(400, "invalid_phone", phoneMessage)
	}
	if body.Code == nil || !codeRe.MatchString(*body.Code) {
		return "", "", apiErr(400, "invalid_code", "The code is 6 digits")
	}
	return *body.Phone, *body.Code, nil
}

// UserView is the signed-in user.
type UserView struct {
	ID    uuid.UUID `json:"id"`
	Phone string    `json:"phone"`
	Role  string    `json:"role"`
	Name  string    `json:"name"`
}

func (s *Server) userByPhone(ctx context.Context, phone string) (UserView, bool, error) {
	var u UserView
	err := s.DB.QueryRow(ctx, `select u.id, u.phone, u.role, coalesce(r.name, d.name, a.name, '') from users u
		left join riders r on r.user_id = u.id left join drivers d on d.user_id = u.id
		left join admins a on a.user_id = u.id where u.phone = $1`, phone).
		Scan(&u.ID, &u.Phone, &u.Role, &u.Name)
	if noRows(err) {
		return u, false, nil
	}
	return u, err == nil, err
}

func (s *Server) authRoutes(handle func(string, handler)) {
	// Riders and drivers sign in the same way; a new number becomes a rider.
	// Drivers are onboarded by ops (D1).
	handle("POST /v1/auth/otp", func(w http.ResponseWriter, r *http.Request) error {
		phone, err := parsePhone(r)
		if err != nil {
			return err
		}
		if err := s.sendCode(r.Context(), phone, "rider"); err != nil {
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
		if err := s.checkCode(r.Context(), phone, "rider", code); err != nil {
			return err
		}
		u, ok, err := s.userByPhone(r.Context(), phone)
		if err != nil {
			return err
		}
		if !ok {
			err = s.DB.QueryRow(r.Context(), `with u as (insert into users (phone, role) values ($1, 'rider')
					on conflict (phone) do update set phone = excluded.phone returning id, role)
				insert into riders (user_id) select id from u on conflict do nothing returning user_id`, phone).Scan(&u.ID)
			if err != nil && !noRows(err) {
				return err
			}
			if u, _, err = s.userByPhone(r.Context(), phone); err != nil {
				return err
			}
		}
		token, err := s.newSession(r.Context(), u.ID, u.Role)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"token": token, "user": u})
		return nil
	})

	// The driver app (D1 auth.start / auth.verify): any number gets a code; a
	// new number gets a driver session with no profile yet (driver.register
	// makes one). A rider's number also signs in here, and driver.register
	// then refuses it with phone_in_use_as_rider.
	handle("POST /v1/driver/auth/otp", func(w http.ResponseWriter, r *http.Request) error {
		phone, err := parsePhone(r)
		if err != nil {
			return err
		}
		if err := s.sendCode(r.Context(), phone, "driver"); err != nil {
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
		if err := s.checkCode(r.Context(), phone, "driver", code); err != nil {
			var oe *offers.Error
			if errors.As(err, &oe) && oe.Code == "invalid_code" {
				oe.Code, oe.Message = "wrong_code", "That code didn't match"
			}
			return err
		}
		u, ok, err := s.userByPhone(r.Context(), phone)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := s.DB.Exec(r.Context(), `insert into users (phone, role) values ($1, 'driver') on conflict (phone) do nothing`, phone); err != nil {
				return err
			}
			if u, _, err = s.userByPhone(r.Context(), phone); err != nil {
				return err
			}
		}
		if u.Role == "admin" {
			return apiErr(403, "not_a_driver", "This number is an admin account.")
		}
		var exists bool
		if err := s.DB.QueryRow(r.Context(), "select exists (select 1 from drivers where user_id = $1)", u.ID).Scan(&exists); err != nil {
			return err
		}
		token, err := s.newSession(r.Context(), u.ID, "driver")
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"token": token, "user": u, "driver_exists": exists})
		return nil
	})

	handle("GET /v1/me", func(w http.ResponseWriter, r *http.Request) error {
		id, _, err := s.requireUser(r)
		if err != nil {
			return err
		}
		var u UserView
		err = s.DB.QueryRow(r.Context(), `select u.id, u.phone, u.role, coalesce(r.name, d.name, a.name, '') from users u
			left join riders r on r.user_id = u.id left join drivers d on d.user_id = u.id
			left join admins a on a.user_id = u.id where u.id = $1`, id).
			Scan(&u.ID, &u.Phone, &u.Role, &u.Name)
		if err != nil {
			return err
		}
		writeJSON(w, 200, u)
		return nil
	})

	// The app registers its FCM token after sign-in and whenever it changes.
	handle("PUT /v1/devices", func(w http.ResponseWriter, r *http.Request) error {
		id, _, err := s.requireUser(r)
		if err != nil {
			return err
		}
		var body struct {
			FCMToken string `json:"fcm_token"`
			Platform string `json:"platform"`
		}
		if err := decode(r, &body); err != nil {
			return err
		}
		if strings.TrimSpace(body.FCMToken) == "" {
			return apiErr(400, "invalid_request", "fcm_token is required")
		}
		if body.Platform == "" {
			body.Platform = "android"
		}
		_, err = s.DB.Exec(r.Context(), `insert into devices (fcm_token, user_id, platform, updated_at) values ($1, $2, $3, $4)
			on conflict (fcm_token) do update set user_id = excluded.user_id, platform = excluded.platform, updated_at = excluded.updated_at`,
			body.FCMToken, id, body.Platform, s.now())
		if err != nil {
			return err
		}
		w.WriteHeader(204)
		return nil
	})
}

// requireUser resolves `Authorization: Bearer <token>` to a user and role.
func (s *Server) requireUser(r *http.Request) (uuid.UUID, string, error) {
	token, ok := strings.CutPrefix(r.Header.Get("authorization"), "Bearer ")
	if !ok || token == "" {
		return uuid.Nil, "", apiErr(401, "unauthorized", "Sign in first")
	}
	var id uuid.UUID
	var role string
	err := s.DB.QueryRow(r.Context(), "select user_id, role from sessions where token_hash = $1 and expires_at > $2",
		sha256Hex(token), s.now()).Scan(&id, &role)
	if noRows(err) {
		return id, "", apiErr(401, "unauthorized", "Your session has ended. Sign in again.")
	}
	return id, role, err
}

func (s *Server) requireRider(r *http.Request) (uuid.UUID, error) {
	id, role, err := s.requireUser(r)
	if err != nil {
		return id, err
	}
	if role != "rider" {
		return id, apiErr(403, "riders_only", "This needs a rider account.")
	}
	return id, nil
}

func (s *Server) requireDriver(r *http.Request) (uuid.UUID, error) {
	id, role, err := s.requireUser(r)
	if err != nil {
		return id, err
	}
	if role != "driver" {
		return id, apiErr(403, "drivers_only", "This needs a driver account.")
	}
	return id, nil
}

func (s *Server) requireAdmin(r *http.Request) (uuid.UUID, error) {
	id, role, err := s.requireUser(r)
	if err != nil {
		return id, err
	}
	if role != "admin" {
		return id, apiErr(403, "admins_only", "This needs an admin account.")
	}
	return id, nil
}
