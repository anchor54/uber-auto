package app

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"commute-backend/internal/domain"
)

// Flow D1, the admin's side (the POC's admin is the app's owner, using
// cmd/admin): review drivers' items, approve or reject them, and confirm or
// reject the payments they claim.

func (a *App) adminRoutes(handle func(string, handler)) {
	handle("GET /v1/admin/pending", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		if _, err := a.requireAdmin(r); err != nil {
			return err
		}
		type pendingDriver struct {
			ID          string  `json:"id"`
			Name        string  `json:"name"`
			Phone       string  `json:"phone"`
			City        string  `json:"city"`
			Status      string  `json:"status"`
			SubmittedAt *string `json:"submittedAt"`
		}
		rows, err := a.DB.Query(ctx, `select id, name, coalesce(phone, ''), city, status, submitted_at from drivers
			where status = 'submitted' order by submitted_at nulls first, created_at, id`)
		if err != nil {
			return err
		}
		drivers, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pendingDriver, error) {
			var d pendingDriver
			var at *time.Time
			err := row.Scan(&d.ID, &d.Name, &d.Phone, &d.City, &d.Status, &at)
			d.SubmittedAt = isoPtr(at)
			return d, err
		})
		if err != nil {
			return err
		}
		type pendingPayment struct {
			ID          string  `json:"id"`
			DriverID    string  `json:"driverId"`
			DriverName  string  `json:"driverName"`
			Reference   *string `json:"reference"`
			PeriodStart string  `json:"periodStart"`
			PeriodEnd   string  `json:"periodEnd"`
			Amount      int     `json:"amount"`
			ClaimedAt   string  `json:"claimedAt"`
		}
		rows, err = a.DB.Query(ctx, `select p.id, p.driver_id, d.name, d.reference, to_char(p.period_start, 'YYYY-MM-DD'),
			  to_char(p.period_end, 'YYYY-MM-DD'), p.amount, p.claimed_at
			from subscription_payments p join drivers d on d.id = p.driver_id
			where p.status = 'claimed' order by p.claimed_at, p.created_at`)
		if err != nil {
			return err
		}
		payments, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pendingPayment, error) {
			var p pendingPayment
			var at time.Time
			err := row.Scan(&p.ID, &p.DriverID, &p.DriverName, &p.Reference, &p.PeriodStart, &p.PeriodEnd, &p.Amount, &at)
			p.ClaimedAt = iso(at)
			return p, err
		})
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"drivers": orEmpty(drivers), "payments": orEmpty(payments)})
		return nil
	})

	handle("GET /v1/admin/drivers/{id}", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		if _, err := a.requireAdmin(r); err != nil {
			return err
		}
		readURL := func(path string) (string, error) {
			u, err := a.storage().SignRead(ctx, path, signedURLTTL)
			return absoluteURL(r, u), err
		}
		d, err := a.loadDriverState(ctx, a.DB, r.PathValue("id"), readURL)
		if noRows(err) {
			return driverNotFound()
		}
		if err != nil {
			return err
		}
		writeJSON(w, 200, d)
		return nil
	})

	handle("POST /v1/admin/drivers/{id}/items/{item}/review", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		admin, err := a.requireAdmin(r)
		if err != nil {
			return err
		}
		id, item := r.PathValue("id"), r.PathValue("item")
		var body struct{ Decision, Reason *string }
		if err := decode(r, &body); err != nil {
			return err
		}
		reason := trimmed(body.Reason)
		var c domain.Check
		c.If(!slices.Contains(domain.ReviewableItems, item), "Expected one of "+strings.Join(domain.ReviewableItems, ", "), "item")
		c.If(body.Decision == nil || (*body.Decision != domain.ItemVerified && *body.Decision != domain.ItemRejected),
			"Expected verified or rejected", "decision")
		rejected := body.Decision != nil && *body.Decision == domain.ItemRejected
		c.If(rejected && reason == "", "Give the driver a reason", "reason")
		if err := c.Err(); err != nil {
			return err
		}
		var reasonArg *string
		if rejected {
			reasonArg = &reason
		}
		var out driverState
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			d, err := lockDriver(ctx, tx, id)
			if noRows(err) {
				return driverNotFound()
			}
			if err != nil {
				return err
			}
			if d.Status != domain.DriverSubmitted {
				return a.driverConflict(ctx, tx, id, "wrong_status", "Items can be reviewed only while the driver is submitted.")
			}
			sql := `update driver_items set state = $3, reject_reason = $4, reviewed_at = $5, updated_at = $5 where driver_id = $1 and kind = $2`
			if item == domain.VehicleItem {
				sql = `update vehicles set review_state = $3, reject_reason = $4, reviewed_at = $5, updated_at = $5 where driver_id = $1 and $2 = 'vehicle'`
			}
			tag, err := tx.Exec(ctx, sql, id, item, *body.Decision, reasonArg, a.now())
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return apiErr(404, "not_found", "The driver hasn't added "+domain.ItemLabel(item)+".")
			}
			if err := a.bumpDriver(ctx, tx, d); err != nil {
				return err
			}
			d.Version++
			out, err = a.loadDriverState(ctx, tx, id, nil)
			if err != nil {
				return err
			}
			// The review is over when nothing is left to look at; any
			// rejection then sends the driver back to fix those items.
			var waiting, labels []string
			for _, k := range domain.ReviewableItems {
				switch out.itemState(k) {
				case domain.ItemUploaded:
					waiting = append(waiting, k)
				case domain.ItemRejected:
					labels = append(labels, domain.ItemLabel(k))
				}
			}
			if len(waiting) > 0 || len(labels) == 0 {
				return nil
			}
			if err := a.setDriverStatus(ctx, tx, d, domain.DriverChangesNeeded, "changes_needed", "admin", admin,
				map[string]any{"items": labels}, ""); err != nil {
				return err
			}
			if err := queueNotification(ctx, tx, notification{To: Recipient{"driver", id}, Kind: "items_rejected", SendAfter: a.now(),
				Payload: map[string]any{"items": strings.Join(labels, ", ")}}); err != nil {
				return err
			}
			out, err = a.loadDriverState(ctx, tx, id, nil)
			return err
		})
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"item": out.item(item), "driver": out})
		return nil
	})

	handle("POST /v1/admin/drivers/{id}/approve", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		admin, err := a.requireAdmin(r)
		if err != nil {
			return err
		}
		id := r.PathValue("id")
		var out driverState
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			d, err := lockDriver(ctx, tx, id)
			if noRows(err) {
				return driverNotFound()
			}
			if err != nil {
				return err
			}
			if d.Status != domain.DriverSubmitted {
				return a.driverConflict(ctx, tx, id, "wrong_status", "Only a submitted driver can be approved.")
			}
			now, err := a.loadDriverState(ctx, tx, id, nil)
			if err != nil {
				return err
			}
			notVerified := []string{}
			for _, k := range domain.ReviewableItems {
				if now.itemState(k) != domain.ItemVerified {
					notVerified = append(notVerified, k)
				}
			}
			if len(notVerified) > 0 {
				e := domain.NewApiError(409, "items_not_verified", "Verify every item first.").WithState(now)
				e.Items = notVerified
				return e
			}
			if err := a.setDriverStatus(ctx, tx, d, domain.DriverApproved, "approved", "admin", admin, nil, "reject_reason = null"); err != nil {
				return err
			}
			if err := queueNotification(ctx, tx, notification{To: Recipient{"driver", id}, Kind: "driver_approved", SendAfter: a.now()}); err != nil {
				return err
			}
			out, err = a.loadDriverState(ctx, tx, id, nil)
			return err
		})
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	})

	handle("POST /v1/admin/drivers/{id}/reject", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		admin, err := a.requireAdmin(r)
		if err != nil {
			return err
		}
		id := r.PathValue("id")
		reason, err := requiredReason(r)
		if err != nil {
			return err
		}
		var out driverState
		err = a.inTx(ctx, func(tx pgx.Tx) error {
			d, err := lockDriver(ctx, tx, id)
			if noRows(err) {
				return driverNotFound()
			}
			if err != nil {
				return err
			}
			if d.Status != domain.DriverSubmitted {
				return a.driverConflict(ctx, tx, id, "wrong_status", "Only a submitted driver can be rejected.")
			}
			if err := a.setDriverStatus(ctx, tx, d, domain.DriverRejected, "rejected", "admin", admin,
				map[string]any{"reason": reason}, "reject_reason = $4", reason); err != nil {
				return err
			}
			if err := queueNotification(ctx, tx, notification{To: Recipient{"driver", id}, Kind: "driver_rejected", SendAfter: a.now()}); err != nil {
				return err
			}
			out, err = a.loadDriverState(ctx, tx, id, nil)
			return err
		})
		if err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	})

	handle("POST /v1/admin/payments/{id}/confirm", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		admin, err := a.requireAdmin(r)
		if err != nil {
			return err
		}
		return a.reviewPayment(ctx, w, r.PathValue("id"), admin, "confirmed", "")
	})

	handle("POST /v1/admin/payments/{id}/reject", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()
		admin, err := a.requireAdmin(r)
		if err != nil {
			return err
		}
		reason, err := requiredReason(r)
		if err != nil {
			return err
		}
		return a.reviewPayment(ctx, w, r.PathValue("id"), admin, "rejected", reason)
	})
}

func driverNotFound() error { return apiErr(404, "not_found", "No such driver") }

func paymentNotFound() error { return apiErr(404, "not_found", "No such payment") }

func requiredReason(r *http.Request) (string, error) {
	var body struct{ Reason *string }
	if err := decode(r, &body); err != nil {
		return "", err
	}
	reason := trimmed(body.Reason)
	var c domain.Check
	c.If(reason == "", "Give a reason", "reason")
	return reason, c.Err()
}

// reviewPayment confirms or rejects a claimed payment in one transaction.
// The update only matches a payment that is still claimed, so of two
// admins confirming at once exactly one changes anything; the other gets
// 409 wrong_status and nothing is written or sent.
func (a *App) reviewPayment(ctx context.Context, w http.ResponseWriter, paymentID, admin, to, reason string) error {
	if !uuidRe.MatchString(paymentID) {
		return paymentNotFound()
	}
	var payment subscriptionPaymentJSON
	var out driverState
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		var reasonArg *string
		if to == "rejected" {
			reasonArg = &reason
		}
		var driverID string
		var claimedAt time.Time
		p := &payment
		err := tx.QueryRow(ctx, `update subscription_payments set status = $2, reviewed_at = $3, reviewed_by = $4, reject_reason = $5
			where id = $1 and status = 'claimed'
			returning driver_id, `+paymentColumns, paymentID, to, a.now(), admin, reasonArg).
			Scan(&driverID, &p.ID, &p.PeriodStart, &p.PeriodEnd, &p.Amount, &p.Status, &claimedAt, &p.RejectReason)
		p.ClaimedAt = iso(claimedAt)
		if noRows(err) {
			current, _, err := loadPayment(ctx, tx, paymentID)
			if noRows(err) {
				return paymentNotFound()
			}
			if err != nil {
				return err
			}
			return domain.NewApiError(409, "wrong_status", "This payment is already "+current.Status+".").WithState(current)
		}
		if err != nil {
			return err
		}
		d, err := lockDriver(ctx, tx, driverID)
		if err != nil {
			return err
		}
		data := map[string]any{"paymentId": payment.ID, "amount": payment.Amount, "periodStart": payment.PeriodStart,
			"periodEnd": payment.PeriodEnd, "paymentStatus": to}
		if to == "rejected" {
			data["reason"] = reason
			if _, err := tx.Exec(ctx, "update drivers set version = version + 1 where id = $1", driverID); err != nil {
				return err
			}
			if err := a.driverEvent(ctx, tx, driverID, "payment_rejected", &d.Status, &d.Status, "admin", admin, data); err != nil {
				return err
			}
			if err := queueNotification(ctx, tx, notification{To: Recipient{"driver", driverID}, Kind: "payment_rejected",
				SendAfter: a.now(), Payload: map[string]any{"reason": reason}}); err != nil {
				return err
			}
		} else {
			var newStatus string
			if err := tx.QueryRow(ctx, `update drivers set
				  subscription_until = greatest(coalesce(subscription_until, $2::text::date), $2::text::date),
				  status = case when status in ('approved', 'paused') then 'live' else status end,
				  version = version + 1
				where id = $1 returning status`, driverID, payment.PeriodEnd).Scan(&newStatus); err != nil {
				return err
			}
			if err := a.driverEvent(ctx, tx, driverID, "payment_confirmed", &d.Status, &newStatus, "admin", admin, data); err != nil {
				return err
			}
			if newStatus == domain.DriverLive {
				vehicleType, _, _ := strings.Cut(d.RideType, "_")
				if err := queueNotification(ctx, tx, notification{To: Recipient{"driver", driverID}, Kind: "driver_live",
					SendAfter: a.now(), Payload: map[string]any{"vehicleType": vehicleType}}); err != nil {
					return err
				}
			}
		}
		out, err = a.loadDriverState(ctx, tx, driverID, nil)
		return err
	})
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"payment": payment, "driver": out})
	return nil
}
