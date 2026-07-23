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

// activeWindow queries Hyprland's request socket for the focused window's
// class and title (replaces shelling out to `hyprctl activewindow -j`).
func activeWindow() (class, title string) {
	conn, err := net.DialTimeout("unix", filepath.Join(hyprDir(), ".socket.sock"), 2*time.Second)
	if err != nil {
		return "", ""
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("j/activewindow")); err != nil {
		return "", ""
	}
	var aw struct {
		Class string `json:"class"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(conn).Decode(&aw); err != nil {
		return "", ""
	}
	return aw.Class, aw.Title
}
