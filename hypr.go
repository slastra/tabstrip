package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func hyprDir() string {
	base := filepath.Join(runtimeDir(), "hypr")
	if sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"); sig != "" {
		return filepath.Join(base, sig)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) == 0 {
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return filepath.Join(base, names[0])
}

// hyprEvents connects to Hyprland's event socket (.socket2.sock).
func hyprEvents() (net.Conn, error) {
	return net.DialTimeout("unix", filepath.Join(hyprDir(), ".socket2.sock"), 2*time.Second)
}

// hyprRequest issues one request on Hyprland's request socket and decodes the
// JSON reply into v. Replaces shelling out to hyprctl.
func hyprRequest(req string, v any) error {
	conn, err := net.DialTimeout("unix", filepath.Join(hyprDir(), ".socket.sock"), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(req)); err != nil {
		return err
	}
	return json.NewDecoder(conn).Decode(v)
}

// activeWindow returns the focused window's class and title.
func activeWindow() (class, title string) {
	var aw struct {
		Class string `json:"class"`
		Title string `json:"title"`
	}
	if err := hyprRequest("j/activewindow", &aw); err != nil {
		return "", ""
	}
	return aw.Class, aw.Title
}

// HyprClient is the subset of a Hyprland client the strip needs.
type HyprClient struct {
	Class     string `json:"class"`
	Title     string `json:"title"`
	At        [2]int `json:"at"`
	Workspace struct {
		ID int `json:"id"`
	} `json:"workspace"`
}

// clientsOnActiveWorkspace returns the windows on the focused workspace,
// ordered left to right then top to bottom, so chips read in the same order
// the windows are arranged on screen.
func clientsOnActiveWorkspace() []HyprClient {
	var ws struct {
		ID int `json:"id"`
	}
	if err := hyprRequest("j/activeworkspace", &ws); err != nil {
		return nil
	}

	var all []HyprClient
	if err := hyprRequest("j/clients", &all); err != nil {
		return nil
	}

	var on []HyprClient
	for _, c := range all {
		if c.Workspace.ID == ws.ID {
			on = append(on, c)
		}
	}
	sort.Slice(on, func(i, j int) bool {
		if on[i].At[0] != on[j].At[0] {
			return on[i].At[0] < on[j].At[0]
		}
		return on[i].At[1] < on[j].At[1]
	})
	return on
}

// hyprDispatch issues a dispatcher on the request socket and discards the
// reply. The Lua-config Hyprland parses dispatch arguments as Lua, so
// callers pass the hl.dsp form, e.g.
//   hl.dsp.focus({ window = "address:0x..." })
func hyprDispatch(lua string) error {
	conn, err := net.DialTimeout("unix", filepath.Join(hyprDir(), ".socket.sock"), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("dispatch " + lua)); err != nil {
		return err
	}
	buf := make([]byte, 256)
	conn.Read(buf)
	return nil
}

// focusWindowOfTab brings the Hyprland window showing the given tab to the
// front, workspace switch included. The freshly activated tab drives its
// window's title, so we poll briefly for a client whose stripped title
// matches, then focus by address. Best effort: a title that never appears
// (miniature race, unusual title mangling) just means no focus jump.
func focusWindowOfTab(browser, tabTitle string) {
	classFrag := strings.ToLower(browser)
	if classFrag == "chrome" {
		classFrag = "chrom" // google-chrome, chromium
	}
	for attempt := 0; attempt < 6; attempt++ {
		var all []struct {
			Class   string `json:"class"`
			Title   string `json:"title"`
			Address string `json:"address"`
		}
		if err := hyprRequest("j/clients", &all); err != nil {
			return
		}
		for _, c := range all {
			if !strings.Contains(strings.ToLower(c.Class), classFrag) {
				continue
			}
			page := c.Title
			for _, suffix := range windowTitleSuffixes {
				if i := strings.LastIndex(page, suffix); i >= 0 {
					page = page[:i]
					break
				}
			}
			if page == tabTitle {
				hyprDispatch(fmt.Sprintf("hl.dsp.focus({ window = %q })", "address:"+c.Address))
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}
