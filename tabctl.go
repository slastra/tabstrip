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

// TabInfo mirrors tabctl's D-Bus wire struct (a(iissibb)).
type TabInfo struct {
	WindowID int32
	TabID    int32
	Title    string
	URL      string
	Index    int32
	Active   bool
	Pinned   bool
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

func (c *TabCtl) ListTabs() ([]TabInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbusCallTimeout)
	defer cancel()
	var tabs []TabInfo
	err := c.obj().CallWithContext(ctx, dbusInterface+".ListTabs", 0).Store(&tabs)
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
