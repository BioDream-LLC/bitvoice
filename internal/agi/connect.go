package agi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Port is FastAGI's port when an agi:// URL names none.
const Port = 4573

// Target is where a script runs: a FastAGI server (agi://host[:port]/script?args, or hagi://domain/
// script, whose servers come from the domain's _agi._tcp SRV records), or a program on this server.
type Target struct {
	URL     string
	HA      bool   // hagi://: the servers come from SRV records
	Host    string // agi://: host:port
	Script  string // the path and query sent as agi_network_script (no leading /)
	Program string // a program's file name in the AGI programs folder
}

var programRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$`)

// ParseTarget reads an AGI address: agi://..., hagi://..., or a program's file name.
func ParseTarget(v string) (Target, error) {
	v = strings.TrimSpace(v)
	if !strings.Contains(v, "://") {
		if !programRe.MatchString(v) {
			return Target{}, errors.New("give agi://host:port/script, hagi://domain/script, or a program's file name in the AGI programs folder")
		}
		return Target{URL: v, Program: v}, nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return Target{}, errors.New("not an address: " + err.Error())
	}
	t := Target{URL: v, Script: strings.TrimPrefix(u.Path, "/")}
	if u.RawQuery != "" {
		t.Script += "?" + u.RawQuery
	}
	switch strings.ToLower(u.Scheme) {
	case "agi":
		if u.Hostname() == "" {
			return Target{}, errors.New("agi:// needs a host")
		}
		port := u.Port()
		if port == "" {
			port = strconv.Itoa(Port)
		}
		t.Host = net.JoinHostPort(u.Hostname(), port)
	case "hagi":
		if u.Hostname() == "" || u.Port() != "" {
			return Target{}, errors.New("hagi:// takes a domain name, without a port (its SRV records give the servers)")
		}
		t.HA, t.Host = true, u.Hostname()
	default:
		return Target{}, errors.New("an AGI address starts agi:// or hagi://")
	}
	if u.User != nil {
		return Target{}, errors.New("FastAGI has no sign-in: leave the user name out of the address")
	}
	return t, nil
}

// Dial connects to a FastAGI server: with hagi://, each server the SRV records list, in their order,
// until one answers. Each try waits up to timeout.
func Dial(ctx context.Context, t Target, timeout time.Duration) (net.Conn, error) {
	addrs := []string{t.Host}
	if t.HA {
		_, srvs, err := net.DefaultResolver.LookupSRV(ctx, "agi", "tcp", t.Host)
		if err != nil || len(srvs) == 0 {
			return nil, fmt.Errorf("no _agi._tcp SRV records for %s", t.Host)
		}
		sort.SliceStable(srvs, func(i, j int) bool { return srvs[i].Priority < srvs[j].Priority })
		addrs = addrs[:0]
		for _, s := range srvs {
			addrs = append(addrs, net.JoinHostPort(strings.TrimSuffix(s.Target, "."), strconv.Itoa(int(s.Port))))
		}
	}
	var last error
	for _, a := range addrs {
		d := net.Dialer{Timeout: timeout}
		c, err := d.DialContext(ctx, "tcp", a)
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

// Program is an AGI program running on this server for one call: its standard input and output
// carry the protocol, standard error goes to the log, and with EAGI the caller's audio (8 kHz
// signed 16-bit, little-endian) is written to its file descriptor 3.
type Program struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	out   io.ReadCloser
	audio *os.File
	feed  chan []byte
	done  chan struct{}
	once  sync.Once
	mu    sync.Mutex // guards feed against Stop closing it
	stop  bool
}

// ErrNoProgram is a program that is not in the folder, or cannot be run.
var ErrNoProgram = errors.New("agi: no such program in the AGI programs folder")

// StartProgram starts dir/name with the script's arguments.
func StartProgram(dir, name string, args []string, eagi bool, logf func(string)) (*Program, error) {
	if !programRe.MatchString(name) {
		return nil, ErrNoProgram
	}
	path := filepath.Join(dir, name)
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, ErrNoProgram
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		return nil, fmt.Errorf("agi: %s is not executable (chmod +x)", name)
	}
	if eagi && runtime.GOOS == "windows" {
		return nil, errors.New("agi: EAGI (the caller's audio on descriptor 3) needs Linux or macOS")
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "AST_AGI_DIR=" + dir}
	if r := os.Getenv("SYSTEMROOT"); r != "" {
		cmd.Env = append(cmd.Env, "SYSTEMROOT="+r)
	}
	p := &Program{cmd: cmd, done: make(chan struct{})}
	if p.in, err = cmd.StdinPipe(); err != nil {
		return nil, err
	}
	if p.out, err = cmd.StdoutPipe(); err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	var audioRead *os.File
	if eagi {
		if audioRead, p.audio, err = os.Pipe(); err != nil {
			return nil, err
		}
		cmd.ExtraFiles = []*os.File{audioRead}
	}
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		if audioRead != nil {
			audioRead.Close()
			p.audio.Close()
		}
		return nil, fmt.Errorf("agi: %s did not start: %w", name, err)
	}
	if audioRead != nil {
		audioRead.Close()
		p.feed = make(chan []byte, 50) // a second of audio; a program that reads none loses the rest
		go func() {
			for b := range p.feed {
				if _, err := p.audio.Write(b); err != nil {
					break
				}
			}
			p.audio.Close()
			for range p.feed {
			}
		}()
	}
	go func() {
		buf := make([]byte, 4096)
		var line []byte
		for {
			n, err := stderr.Read(buf)
			for _, c := range buf[:n] {
				if c == '\n' || len(line) > 1000 {
					logf(string(line))
					line = line[:0]
					continue
				}
				line = append(line, c)
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// Read reads the program's commands.
func (p *Program) Read(b []byte) (int, error) { return p.out.Read(b) }

// Write writes BitVoice's answers.
func (p *Program) Write(b []byte) (int, error) { return p.in.Write(b) }

// Audio passes the caller's audio on to an EAGI program (8 kHz samples); it never blocks.
func (p *Program) Audio(pcm []int16) {
	if p.feed == nil || len(pcm) == 0 {
		return
	}
	b := make([]byte, 2*len(pcm))
	for i, v := range pcm {
		b[2*i], b[2*i+1] = byte(v), byte(uint16(v)>>8)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stop {
		return
	}
	select {
	case p.feed <- b:
	default:
	}
}

// EAGI says whether the program takes the caller's audio.
func (p *Program) EAGI() bool { return p.feed != nil }

// Hangup tells the program the call ended, as Asterisk does: SIGHUP (nothing on Windows, where the
// program sees its input close when BitVoice stops).
func (p *Program) Hangup() { signalHangup(p.cmd) }

// Stop ends the program: its input is closed, and it is killed if it has not ended a few seconds later.
func (p *Program) Stop() {
	p.once.Do(func() {
		p.in.Close()
		p.mu.Lock()
		p.stop = true
		if p.feed != nil {
			close(p.feed)
		}
		p.mu.Unlock()
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			killProcess(p.cmd)
			<-p.done
		}
	})
}
