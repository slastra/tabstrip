package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	dbusServiceBase = "dev.slastra.TabCtl"
	dbusInterface   = "dev.slastra.TabCtl.Browser"
	dbusObjectBase  = "/dev/slastra/TabCtl/Browser/"
	dbusCallTimeout = 5 * time.Second
	tabsUpdatedSig  = dbusInterface + ".TabsUpdated"
)

// TabInfo mirrors tabctl's ListTabsWithIcons wire struct (a(iissibbs)):
// ListTabs plus the browser-resolved favicon URL.
//
// tabctl froze ListTabs at a(iissibb) and put new fields behind the new
// method, so this field order must track that signature exactly. D-Bus
// matches structs by arity, and a mismatch fails the call outright rather
// than degrading.
type TabInfo struct {
	WindowID   int32
	TabID      int32
	Title      string
	URL        string
	Index      int32
	Active     bool
	Pinned     bool
	FavIconURL string
}

// ID composes the user-facing tab token, e.g. "firefox.127.9".
func (t TabInfo) ID(browser string) string {
	return fmt.Sprintf("%s.%d.%d", strings.ToLower(browser), t.WindowID, t.TabID)
}

// Tab is a TabInfo tagged with the browser it came from. TabInfo itself is the
// D-Bus wire struct and must keep its exact field set, so the browser is
// carried alongside rather than added to it.
type Tab struct {
	TabInfo
	Browser string
}

type TabCtl struct {
	conn    *dbus.Conn
	browser string
}

func NewTabCtl(browser string) (*TabCtl, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}
	return &TabCtl{conn: conn, browser: browser}, nil
}

// Fleet is every tabctl mediator on the session bus: Firefox and Chrome at
// once, and a separate entry per browser profile, since tabctl gives each
// profile its own bus name (Chrome, Chrome2).
type Fleet struct {
	conn *dbus.Conn
}

func NewFleet() (*Fleet, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}
	return &Fleet{conn: conn}, nil
}

// Browsers lists the mediators currently registered. Discovery runs on every
// call rather than once at startup, so a browser opened later joins the strip
// without restarting the daemon.
func (f *Fleet) Browsers() []string {
	ctx, cancel := context.WithTimeout(context.Background(), dbusCallTimeout)
	defer cancel()

	var names []string
	obj := f.conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus")
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return nil
	}

	prefix := dbusServiceBase + "."
	var out []string
	for _, n := range names {
		if b := strings.TrimPrefix(n, prefix); b != n && b != "" {
			out = append(out, b)
		}
	}
	sort.Strings(out)
	return out
}

func (f *Fleet) client(browser string) *TabCtl {
	return &TabCtl{conn: f.conn, browser: browser}
}

// ListAll returns every tab across every connected browser. A mediator that
// fails is skipped: one wedged browser must not blank the whole strip.
func (f *Fleet) ListAll() []Tab {
	var all []Tab
	for _, b := range f.Browsers() {
		tabs, err := f.client(b).ListTabs()
		if err != nil {
			continue
		}
		for _, t := range tabs {
			all = append(all, Tab{TabInfo: t, Browser: b})
		}
	}
	return all
}

// Activate routes a composed "<browser>.<window>.<tab>" ID back to the
// mediator that owns it.
func (f *Fleet) Activate(id string) error {
	parts := strings.Split(id, ".")
	if len(parts) != 3 {
		return fmt.Errorf("malformed tab id %q", id)
	}
	tabID, err := strconv.Atoi(parts[2])
	if err != nil {
		return fmt.Errorf("malformed tab id %q", id)
	}
	for _, b := range f.Browsers() {
		if strings.EqualFold(b, parts[0]) {
			return f.client(b).ActivateTab(int32(tabID))
		}
	}
	return fmt.Errorf("no connected browser for %q", parts[0])
}

// SubscribeTabsUpdated matches on interface and member, not sender, so one
// subscription covers every mediator including ones that appear later.
func (f *Fleet) SubscribeTabsUpdated() (chan *dbus.Signal, error) {
	if err := f.conn.AddMatchSignal(
		dbus.WithMatchInterface(dbusInterface),
		dbus.WithMatchMember("TabsUpdated"),
	); err != nil {
		return nil, err
	}
	ch := make(chan *dbus.Signal, 16)
	f.conn.Signal(ch)
	return ch, nil
}

func (c *TabCtl) obj() dbus.BusObject {
	return c.conn.Object(dbusServiceBase+"."+c.browser, dbus.ObjectPath(dbusObjectBase+c.browser))
}

// ListTabs calls ListTabsWithIcons so each tab carries its favicon URL.
// Requires tabctl 2.2.0+; against an older mediator the method is missing and
// the call errors rather than silently returning iconless tabs.
func (c *TabCtl) ListTabs() ([]TabInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbusCallTimeout)
	defer cancel()
	var tabs []TabInfo
	err := c.obj().CallWithContext(ctx, dbusInterface+".ListTabsWithIcons", 0).Store(&tabs)
	return tabs, err
}

func (c *TabCtl) ActivateTab(tabID int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbusCallTimeout)
	defer cancel()
	return c.obj().CallWithContext(ctx, dbusInterface+".ActivateTab", 0, tabID, true).Err
}

// SubscribeTabsUpdated registers interest in tabctl's TabsUpdated signal
// (mediator >= 2.1). On older mediators the signal never arrives and the
// daemon's fallback poll covers refreshes instead. Returns the signal channel.
func (c *TabCtl) SubscribeTabsUpdated() (chan *dbus.Signal, error) {
	if err := c.conn.AddMatchSignal(
		dbus.WithMatchInterface(dbusInterface),
		dbus.WithMatchMember("TabsUpdated"),
	); err != nil {
		return nil, err
	}
	ch := make(chan *dbus.Signal, 16)
	c.conn.Signal(ch)
	return ch, nil
}
