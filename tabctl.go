package main

import (
	"context"
	"fmt"
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
