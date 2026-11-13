package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MSG91 sends the backend's own sign-in code by SMS through MSG91's OTP API
// (control.msg91.com/api/v5/otp), using a DLT-approved OTP template. The
// backend still makes, hashes and checks the code; MSG91 only delivers it.
//
// Needs MSG91_AUTH_KEY and MSG91_OTP_TEMPLATE_ID (the template's text must
// contain MSG91's ##OTP## placeholder).
type MSG91 struct {
	AuthKey    string
	TemplateID string
	// Client defaults to one with a 10-second timeout.
	Client *http.Client
	Log    *slog.Logger
}

func (m *MSG91) Send(ctx context.Context, phone, code string) error {
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	q := url.Values{}
	q.Set("template_id", m.TemplateID)
	q.Set("mobile", "91"+phone)
	q.Set("otp", code)
	q.Set("otp_expiry", "5")
	var body struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://control.msg91.com/api/v5/otp?"+q.Encode(), strings.NewReader("{}"))
	if err == nil {
		req.Header.Set("authkey", m.AuthKey)
		req.Header.Set("content-type", "application/json")
		var res *http.Response
		if res, err = client.Do(req); err == nil {
			raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
			res.Body.Close()
			_ = json.Unmarshal(raw, &body)
			if res.StatusCode < 300 && body.Type == "success" {
				return nil
			}
		}
	}
	reason := body.Message
	if reason == "" {
		reason = "no response"
	}
	log := m.Log
	if log == nil {
		log = slog.Default()
	}
	log.Error("[sms] MSG91 did not send", "to", "…"+phone[len(phone)-4:], "reason", reason)
	return apiErr(502, "sms_failed", "We couldn't send the code. Try again in a minute.")
}
