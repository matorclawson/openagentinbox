package deliverystore

import (
	"errors"
	"sync"

	dost "github.com/rodriveracom/dostoevsky"
)

const (
	MaxSeatWaiters   = 8
	MaxStoreWaiters  = 512
	maxNativeWaiters = 64
)

type watchHub struct {
	mu            sync.Mutex
	seats         map[string]int
	native        int
	notifications *dost.Notifications[string]
}

// Subscription owns a Messenger admission slot across successive generations.
type Subscription struct {
	hub     *watchHub
	seat    string
	native  bool
	ch      <-chan struct{}
	release func()
}

// Changes returns the current snapshot. The owning reader calls Rearm after observing a close.
func (w *Subscription) Changes() <-chan struct{} { return w.ch }

func (w *Subscription) Rearm() error {
	h := w.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if w.release == nil {
		return errors.New("watch closed")
	}
	w.release()
	ch, release, err := h.notifications.Subscribe(w.seat)
	if err != nil {
		return err
	}
	w.ch, w.release = ch, release
	return nil
}

func (w *Subscription) Close() {
	h := w.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	if w.release == nil {
		return
	}
	w.release()
	w.release = nil
	h.seats[w.seat]--
	if h.seats[w.seat] == 0 {
		delete(h.seats, w.seat)
	}
	if w.native {
		h.native--
	}
}

func (s *Store) WatchSeat(seat string, native bool) (*Subscription, error) {
	h := &s.watch
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seats[seat] >= MaxSeatWaiters || native && h.native >= maxNativeWaiters {
		return nil, errors.New("watcher limit reached")
	}
	ch, release, err := h.notifications.Subscribe(seat)
	if err != nil {
		return nil, err
	}
	h.seats[seat]++
	if native {
		h.native++
	}
	return &Subscription{hub: h, seat: seat, native: native, ch: ch, release: release}, nil
}

func newWatchHub() watchHub {
	return watchHub{seats: make(map[string]int), notifications: dost.NewNotifications[string](MaxStoreWaiters)}
}

func (s *Store) signal(seats []string) {
	for _, seat := range seats {
		s.watch.notifications.Signal(seat)
	}
}

// SignalProbe wakes the notification channel reserved for a paired host.
func (s *Store) SignalProbe(host string) { s.signal([]string{"probe/" + host}) }
