package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// listAll prints every connected browser's tabs as JSON for the switcher,
// most recently touched first (see Recency): id, title, chip path (bright
// variant — the modal has no focus dimming), active flag, window id so the
// modal can group or badge by window, and focused for the one tab you are
// looking at, so the modal can sink it the way alt-tab does.
func listAll(f *Fleet) {
	type row struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Icon     string `json:"icon"`
		WindowID int32  `json:"windowId"`
		Active   bool   `json:"active"`
		Focused  bool   `json:"focused"`
		Browser  string `json:"browser"`
	}
	dir := iconDir()
	tabs := f.ListAll()
	SortByRecency(tabs, loadRecency().last)
	win, hasFocus := focusedWindow(tabs)
	focused := focusedTab(tabs, win, hasFocus)
	var rows []row
	for _, t := range tabs {
		id := t.ID(t.Browser)
		rows = append(rows, row{
			ID:       id,
			Title:    t.Title,
			Icon:     iconForList(dir, t.FavIconURL, t.Browser),
			WindowID: t.WindowID,
			Active:   t.Active,
			Focused:  id == focused,
			Browser:  t.Browser,
		})
	}
	if rows == nil {
		rows = []row{}
	}
	out, _ := json.Marshal(rows)
	fmt.Fprintln(os.Stdout, string(out))
}

// iconForList is iconFor without the dim machinery: bright chip if cached,
// browser fallback otherwise.
func iconForList(dir, favIconURL, browser string) string {
	return iconFor(favIconURL, browser, true)
}
