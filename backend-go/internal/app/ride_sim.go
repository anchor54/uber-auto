package app

import (
	"context"
	"math"
	"sync"
	"time"

	"commute-backend/internal/domain"
)

// Dev-only driver for R2 rides. Every move goes through the same service
// functions the driver endpoints call (ApplyMove, RecordLocation, …), so a
// simulated ride obeys the same rules a real one does.

var SimMoves = []string{"depart", "arrive", "start", "end", "no_show", "contact"}

func (a *App) simAt(ctx context.Context, driverID string, lat, lng float64) error {
	return a.RecordLocation(ctx, driverID, []LocationPoint{Point(lat, lng, a.now())})
}

// SimStep makes one move, with whatever a real driver would have done first
// (go on duty, drive to the pickup, …).
func (a *App) SimStep(ctx context.Context, rideID, move string) (*RideRow, error) {
	r, err := a.mustLoadRide(ctx, rideID)
	if err != nil {
		return nil, err
	}
	driver := Actor{"driver", r.DriverID}
	switch move {
	case "depart":
		if err := a.SetDuty(ctx, r.DriverID, true); err != nil {
			return nil, err
		}
		return a.ApplyMove(ctx, r.ID, "depart", driver, MoveInput{})
	case "arrive":
		if err := a.simAt(ctx, r.DriverID, r.Pickup.Lat, r.Pickup.Lng); err != nil {
			return nil, err
		}
		return a.ApplyMove(ctx, r.ID, "arrive", driver, MoveInput{})
	case "start":
		return a.ApplyMove(ctx, r.ID, "start", driver, MoveInput{Pin: r.Pin})
	case "end":
		if err := a.simAt(ctx, r.DriverID, r.DropOff.Lat, r.DropOff.Lng); err != nil {
			return nil, err
		}
		return a.ApplyMove(ctx, r.ID, "end", driver, MoveInput{Lat: &r.DropOff.Lat, Lng: &r.DropOff.Lng})
	case "contact":
		if err := a.LogContact(ctx, r.ID, r.DriverID, "call"); err != nil {
			return nil, err
		}
		return a.mustLoadRide(ctx, r.ID)
	case "no_show":
		if err := a.LogContact(ctx, r.ID, r.DriverID, "call"); err != nil {
			return nil, err
		}
		return a.ApplyMove(ctx, r.ID, "no_show", driver, MoveInput{})
	}
	return nil, apiErr(400, "invalid_request", "Unknown move "+move)
}

var (
	playingMu sync.Mutex
	playing   = map[string]bool{}
)

type PlayOptions struct {
	// 1 = real time. 60 plays a 6-minute approach in 6 seconds.
	Speed float64
	// Minutes the driver takes to reach the pickup (default 6).
	ApproachMinutes float64
	// Minutes the driver waits at the pickup before the PIN start (default 1).
	WaitMinutes float64
	// Sleep defaults to time.Sleep; tests move their clock instead.
	Sleep func(time.Duration)
}

// SimPlay plays a whole ride: depart from about 2 km out, drive to the
// pickup sending a position every 5 simulated seconds, arrive, start with
// the real PIN, drive to the drop-off, end. It runs until the ride is done;
// call it on its own goroutine to play in the background.
func (a *App) SimPlay(ctx context.Context, rideID string, o PlayOptions) error {
	playingMu.Lock()
	if playing[rideID] {
		playingMu.Unlock()
		return apiErr(409, "already_playing", "This ride is already being played")
	}
	playing[rideID] = true
	playingMu.Unlock()
	defer func() {
		playingMu.Lock()
		delete(playing, rideID)
		playingMu.Unlock()
	}()
	return a.playRide(ctx, rideID, o)
}

func (a *App) playRide(ctx context.Context, rideID string, o PlayOptions) error {
	sleep := o.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	if o.ApproachMinutes == 0 {
		o.ApproachMinutes = 6
	}
	if o.WaitMinutes == 0 {
		o.WaitMinutes = 1
	}
	tick := time.Duration(float64(5*time.Second) / o.Speed) // a position every 5 simulated seconds
	r, err := a.mustLoadRide(ctx, rideID)
	if err != nil {
		return err
	}
	start := domain.LatLng{Lat: r.Pickup.Lat + 0.018, Lng: r.Pickup.Lng} // ~2 km north
	if err := a.simAt(ctx, r.DriverID, start.Lat, start.Lng); err != nil {
		return err
	}
	if _, err := a.SimStep(ctx, rideID, "depart"); err != nil {
		return err
	}

	// drive moves along [path] over [minutes].
	drive := func(path []domain.LatLng, minutes float64) error {
		point := domain.AlongPath(path)
		steps := max(2, int(math.Round(minutes*60/5)))
		for i := 1; i <= steps; i++ {
			sleep(tick)
			p := point(float64(i) / float64(steps))
			if err := a.simAt(ctx, r.DriverID, p.Lat, p.Lng); err != nil {
				return err
			}
		}
		return nil
	}

	// The same road route the rider's map shows (asked for at departure), so
	// the marker and the line agree; a straight line in the demo city.
	departed, err := a.mustLoadRide(ctx, rideID)
	if err != nil {
		return err
	}
	if err := a.refreshApproachRoute(ctx, departed, start, true); err != nil {
		return err
	}
	if departed, err = a.mustLoadRide(ctx, rideID); err != nil {
		return err
	}
	if err := drive(RouteOf(departed.ApproachRoute, start, r.Pickup.Point()), o.ApproachMinutes); err != nil {
		return err
	}
	if _, err := a.SimStep(ctx, rideID, "arrive"); err != nil {
		return err
	}
	sleep(time.Duration(o.WaitMinutes * float64(time.Minute) / o.Speed))
	if _, err := a.SimStep(ctx, rideID, "start"); err != nil {
		return err
	}
	if err := drive(RouteOf(r.Route, r.Pickup.Point(), r.DropOff.Point()), float64(r.TripMinutes)); err != nil {
		return err
	}
	_, err = a.SimStep(ctx, rideID, "end")
	return err
}

// RouteOf is the ride's stored road route, or the straight line when it has
// none (or a damaged one).
func RouteOf(encoded *string, from, to domain.LatLng) []domain.LatLng {
	if encoded != nil {
		if path, err := domain.DecodePolyline(*encoded); err == nil && len(path) >= 2 {
			return path
		}
	}
	return []domain.LatLng{from, to}
}
