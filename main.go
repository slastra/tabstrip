// tabstrip drives a browser tab strip in waybar, fed by tabctl's
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

func main() {
	mode := "snapshot"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	c, err := NewFleet()
	if err != nil {
		fmt.Fprintln(os.Stderr, "session bus unavailable:", err)
		os.Exit(1)
	}

	switch mode {
	case "daemon":
		daemon(c)
	case "list":
		// Every tab across every browser and workspace, with chip paths —
		// for lastshell's tab switcher. The daemon's snapshot stays
		// workspace-filtered by design (the bar shows what you can see);
		// the modal is the global view.
		listAll(c)
	case "activate":
		if len(os.Args) > 2 {
			id := os.Args[2]
			// Resolve the tab first: its title is what the window will be
			// renamed to, which is how we find the window to focus.
			var target *Tab
			for _, t := range c.ListAll() {
				if t.ID(t.Browser) == id {
					tt := t
					target = &tt
					break
				}
			}
			if err := c.Activate(id); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			// Follow across workspaces: activating a tab in another
			// workspace's window is useless if you aren't taken there.
			if target != nil {
				focusWindowOfTab(target.Browser, target.Title)
			}
		}
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
		snapshotOnce(c)
	}
}

// snapshotOnce is the one-shot refresh. Unlike the daemon it has no second
// chance, so it waits for the chips its first pass kicked off and writes
// again, leaving the cache warm and the snapshot pointing at real icons
// rather than at fallbacks that were merely the state a moment ago.
//
// The wait is capped a little above fetchTimeout: every fetch is bounded by
// that, so this only ever gives up on a decode pathological enough to outlast
// it, and a debugging command must not hang.
func snapshotOnce(c *Fleet) {
	changed := writeSnapshot(c)
	if !WaitForChips(fetchTimeout + time.Second) {
		fmt.Fprintln(os.Stderr, "tabstrip: some icons were still fetching")
	}
	if writeSnapshot(c) || changed {
		pokeSlots()
	}
}

func gotoSlot(c *Fleet, n int) {
	tabs := readSnapshot()
	if n >= 1 && n <= len(tabs) {
		c.Activate(tabs[n-1].ID)
		if writeSnapshot(c) {
			pokeSlots()
		}
	}
}

func cycle(c *Fleet, step int) {
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
	c.Activate(tabs[(active+step+len(tabs))%len(tabs)].ID)
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

func daemon(c *Fleet) {
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

	// Push path: tabctl's TabsUpdated signal (mediator >= 2.1). No-op on
	// older mediators, which just fall back to the poll below.
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
