// Package backupretention selects one verified recovery copy per object.
package backupretention

import (
	"sort"
	"time"
)

// Copy describes a completed backup; CreatedAt comes from its manifest, not mtime.
type Copy struct {
	Path      string
	Object    string
	SHA256    string
	CreatedAt time.Time
	Verified  bool
	InUse     bool
}

// Plan separates retained copies from obsolete copies and unresolved objects.
type Plan struct {
	Keep    []Copy
	Remove  []Copy
	Blocked []Copy
}

// OnePrevious keeps the newest verified copy for every object.
func OnePrevious(copies []Copy) Plan {
	var p Plan
	pc := make(map[string]int)
	for _, c := range copies {
		pc[c.Path]++
	}
	dup := make(map[string]bool)
	for k, v := range pc {
		if v > 1 {
			dup[k] = true
		}
	}
	byObj := make(map[string][]Copy)
	for _, c := range copies {
		if dup[c.Path] {
			p.Blocked = append(p.Blocked, c)
			continue
		}
		byObj[c.Object] = append(byObj[c.Object], c)
	}
	valid := func(c Copy) bool {
		return c.Verified && c.Path != "" && c.Object != "" && c.SHA256 != "" && !c.CreatedAt.IsZero()
	}
	for _, list := range byObj {
		best := -1
		for i, c := range list {
			if !valid(c) {
				continue
			}
			if best == -1 || c.CreatedAt.After(list[best].CreatedAt) || (c.CreatedAt.Equal(list[best].CreatedAt) && c.Path < list[best].Path) {
				best = i
			}
		}
		for i, c := range list {
			if !valid(c) {
				p.Blocked = append(p.Blocked, c)
			} else if i == best {
				p.Keep = append(p.Keep, c)
			} else if c.InUse {
				p.Blocked = append(p.Blocked, c)
			} else {
				p.Remove = append(p.Remove, c)
			}
		}
	}
	less := func(a, b Copy) bool { return a.Object < b.Object || (a.Object == b.Object && a.Path < b.Path) }
	sort.Slice(p.Keep, func(i, j int) bool { return less(p.Keep[i], p.Keep[j]) })
	sort.Slice(p.Remove, func(i, j int) bool { return less(p.Remove[i], p.Remove[j]) })
	sort.Slice(p.Blocked, func(i, j int) bool { return less(p.Blocked[i], p.Blocked[j]) })
	return p
}
