package gopro

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"goprobuddy/internal/diag"
)

// SettingOption is one choice for a camera setting, e.g. {Value: 1, Name: "4K"}.
type SettingOption struct {
	Value int
	Name  string
}

// Setting is a camera setting and the choices this camera offers for it.
type Setting struct {
	ID      int
	Name    string
	Group   string // the mode or section it was listed under, e.g. "Video"
	Options []SettingOption
}

// Filter is one availability rule from the camera definition: while every condition in When
// holds, the listed options of each setting in Block are not allowed.
type Filter struct {
	When  []FilterCond
	Block map[int][]int // setting id -> blocked option values
}

// FilterCond matches a status (Status true) or a setting whose current value is in Values.
type FilterCond struct {
	Status bool
	ID     int
	Values []int
}

// Blocked applies filters to the camera's current status and setting values and returns,
// per setting id, the option values that are not allowed right now. A setting whose options
// are all blocked does not apply in the current mode, and the camera's menus hide it.
func Blocked(filters []Filter, status, values map[int]int) map[int]map[int]bool {
	out := map[int]map[int]bool{}
	for _, f := range filters {
		active := true
		for _, c := range f.When {
			cur, ok := values[c.ID]
			if c.Status {
				cur, ok = status[c.ID]
			}
			if !ok || !containsInt(c.Values, cur) {
				active = false
				break
			}
		}
		if !active {
			continue
		}
		for id, opts := range f.Block {
			if out[id] == nil {
				out[id] = map[int]bool{}
			}
			for _, o := range opts {
				out[id][o] = true
			}
		}
	}
	return out
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func intList(v any) []int {
	arr, _ := v.([]any)
	out := make([]int, 0, len(arr))
	for _, x := range arr {
		if f, ok := x.(float64); ok {
			out = append(out, int(f))
		}
	}
	return out
}

// parseFilters reads the definition's "filters":
// [{"activated_by":[{"status_id":89,"values":[...]} | {"setting_id":2,"ids":[...]}],
//
//	"blacklist":[{"setting_id":3,"opt_ids":[...]}]}]
func parseFilters(root any) []Filter {
	m, _ := root.(map[string]any)
	raw, _ := m["filters"].([]any)
	var out []Filter
	for _, r := range raw {
		fm, _ := r.(map[string]any)
		f := Filter{Block: map[int][]int{}}
		acts, _ := fm["activated_by"].([]any)
		for _, a := range acts {
			am, _ := a.(map[string]any)
			if id, ok := am["status_id"].(float64); ok {
				f.When = append(f.When, FilterCond{Status: true, ID: int(id), Values: intList(am["values"])})
			} else if id, ok := am["setting_id"].(float64); ok {
				f.When = append(f.When, FilterCond{ID: int(id), Values: intList(am["ids"])})
			}
		}
		bls, _ := fm["blacklist"].([]any)
		for _, b := range bls {
			bm, _ := b.(map[string]any)
			if id, ok := bm["setting_id"].(float64); ok {
				f.Block[int(id)] = append(f.Block[int(id)], intList(bm["opt_ids"])...)
			}
		}
		if len(f.When) > 0 && len(f.Block) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// Settings reads the camera's own definition (GET /gp/gpControl, docs/PROTOCOL.md §4), so the
// list matches whatever model and firmware is connected instead of a hard-coded table.
// It also returns the definition's availability filters; see Blocked.
func (c *Client) Settings(ctx context.Context) ([]Setting, []Filter, error) {
	body, err := c.get(ctx, "/gp/gpControl")
	if err != nil {
		return nil, nil, err
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		diag.Logf("settings", "camera definition did not parse: %v, body: %.300s", err, body)
		return nil, nil, fmt.Errorf("camera definition: %w", err)
	}
	seen := map[int]bool{}
	var out []Setting
	var walk func(v any, group string)
	walk = func(v any, group string) {
		switch t := v.(type) {
		case map[string]any:
			if s, ok := asSetting(t, group); ok {
				if !seen[s.ID] {
					seen[s.ID] = true
					out = append(out, s)
				}
				return
			}
			if dn, ok := t["display_name"].(string); ok && dn != "" {
				group = dn
			}
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys) // "modes" before "settings", so settings group under their mode
			for _, k := range keys {
				walk(t[k], group)
			}
		case []any:
			for _, e := range t {
				walk(e, group)
			}
		}
	}
	walk(root, "General")
	out = groupByHints(root, out)
	if len(out) == 0 {
		diag.Logf("settings", "definition had no settings, body: %.500s", body)
		return nil, nil, errors.New("camera did not describe any settings")
	}
	filters := parseFilters(root)
	diag.Logf("settings", "camera describes %d settings and %d availability filters (%d bytes of definition)", len(out), len(filters), len(body))
	return out, filters, nil
}

// groupByHints orders and groups settings the way the camera's own menus do, using the
// definition's display_hints ({display_name, settings:[{setting_id}]}). Settings the hints
// don't mention are internal and are dropped. Without hints the list is returned unchanged.
func groupByHints(root any, list []Setting) []Setting {
	m, _ := root.(map[string]any)
	hints, _ := m["display_hints"].([]any)
	if len(hints) == 0 {
		return list
	}
	byID := make(map[int]Setting, len(list))
	for _, s := range list {
		byID[s.ID] = s
	}
	var out []Setting
	used := map[int]bool{}
	for _, h := range hints {
		hm, _ := h.(map[string]any)
		group, _ := hm["display_name"].(string)
		refs, _ := hm["settings"].([]any)
		for _, r := range refs {
			rm, _ := r.(map[string]any)
			id, ok := rm["setting_id"].(float64)
			s, found := byID[int(id)]
			if !ok || !found || used[s.ID] {
				continue
			}
			used[s.ID] = true
			s.Group = group
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return list
	}
	diag.Logf("settings", "%d of %d settings are in the camera's menus (display_hints)", len(out), len(list))
	return out
}

// asSetting recognises a setting entry: an object with a numeric id, a name and a list of options.
func asSetting(m map[string]any, group string) (Setting, bool) {
	id, ok1 := m["id"].(float64)
	name, ok2 := m["display_name"].(string)
	opts, ok3 := m["options"].([]any)
	if !ok1 || !ok2 || !ok3 || len(opts) == 0 {
		return Setting{}, false
	}
	s := Setting{ID: int(id), Name: name, Group: group}
	for _, o := range opts {
		om, ok := o.(map[string]any)
		if !ok {
			continue
		}
		// Newer firmware calls the option number "value"; HERO9-era firmware calls it "id".
		v, okv := om["value"].(float64)
		if !okv {
			v, okv = om["id"].(float64)
		}
		n, okn := om["display_name"].(string)
		if okv && okn {
			s.Options = append(s.Options, SettingOption{Value: int(v), Name: n})
		}
	}
	return s, len(s.Options) > 0
}

// SettingValues returns the current option of every setting, keyed by setting id.
func (c *Client) SettingValues(ctx context.Context) (map[int]int, error) {
	_, vals, err := c.CameraState(ctx)
	return vals, err
}

// CameraState returns the camera's numeric status values and current setting values.
func (c *Client) CameraState(ctx context.Context) (status, values map[int]int, err error) {
	var body []byte
	for _, p := range []string{"/gopro/camera/state", "/gp/gpControl/status"} {
		if body, err = c.get(ctx, p); err == nil {
			break
		}
	}
	if err != nil {
		return nil, nil, err
	}
	var raw struct {
		Status   map[string]json.RawMessage `json:"status"`
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, nil, err
	}
	ints := func(m map[string]json.RawMessage) map[int]int {
		out := make(map[int]int, len(m))
		for k, v := range m {
			id, err1 := strconv.Atoi(k)
			var n int
			if err1 == nil && json.Unmarshal(v, &n) == nil {
				out[id] = n
			}
		}
		return out
	}
	return ints(raw.Status), ints(raw.Settings), nil
}

// SetSetting changes one setting. The camera rejects options that clash with the current
// mode or other settings; the error says so.
func (c *Client) SetSetting(ctx context.Context, id, option int) error {
	diag.Logf("settings", "set setting %d to option %d", id, option)
	_, err := c.get(ctx, fmt.Sprintf("/gopro/camera/setting?setting=%d&option=%d", id, option))
	if err == nil {
		return nil
	}
	if _, err2 := c.get(ctx, fmt.Sprintf("/gp/gpControl/setting/%d/%d", id, option)); err2 == nil {
		diag.Logf("settings", "set via legacy gpControl API")
		return nil
	}
	diag.Logf("settings", "camera rejected setting %d option %d", id, option)
	return fmt.Errorf("camera rejected the change (it may not be allowed in the current mode): %w", err)
}
