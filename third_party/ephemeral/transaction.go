package ephemeral

type rows [][]string

func (s *Store) tx(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.query("BEGIN IMMEDIATE"); e != nil {
		return e
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = s.query("ROLLBACK")
		}
	}()
	if e := fn(); e != nil {
		return e
	}
	_, e := s.query("COMMIT")
	committed = e == nil
	return e
}
