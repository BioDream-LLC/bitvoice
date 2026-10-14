package pbx

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/audiosocket"
	"github.com/biodream-llc/bitvoice/internal/media"
)

// runAudioStream hands an answered caller to a voice bot over AudioSocket. The bot hanging up ends the
// call; a bot that cannot be reached, or breaks off, sends the caller to the stream's fallback.
func (c *Call) runAudioStream(id string) {
	cfg := c.e.Config()
	s := cfg.FindAudioStream(id)
	if s == nil {
		c.hangup("audio stream removed", "system")
		return
	}
	ep, err := c.e.NewMediaEndpoint()
	if err != nil {
		c.e.Log.Warn("audio stream: no media ports free", "stream", s.Name)
		c.goTo(cfg, s.Fallback)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	pipe, err := audiosocket.Dial(ctx, s.Address, c.ID, ep)
	cancel()
	if err != nil {
		ep.Close()
		c.e.Log.Warn("audio stream: the bot could not be reached; the caller goes to the fallback", "stream", s.Name, "address", s.Address, "err", err.Error())
		c.e.publish("stream.failed", map[string]any{"stream": s.Name, "call": c.ID, "reason": err.Error()})
		c.goTo(cfg, s.Fallback)
		return
	}
	c.mu.Lock()
	a := c.caller
	c.answeredBy = s.Name
	c.mu.Unlock()
	media.Connect(a.ep, ep)
	ep.Start()
	c.e.publish("call.updated", c.info())
	select {
	case <-pipe.Done():
	case <-c.cancelled:
		pipe.Close()
	}
	media.Connect(a.ep, nil)
	ep.Close()
	switch how, why := pipe.Ended(); how {
	case audiosocket.ByBot:
		c.hangup("the bot hung up", "system")
	case audiosocket.Failed:
		c.e.Log.Warn("audio stream: the bot broke off; the caller goes to the fallback", "stream", s.Name, "why", why)
		c.e.publish("stream.failed", map[string]any{"stream": s.Name, "call": c.ID, "reason": why})
		c.goTo(c.e.Config(), s.Fallback)
	}
}

// AttachMedia joins a media endpoint to a call between two parties through a mixing bridge, as a
// supervisor joins one: listen (it hears both, nobody hears it) or barge (everyone hears it). For ARI's
// external media. detach takes it out and joins the two parties directly again.
func (e *Engine) AttachMedia(id string, ep *media.Endpoint, mode string) (detach func(), err error) {
	if mode != "listen" && mode != "barge" {
		return nil, errors.New("choose listen or barge")
	}
	e.mu.Lock()
	c := e.calls[id]
	e.mu.Unlock()
	if c == nil {
		return nil, errors.New("no such call")
	}
	c.mu.Lock()
	a, b := c.caller, c.callee
	if a == nil || b == nil || c.ended {
		c.mu.Unlock()
		return nil, errors.New("only a call between two parties, once answered, takes external media")
	}
	if c.bridge != nil {
		c.mu.Unlock()
		return nil, errors.New("this call already has someone or something joined to it")
	}
	br := media.NewBridge(hearing(mode, "callee"))
	c.bridge, c.supervision, c.supAgent = br, mode, "callee"
	c.mu.Unlock()
	br.Add("caller", a.ep)
	br.Add("callee", b.ep)
	br.Add("sup", ep)
	once := sync.OnceFunc(func() { c.endSupervision(br, nil) })
	c.mu.Lock()
	c.onEnd = append(c.onEnd, once)
	c.mu.Unlock()
	e.publish("call.updated", c.info())
	return once, nil
}
