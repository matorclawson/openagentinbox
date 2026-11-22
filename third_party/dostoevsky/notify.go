package dostoevsky

import "sync"

// Notifications broadcasts hints after commits. Subscribe before checking the
// durable predicate. Release after the check/wait, then resubscribe and recheck.
type Notifications[K comparable] struct {
	mu             sync.Mutex
	listenersByKey map[K]*listeners
	count, limit   int
}
type listeners struct {
	ch   chan struct{}
	refs int
}

func NewNotifications[K comparable](limit int) *Notifications[K] {
	return &Notifications[K]{listenersByKey: make(map[K]*listeners), limit: limit}
}
func (n *Notifications[K]) Subscribe(key K) (<-chan struct{}, func(), error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.count >= n.limit {
		return nil, nil, ErrLimit
	}
	s := n.listenersByKey[key]
	if s == nil {
		s = &listeners{}
		n.listenersByKey[key] = s
	}
	if s.ch == nil {
		s.ch = make(chan struct{})
	}
	s.refs++
	n.count++
	release := sync.OnceFunc(func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		s.refs--
		n.count--
		if s.refs == 0 {
			delete(n.listenersByKey, key)
		}
	})
	return s.ch, release, nil
}
func (n *Notifications[K]) Signal(key K) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if s := n.listenersByKey[key]; s != nil && s.ch != nil {
		close(s.ch)
		s.ch = nil
	}
}
