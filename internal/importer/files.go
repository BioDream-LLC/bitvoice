package importer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/biodream-llc/bitvoice/internal/config"
)

// File is one uploaded file.
type File struct {
	Name string
	Data []byte
}

const maxUnpacked = 64 << 20

// unpack opens archives (a Cisco BAT export is a .tar; people zip several CSV files together) into
// the files inside them.
func unpack(f File) ([]File, error) {
	lower := strings.ToLower(f.Name)
	data := f.Data
	if strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".tgz") || bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s: not a gzip file", f.Name)
		}
		if data, err = io.ReadAll(io.LimitReader(zr, maxUnpacked)); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		lower = strings.TrimSuffix(strings.TrimSuffix(lower, ".gz"), ".tgz") + ".tar"
	}
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("%s: not a zip file BitVoice can open", f.Name)
		}
		var out []File
		total := 0
		for _, zf := range zr.File {
			if zf.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(zf.Name), ".csv") {
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(io.LimitReader(rc, maxUnpacked))
			rc.Close()
			if err != nil {
				return nil, err
			}
			if total += len(b); total > maxUnpacked {
				return nil, errors.New("the archive is too large once opened")
			}
			out = append(out, File{Name: path.Base(zf.Name), Data: b})
		}
		return out, nil
	case strings.HasSuffix(lower, ".tar") || len(data) > 262 && string(data[257:262]) == "ustar":
		tr := tar.NewReader(bytes.NewReader(data))
		var out []File
		total := 0
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("%s: not a tar file BitVoice can open", f.Name)
			}
			if h.Typeflag != tar.TypeReg || !strings.HasSuffix(strings.ToLower(h.Name), ".csv") {
				continue
			}
			b, err := io.ReadAll(io.LimitReader(tr, maxUnpacked))
			if err != nil {
				return nil, err
			}
			if total += len(b); total > maxUnpacked {
				return nil, errors.New("the archive is too large once opened")
			}
			out = append(out, File{Name: path.Base(h.Name), Data: b})
		}
		return out, nil
	}
	return []File{{Name: f.Name, Data: data}}, nil
}

// ParseFiles reads one or more files (or archives of them) together: people and extensions from the
// files that hold them, and call flows from the rest, members resolved against the people found.
func ParseFiles(files []File) (Result, error) {
	var all []File
	for _, f := range files {
		fs, err := unpack(f)
		if err != nil {
			return Result{}, err
		}
		all = append(all, fs...)
	}
	if len(all) == 0 {
		return Result{}, errors.New("there were no CSV files in what was uploaded")
	}
	var res Result
	var formats []string
	var flows []*table
	for _, f := range all {
		if t, err := readTable(f.Name, f.Data); err == nil && kindOf(t) != "" && !isPeople(f.Data) {
			flows = append(flows, t)
			continue
		}
		if r, ok := ipOfficeHuntGroups(f); ok {
			res.RingGroups = append(res.RingGroups, r.RingGroups...)
			res.Warnings = append(res.Warnings, r.Warnings...)
			formats = append(formats, "Avaya IP Office hunt groups")
			continue
		}
		r, err := parse(f.Name, f.Data)
		if err != nil {
			if len(all) == 1 {
				return Result{}, err
			}
			res.Warnings = append(res.Warnings, f.Name+": "+err.Error())
			continue
		}
		formats = append(formats, r.Format)
		res.Extensions = append(res.Extensions, r.Extensions...)
		res.Trunks = append(res.Trunks, r.Trunks...)
		for _, w := range r.Warnings {
			if len(all) > 1 {
				w = f.Name + ": " + w
			}
			res.Warnings = append(res.Warnings, w)
		}
	}
	if len(flows) > 0 {
		// Members may be named by user or by number: users known from this import, by name and by
		// number, resolve to their extensions.
		known := map[string]string{}
		for _, x := range res.Extensions {
			known[strings.ToLower(x.Name)] = x.Number
			known[x.Number] = x.Number
		}
		parseFlows(flows, &res, known)
		formats = append(formats, "call flows")
	}
	resolveIPOfficeMembers(&res)
	res.Format = strings.Join(uniqueStrings(formats), ", ")
	fill(&res)
	sortResult(&res)
	if len(res.Extensions)+len(res.Trunks)+len(res.RingGroups)+len(res.InboundRoutes)+len(res.OutboundRoutes) == 0 {
		return res, errors.New("nothing BitVoice could import was found in these files")
	}
	return res, nil
}

// isPeople says whether a CSV is a people export the vendor readers know (which kindOf might also
// take for a group file, both having an extension column).
func isPeople(data []byte) bool {
	v, _ := vendorFor(data)
	return v != nil
}

func uniqueStrings(in []string) []string {
	return unique(in)
}

// IP Office Manager exports hunt groups as CSV with no header row (File > Import/Export > Export,
// Hunt Group): the group's name, its extension, its members (user names, | between them), its ring
// mode (Group, Hunt, Rotary, Longest Waiting), then settings BitVoice does not use.
var ipoModes = map[string]bool{"group": true, "hunt": true, "rotary": true, "longest waiting": true, "collective": true,
	"sequential": true, "circular": true, "collective call waiting": true}

func ipOfficeHuntGroups(f File) (Result, bool) {
	t, err := readTable(f.Name, append([]byte("c0,c1\n"), f.Data...)) // read every row as data
	if err != nil || len(t.rows) == 0 {
		return Result{}, false
	}
	var res Result
	for _, row := range t.rows {
		if len(row) < 3 {
			return Result{}, false
		}
		name, num := strings.TrimSpace(row[0]), digits(row[1])
		if name == "" || !numberRe.MatchString(num) || strings.EqualFold(name, "name") {
			return Result{}, false
		}
		// The documented order: name, extension, the group's members, its ring mode, then settings.
		var members []string
		for _, m := range strings.FieldsFunc(row[2], func(r rune) bool { return r == '|' || r == ';' }) {
			if m = strings.TrimSpace(m); m != "" {
				members = append(members, m)
			}
		}
		mode := ""
		if len(row) > 3 && ipoModes[strings.ToLower(strings.TrimSpace(row[3]))] {
			mode = strings.TrimSpace(row[3])
		}
		if mode == "" {
			return Result{}, false // not the hunt group layout
		}
		strat, why := strategyOf(mode)
		if why != "" {
			res.Warnings = append(res.Warnings, "hunt group "+num+" "+why)
		}
		res.RingGroups = append(res.RingGroups, config.RingGroup{Number: num, Name: name, Strategy: strat, Members: members, RingSeconds: 15})
	}
	return res, true
}

// resolveIPOfficeMembers turns member user names (as IP Office lists them) into extension numbers.
func resolveIPOfficeMembers(res *Result) {
	byName := map[string]string{}
	for _, x := range res.Extensions {
		byName[strings.ToLower(x.Name)] = x.Number
	}
	kept := res.RingGroups[:0]
	for _, rg := range res.RingGroups {
		var ms []string
		for _, m := range rg.Members {
			switch {
			case numberRe.MatchString(m):
				ms = append(ms, m)
			case byName[strings.ToLower(m)] != "":
				ms = append(ms, byName[strings.ToLower(m)])
			default:
				res.Warnings = append(res.Warnings, fmt.Sprintf("group %s lists %q, who is not among the people imported: import the users file with it", rg.Number, m))
			}
		}
		rg.Members = unique(ms)
		if len(rg.Members) == 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("group %s %s has no members BitVoice knows, left out", rg.Number, rg.Name))
			continue
		}
		kept = append(kept, rg)
	}
	res.RingGroups = kept
}
