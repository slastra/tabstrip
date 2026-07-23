// tabstrip drives a kitty-style browser tab strip in waybar, fed by tabctl's
// D-Bus mediator. Successor to the firefox_tabs.py daemon: native D-Bus and
// Hyprland IPC, no subprocess spawns on the hot path.
//
//	tabstrip daemon    run persistently (spawned by the hidden waybar module):
//	                   refresh on Hyprland activewindow/windowtitle events and
//	                   on tabctl's TabsUpdated signal (when available), with a
//	                   fallback poll; poke slots via SIGRTMIN+8 on change
//	tabstrip snapshot  one-shot refresh (debugging; default)
//	tabstrip goto N    activate the Nth displayed tab (slot click binding)
//	tabstrip next|prev cycle the active tab (scroll bindings)
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const browser = "Firefox"

func main() {
	mode := "snapshot"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	c, err := NewTabCtl(browser)
	if err != nil {
		fmt.Fprintln(os.Stderr, "session bus unavailable:", err)
		os.Exit(1)
	}

	switch mode {
	case "daemon":
		daemon(c)
	case "goto":
		if len(os.Args) > 2 {
			if n, err := strconv.Atoi(os.Args[2]); err == nil {
				gotoSlot(c, n)
			}
		}
	case "next":
		cycle(c, 1)
	case "prev":
		cycle(c, -1)
	default:
		if writeSnapshot(c) {
			pokeSlots()
		}
	}
}

// activateID parses "firefox.<window>.<tab>" and activates the tab.
func activateID(c *TabCtl, id string) {
	parts := strings.Split(id, ".")
	if len(parts) != 3 {
		return
	}
	tabID, err := strconv.Atoi(parts[2])
	if err != nil {
		return
	}
	c.ActivateTab(int32(tabID))
}

func gotoSlot(c *TabCtl, n int) {
	tabs := readSnapshot()
	if n >= 1 && n <= len(tabs) {
		activateID(c, tabs[n-1].ID)
		if writeSnapshot(c) {
			pokeSlots()
		}
	}
}

func cycle(c *TabCtl, step int) {
	tabs := readSnapshot()
	active := -1
	for i, t := range tabs {
		if t.Active {
			active = i
			break
		}
	}
	if active < 0 || len(tabs) < 2 {
		return
	}
	activateID(c, tabs[(active+step+len(tabs))%len(tabs)].ID)
	if writeSnapshot(c) {
		pokeSlots()
	}
}

// replacePriorInstance kills any daemon left over from a previous waybar.
func replacePriorInstance() {
	pidfile := filepath.Join(runtimeDir(), "waybar-fftabs.pid")
	if b, err := os.ReadFile(pidfile); err == nil {
		if old, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && old != os.Getpid() {
			syscall.Kill(old, syscall.SIGTERM)
		}
	}
	os.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func daemon(c *TabCtl) {
	replacePriorInstance()

	// Hidden waybar module expects one line of module JSON.
	fmt.Println(`{"text": ""}`)

	if writeSnapshot(c) {
		pokeSlots()
	}

	const (
		minGap   = 300 * time.Millisecond
		fallback = 1500 * time.Millisecond
	)

	// Hyprland events → wake channel. Reconnects forever; Hyprland outliving
	// this process is not assumed (boot races, compositor restarts).
	wake := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := hyprEvents()
			if err != nil {
				time.Sleep(2 * time.Second)
				continue
			}
			sc := bufio.NewScanner(conn)
			sc.Buffer(make([]byte, 64*1024), 64*1024)
			for sc.Scan() {
				line := sc.Text()
				if strings.HasPrefix(line, "activewindow>>") || strings.HasPrefix(line, "windowtitle>>") {
					select {
					case wake <- struct{}{}:
					default:
					}
				}
			}
			conn.Close()
			time.Sleep(time.Second)
		}
	}()

	// Future push path: tabctl TabsUpdated signal (no-op until it exists).
	if sig, err := c.SubscribeTabsUpdated(); err == nil {
		go func() {
			for range sig {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}()
	}

	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	pending := false

	for {
		var timeout <-chan time.Time
		if !pending {
			timeout = time.After(fallback)
		}
		select {
		case <-wake:
			if !pending {
				pending = true
				debounce.Reset(minGap)
			}
		case <-debounce.C:
			pending = false
			if writeSnapshot(c) {
				pokeSlots()
			}
		case <-timeout:
			if writeSnapshot(c) {
				pokeSlots()
			}
		}
	}
}
