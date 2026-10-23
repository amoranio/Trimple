package notes

import (
	"sort"
	"strings"
)

// Query filters and groups tasks for the side panel.
type Query struct {
	Group    string // "priority" (default), "label" or "file"
	Label    string
	Search   string
	ShowDone bool
}

// Group is a titled list of tasks.
type Group struct {
	Name  string `json:"name"`
	Open  int    `json:"open"`
	Tasks []Task `json:"tasks"`
}

// Counts are open-task totals.
type Counts struct {
	Total  int `json:"total"`
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
	None   int `json:"none"`
}

// CountOpen counts open tasks by priority.
func CountOpen(tasks []Task) Counts {
	var c Counts
	for _, t := range tasks {
		if t.Done {
			continue
		}
		c.Total++
		switch t.Priority {
		case 1:
			c.High++
		case 2:
			c.Medium++
		case 3:
			c.Low++
		default:
			c.None++
		}
	}
	return c
}

// Labels lists every label in use, sorted.
func Labels(tasks []Task) []string {
	set := map[string]bool{}
	for _, t := range tasks {
		for _, l := range t.Labels {
			set[l] = true
		}
	}
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Select applies the label filter, text search and done filter.
func Select(tasks []Task, q Query) []Task {
	label := strings.ToLower(q.Label)
	search := strings.ToLower(q.Search)
	out := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		if !q.ShowDone && t.Done {
			continue
		}
		if label != "" {
			has := false
			for _, l := range t.Labels {
				if l == label {
					has = true
					break
				}
			}
			if !has {
				continue
			}
		}
		if search != "" {
			hay := strings.ToLower(t.Text + " " + strings.Join(t.Labels, " ") + " " + t.Path)
			if !strings.Contains(hay, search) {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

func prioRank(p int) int {
	if p == 0 {
		return 4
	}
	return p
}

// SortTasks orders open before done, then by priority (none last), file and line.
func SortTasks(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if a.Done != b.Done {
			return !a.Done
		}
		if pa, pb := prioRank(a.Priority), prioRank(b.Priority); pa != pb {
			return pa < pb
		}
		if a.Path != b.Path {
			return lessPath(a.Path, b.Path)
		}
		return a.Line < b.Line
	})
}

func openCount(tasks []Task) int {
	n := 0
	for _, t := range tasks {
		if !t.Done {
			n++
		}
	}
	return n
}

// GroupTasks arranges tasks into the side-panel groups. Empty groups are omitted.
func GroupTasks(tasks []Task, by string) []Group {
	groups := []Group{}
	add := func(name string, items []Task) {
		if len(items) == 0 {
			return
		}
		SortTasks(items)
		groups = append(groups, Group{Name: name, Open: openCount(items), Tasks: items})
	}
	switch by {
	case "label":
		byLabel := map[string][]Task{}
		var none []Task
		for _, t := range tasks {
			if len(t.Labels) == 0 {
				none = append(none, t)
			}
			for _, l := range t.Labels {
				byLabel[l] = append(byLabel[l], t)
			}
		}
		names := make([]string, 0, len(byLabel))
		for l := range byLabel {
			names = append(names, l)
		}
		sort.Strings(names)
		for _, l := range names {
			add("#"+l, byLabel[l])
		}
		add("(no label)", none)
	case "file":
		byFile := map[string][]Task{}
		for _, t := range tasks {
			byFile[t.Path] = append(byFile[t.Path], t)
		}
		names := make([]string, 0, len(byFile))
		for f := range byFile {
			names = append(names, f)
		}
		sort.Slice(names, func(i, j int) bool { return lessPath(names[i], names[j]) })
		for _, f := range names {
			add(f, byFile[f])
		}
	default:
		buckets := map[int][]Task{}
		for _, t := range tasks {
			buckets[t.Priority] = append(buckets[t.Priority], t)
		}
		add("High (!1)", buckets[1])
		add("Medium (!2)", buckets[2])
		add("Low (!3)", buckets[3])
		add("No priority", buckets[0])
	}
	return groups
}
