package agi

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/biodream-llc/bitvoice/internal/media"
)

// Library is where AGI scripts find their sounds: files uploaded under a name ("custom/welcome",
// as under Asterisk's sounds folder) and recordings the scripts made (RECORD FILE, Record), kept
// for a number of days. Every file is kept as 8 kHz WAV.
type Library struct {
	Dir string // DATA/agi
	// KeepDays is how long recordings scripts made are kept (default 30).
	KeepDays int
	// MaxBytes caps the sounds and recordings together (default 1 GB): uploads and recordings past
	// it are refused.
	MaxBytes int64
	mu       sync.Mutex
}

// Sound is one file of the library.
type Sound struct {
	Name     string    `json:"name"`
	Recorded bool      `json:"recorded"` // made by a script, removed after KeepDays
	Seconds  float64   `json:"seconds"`
	Bytes    int64     `json:"bytes"`
	Modified time.Time `json:"modified"`
}

var soundNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+){0,4}$`)

// ErrSoundName is a name that cannot be a sound's.
var ErrSoundName = errors.New("a sound's name is letters, digits, . - and _, in up to five folders (custom/welcome)")

// ErrFull is the library over its size limit.
var ErrFull = errors.New("the sound library is full: remove sounds or recordings first")

// CleanName turns what a script names into a library name: Asterisk's sounds folder and the
// language folder are left out, and so is a file extension (/var/lib/asterisk/sounds/en/custom/
// welcome.gsm is custom/welcome).
func CleanName(name string) (string, error) {
	n := strings.TrimSpace(strings.ReplaceAll(name, `\`, "/"))
	for _, p := range []string{"/var/lib/asterisk/sounds/", "/usr/share/asterisk/sounds/", "/var/spool/asterisk/"} {
		if strings.HasPrefix(n, p) {
			n = n[len(p):]
			break
		}
	}
	n = strings.TrimLeft(n, "/")
	if ext := filepath.Ext(n); ext != "" && strings.LastIndexByte(n, '.') > strings.LastIndexByte(n, '/') {
		switch strings.ToLower(ext) {
		case ".wav", ".gsm", ".ulaw", ".ul", ".alaw", ".al", ".sln", ".sln16", ".raw", ".pcm", ".mp3", ".g722", ".wav49":
			n = n[:len(n)-len(ext)]
		}
	}
	if !soundNameRe.MatchString(n) || strings.Contains(n, "..") {
		return "", ErrSoundName
	}
	return n, nil
}

var languageDirs = map[string]bool{"en": true, "es": true, "fr": true, "de": true, "it": true, "pt": true, "nl": true, "pl": true, "en_US": true, "en_GB": true, "es_MX": true, "fr_CA": true, "pt_BR": true}

func (l *Library) path(kind, name string) string {
	return filepath.Join(l.Dir, kind, filepath.FromSlash(name)+".wav")
}

// Find returns a sound by the name a script used: in the caller's language first (en/custom/x),
// then without a language; recordings before uploaded sounds.
func (l *Library) Find(name, lang string) ([]int16, bool) {
	n, err := CleanName(name)
	if err != nil {
		return nil, false
	}
	first, rest, _ := strings.Cut(n, "/")
	try := []string{n}
	if lang != "" && !languageDirs[first] {
		try = []string{lang + "/" + n, n}
	} else if languageDirs[first] && rest != "" {
		try = []string{n, rest}
	}
	for _, t := range try {
		for _, kind := range []string{"recorded", "sounds"} {
			if b, err := os.ReadFile(l.path(kind, t)); err == nil {
				if pcm, err := media.DecodeWAV(b); err == nil {
					return pcm, true
				}
			}
		}
	}
	return nil, false
}

func (l *Library) limit() int64 {
	if l.MaxBytes > 0 {
		return l.MaxBytes
	}
	return 1 << 30
}

func (l *Library) used() int64 {
	var n int64
	filepath.WalkDir(l.Dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".wav") {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func (l *Library) write(kind, name string, pcm []int16) error {
	n, err := CleanName(name)
	if err != nil {
		return err
	}
	b := media.EncodeWAV(pcm)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used()+int64(len(b)) > l.limit() {
		return ErrFull
	}
	p := l.path(kind, n)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// SaveRecording keeps what a script recorded under the name it gave.
func (l *Library) SaveRecording(name string, pcm []int16) error {
	return l.write("recorded", name, pcm)
}

// Upload adds a sound under a name, from a file in any format the library reads (see DecodeSound).
func (l *Library) Upload(name, filename string, b []byte) error {
	pcm, err := DecodeSound(filename, b)
	if err != nil {
		return err
	}
	return l.write("sounds", name, pcm)
}

// Open returns a file's WAV bytes, for listening to it on the screen.
func (l *Library) Open(name string, recorded bool) ([]byte, error) {
	n, err := CleanName(name)
	if err != nil {
		return nil, err
	}
	kind := "sounds"
	if recorded {
		kind = "recorded"
	}
	return os.ReadFile(l.path(kind, n))
}

// Remove deletes a sound or a recording.
func (l *Library) Remove(name string, recorded bool) error {
	n, err := CleanName(name)
	if err != nil {
		return err
	}
	kind := "sounds"
	if recorded {
		kind = "recorded"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return os.Remove(l.path(kind, n))
}

// List returns every sound and recording, by name.
func (l *Library) List() []Sound {
	var out []Sound
	for _, kind := range []string{"sounds", "recorded"} {
		root := filepath.Join(l.Dir, kind)
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".wav") {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			out = append(out, Sound{Name: strings.TrimSuffix(filepath.ToSlash(rel), ".wav"), Recorded: kind == "recorded",
				Seconds: float64(max(fi.Size()-44, 0)) / 16000, Bytes: fi.Size(), Modified: fi.ModTime().UTC()})
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Recorded != out[j].Recorded {
			return !out[i].Recorded
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Prune removes recordings older than KeepDays; it returns how many went.
func (l *Library) Prune(now time.Time) int {
	days := l.KeepDays
	if days <= 0 {
		days = 30
	}
	cut := now.Add(-time.Duration(days) * 24 * time.Hour)
	n := 0
	l.mu.Lock()
	defer l.mu.Unlock()
	filepath.WalkDir(filepath.Join(l.Dir, "recorded"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().Before(cut) && os.Remove(p) == nil {
			n++
		}
		return nil
	})
	return n
}

// ErrSoundFormat is a file the library cannot read.
var ErrSoundFormat = errors.New("upload WAV, MP3, or Asterisk's .gsm, .ulaw, .alaw, .sln or .sln16 files")

// DecodeSound reads a sound file as 8 kHz audio: WAV and MP3 by their content, and Asterisk's raw
// formats by their extension (.gsm, .ulaw/.ul/.pcm, .alaw/.al, .sln/.raw 8 kHz, .sln16 16 kHz).
func DecodeSound(filename string, b []byte) ([]int16, error) {
	if pcm, err := media.DecodeAudio(b); err == nil {
		return pcm, nil
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".gsm":
		if len(b) < 33 || len(b)%33 != 0 {
			return nil, errors.New("a .gsm file is whole 33-byte frames")
		}
		d := media.NewGSMDecoder()
		var pcm []int16
		for i := 0; i+33 <= len(b); i += 33 {
			pcm = append(pcm, d.Decode(b[i:i+33])...)
		}
		return pcm, nil
	case ".ulaw", ".ul", ".pcm", ".mu":
		pcm := make([]int16, len(b))
		for i, u := range b {
			pcm[i] = media.UlawToLinear(u)
		}
		return pcm, nil
	case ".alaw", ".al":
		pcm := make([]int16, len(b))
		for i, a := range b {
			pcm[i] = media.AlawToLinear(a)
		}
		return pcm, nil
	case ".sln", ".raw", ".slin":
		pcm := make([]int16, len(b)/2)
		for i := range pcm {
			pcm[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
		}
		return pcm, nil
	case ".sln16", ".slin16":
		pcm := make([]int16, len(b)/4)
		for i := range pcm { // 16 kHz to 8 kHz: the mean of each pair
			a, c := int32(int16(binary.LittleEndian.Uint16(b[4*i:]))), int32(int16(binary.LittleEndian.Uint16(b[4*i+2:])))
			pcm[i] = int16((a + c) / 2)
		}
		return pcm, nil
	}
	return nil, ErrSoundFormat
}

// asteriskSounds are sounds Asterisk ships that BitVoice has a prompt for.
var asteriskSounds = map[string]string{
	"beep": "beep", "vm-goodbye": "goodbye", "goodbye": "goodbye", "auth-thankyou": "thank-you",
	"thank-you-for-calling": "thank-you", "invalid": "invalid", "option-is-invalid": "invalid",
	"pbx-invalid": "invalid", "please-hold": "please-hold", "pls-hold-while-try": "please-hold",
	"agent-pass": "enter-pin", "vm-password": "enter-pin", "conf-getpin": "enter-pin",
	"auth-incorrect": "pin-wrong", "vm-incorrect": "pin-wrong", "conf-invalidpin": "pin-wrong",
	"dir-intro": "directory-intro", "dir-nomatch": "directory-none", "queue-thankyou": "thank-you",
	"tt-allbusy": "please-hold", "vm-enter-num-to-call": "enter-number",
}

// soundKey turns a sound Asterisk names into one BitVoice has: digits/5 is digit-5, silence/2 is two
// seconds of silence, vm-goodbye is BitVoice's goodbye; any other name is tried as a BitVoice prompt.
func soundKey(name string) string {
	n, err := CleanName(name)
	if err != nil {
		return name
	}
	if first, rest, ok := strings.Cut(n, "/"); ok && languageDirs[first] {
		n = rest
	}
	if d, ok := strings.CutPrefix(n, "digits/"); ok && len(d) == 1 && d[0] >= '0' && d[0] <= '9' {
		return "digit-" + d
	}
	if d, ok := strings.CutPrefix(n, "silence/"); ok {
		return "silence:" + d
	}
	if k, ok := asteriskSounds[n]; ok {
		return k
	}
	return n
}

// DB is Asterisk's database (AstDB) for scripts: values by family and key, kept in one file.
type DB struct {
	Path string
	mu   sync.Mutex
	data map[string]map[string]string
}

const dbMaxKeys = 100000

func (d *DB) load() {
	if d.data != nil {
		return
	}
	d.data = map[string]map[string]string{}
	if b, err := os.ReadFile(d.Path); err == nil {
		json.Unmarshal(b, &d.data)
	}
}

func (d *DB) save() error {
	b, err := json.Marshal(d.data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o700); err != nil {
		return err
	}
	tmp := d.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.Path)
}

func (d *DB) count() int {
	n := 0
	for _, f := range d.data {
		n += len(f)
	}
	return n
}

// Get returns a value.
func (d *DB) Get(family, key string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	v, ok := d.data[family][key]
	return v, ok
}

// Put sets a value.
func (d *DB) Put(family, key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	if _, ok := d.data[family][key]; !ok && d.count() >= dbMaxKeys {
		return errors.New("agi: the database is full")
	}
	if d.data[family] == nil {
		d.data[family] = map[string]string{}
	}
	d.data[family][key] = value
	return d.save()
}

// Del removes a value; false when there was none.
func (d *DB) Del(family, key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	if _, ok := d.data[family][key]; !ok {
		return false
	}
	delete(d.data[family], key)
	if len(d.data[family]) == 0 {
		delete(d.data, family)
	}
	return d.save() == nil
}

// DelTree removes a family, or its keys under a key tree (key or key/...); it returns how many went.
func (d *DB) DelTree(family, tree string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	n := 0
	for k := range d.data[family] {
		if tree == "" || k == tree || strings.HasPrefix(k, tree+"/") {
			delete(d.data[family], k)
			n++
		}
	}
	if len(d.data[family]) == 0 {
		delete(d.data, family)
	}
	if n > 0 {
		d.save()
	}
	return n
}

// All returns every value, for the screen: family -> key -> value.
func (d *DB) All() map[string]map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	out := map[string]map[string]string{}
	for f, kv := range d.data {
		out[f] = map[string]string{}
		for k, v := range kv {
			out[f][k] = v
		}
	}
	return out
}
