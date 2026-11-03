package main

// Small, allocation-free mailbox for touch sampled between display strips.
// Retain press/release order but coalesce adjacent hold updates. Cancellation
// removes queued holds so delayed rendering cannot approve a released finger.
type touchQueue struct {
	events [8]touchEvent
	n      int
}

func (q *touchQueue) clear() { q.n = 0 }
func (q *touchQueue) push(e touchEvent) bool {
	if !e.Activity {
		return true
	}
	switch e.Kind {
	case inputRelease, inputCancel, inputTap, inputSwipeLeft, inputSwipeRight, inputSwipeUp, inputSwipeDown:
		n := 0
		for i := 0; i < q.n; i++ {
			if q.events[i].Kind != inputHold {
				q.events[n] = q.events[i]
				n++
			}
		}
		q.n = n
	case inputHold:
		if q.n > 0 && q.events[q.n-1].Kind == inputHold {
			q.events[q.n-1] = e
			return true
		}
	}
	if q.n == len(q.events) {
		q.n = 1
		q.events[0] = touchEvent{Activity: true, inputEvent: inputEvent{Kind: inputCancel}}
		return false // Caller suppresses this gesture until a release.
	}
	q.events[q.n] = e
	q.n++
	return true
}
func (q *touchQueue) pop() touchEvent {
	if q.n == 0 {
		return touchEvent{}
	}
	e := q.events[0]
	q.n--
	copy(q.events[:q.n], q.events[1:q.n+1])
	return e
}
