package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// listAll prints every connected browser's tabs as JSON for the switcher:
// id, title, chip path (bright variant — the modal has no focus dimming),
// active flag, and window id so the modal can group or badge by window.
func listAll(f *Fleet) {
	type row struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Icon     string `json:"icon"`
		WindowID int32  `json:"windowId"`
		Active   bool   `json:"active"`
		Browser  string `json:"browser"`
	}
	dir := iconDir()
	var rows []row
	for _, t := range f.ListAll() {
		rows = append(rows, row{
			ID:       t.ID(t.Browser),
			Title:    t.Title,
			Icon:     iconForList(dir, t.FavIconURL, t.Browser),
			WindowID: t.WindowID,
			Active:   t.Active,
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
