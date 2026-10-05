package main

// fixtures.go carries the part a real deployment gets for free: the
// process death between signal and recovery. Everything runs on memory
// stores, offline.

// crash simulates the pod dying right after the approval signal was
// consumed: every in-memory runtime fact is lost, only the stores
// survive. The recovered run must rebuild its position from the
// checkpoint payload, not from live memory.
func (t *Trip) crash() {
	t.rt = &travelRuntime{
		sessionID: t.rt.sessionID,
		journal:   t.rt.journal,
		flags:     t.rt.flags,
		save:      t.cps.Put,
	}
}
