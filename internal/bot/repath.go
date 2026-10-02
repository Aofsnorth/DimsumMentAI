package bot

import "github.com/go-gl/mathgl/mgl32"

// Repath admission.
//
// Re-planning is an A* search measured in hundreds of milliseconds, and it used
// to have no admission control at all. A caller that asked for a route (the
// gatherer walking to a tree) and the movement tick asking whether it has one
// both started a search for the same destination within a few tens of
// milliseconds of each other, and the world paid for two searches to get one
// route.
//
// The guard is per destination rather than a blanket "is one running". A blanket
// guard would drop a legitimate request: if the gatherer changed its mind about
// the tree while a search for the old one was still running, skipping the new
// request would leave the in-flight search to publish a route to the old tree,
// and nothing would correct it because the bot would then have a path and no
// reason to ask again.
//
// The caller must hold b.Mu.
func (b *Bot) ClaimRepathLocked(target mgl32.Vec3) bool {
	if b.RepathInFlight && b.RepathTarget == target {
		return false
	}
	b.RepathInFlight = true
	b.RepathTarget = target
	return true
}

// ReleaseRepath marks the in-flight search as finished. It must run even when
// the search failed, or the bot would refuse to plan again for the rest of the
// session.
func (b *Bot) ReleaseRepath() {
	b.Mu.Lock()
	b.RepathInFlight = false
	b.Mu.Unlock()
}
