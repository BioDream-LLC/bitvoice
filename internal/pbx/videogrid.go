package pbx

import (
	"context"
	"slices"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// Grid rooms: everyone sees everyone. ffmpeg decodes each member's picture, puts them in a grid (up
// to 3 by 3, in the order they joined) and encodes the grid once; every member gets that one stream.
// A member counts while its picture keeps coming: one who joins without video, or whose camera stops,
// leaves the grid (so a still camera never freezes it), and the grid is made again when that changes.

const (
	gridActive = 2 * time.Second // a member whose picture stopped this long ago leaves the grid
	gridSettle = time.Second     // a change waits this long, so the grid is not remade at every blip
	gridStall  = 4 * time.Second // no grid packet for this long (after its start-up) remakes it
)

// gridWarmup is ffmpeg's start-up for n pictures (it waits for a full picture of each): pictures are
// asked for until then, and no stall is judged before.
func gridWarmup(n int) time.Duration { return 4*time.Second + time.Duration(n)*500*time.Millisecond }

// gridTick decides each tick whether the grid needs making again.
func (v *videoSwitch) gridTick() {
	now := time.Now()
	v.mu.Lock()
	var act []*vmember
	for _, x := range v.members {
		if !x.lastVid.IsZero() && now.Sub(x.lastVid) < gridActive {
			act = append(act, x)
		}
	}
	slices.SortFunc(act, func(a, b *vmember) int { return a.joined - b.joined })
	if len(act) > media.MaxGridTiles {
		act = act[:media.MaxGridTiles]
	}
	want := make([]string, len(act))
	for i, x := range act {
		want[i] = x.id
	}
	if !slices.Equal(want, v.gridWant) {
		v.gridWant, v.gridWantSince = want, now
	}
	changed := !slices.Equal(want, v.gridIDs) && (v.grid == nil || now.Sub(v.gridWantSince) >= gridSettle)
	stalled := v.grid != nil && now.Sub(v.gridAt) > gridWarmup(len(v.gridIDs)) && now.Sub(lastOr(v.grid.LastOut(), v.gridAt)) > gridStall
	var ask []string
	if now.Sub(v.gridAt) < gridWarmup(len(v.gridIDs)) && v.grid != nil && v.grid.LastOut().IsZero() {
		ask = slices.Clone(v.gridIDs) // ffmpeg needs a full picture of each to start
	}
	v.mu.Unlock()
	if changed || stalled {
		v.remakeGrid(want)
		return
	}
	for _, id := range ask {
		v.askPicture(id)
	}
}

func lastOr(t, d time.Time) time.Time {
	if t.IsZero() {
		return d
	}
	return t
}

// remakeGrid starts a grid of these members (none: no grid), stopping the one before.
func (v *videoSwitch) remakeGrid(ids []string) {
	v.mu.Lock()
	old := v.grid
	v.grid = nil
	v.gridGen++
	gen := v.gridGen
	v.gridIDs, v.gridAt = slices.Clone(ids), time.Now()
	for _, x := range v.members {
		x.tile = -1
		x.switched = true // a new stream: its numbering carries on from the last
	}
	v.mu.Unlock()
	if old != nil {
		old.Close()
	}
	if len(ids) == 0 {
		return
	}
	g, err := media.StartVideoMixer(context.Background(), v.ffmpeg, media.VideoCodec{Name: v.codec}, len(ids), func(pkt []byte) {
		v.mu.Lock()
		cur := v.gridGen == gen
		v.mu.Unlock()
		if cur {
			v.forward("grid", nil, pkt)
		}
	})
	if err != nil {
		if v.log != nil {
			v.log("the video grid could not start", "room", v.room.number, "err", err)
		}
		return
	}
	v.mu.Lock()
	if v.gridGen != gen {
		v.mu.Unlock()
		g.Close()
		return
	}
	select {
	case <-v.stop:
		v.mu.Unlock()
		g.Close()
		return
	default:
	}
	v.grid = g
	for i, id := range ids {
		if x := v.members[id]; x != nil {
			x.tile = i
		}
	}
	v.mu.Unlock()
	for _, id := range ids {
		v.askPicture(id)
	}
}

// gridShows names the members in the grid, in their places.
func (v *videoSwitch) gridShows() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.grid == nil {
		return nil
	}
	return slices.Clone(v.gridIDs)
}
