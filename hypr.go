package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
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
