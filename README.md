# tabstrip

A browser tab strip for Wayland status bars, fed by
[tabctl](https://github.com/slastra/tabctl). It shows the tabs of every browser
window on the current workspace as clickable chips. Each chip carries the site
favicon. Click a chip to activate the tab. Scroll to cycle.

tabstrip does the work and the bar only draws. The daemon writes a small JSON
snapshot, and the bar calls back into the same binary on click and scroll. Any
bar that can watch a file and run a command can host it. Wiring is given below
for [waybar](https://github.com/Alexays/Waybar) and
[Quickshell](https://quickshell.org).

The strip is scoped to the workspace, not to the focused window. Every chip is
therefore a tab you can currently see, and the strip stays populated while a
terminal or an editor has focus. Windows are ordered by screen position. Tabs
are ordered by tab-bar position.

Built for Hyprland. One Go binary, plus `resvg` for SVG favicons.

## How it works

```
browser extension → tabctl-mediator → D-Bus → tabstrip daemon → snapshot JSON → bar
```

The daemon listens for tabctl's `TabsUpdated` D-Bus signal and for Hyprland
window events. It then rewrites the snapshot file atomically (write, then
rename) and signals any running waybar with `SIGRTMIN+8`. Bars that watch the
file pick up the rename and need no signal. If the D-Bus signal is
unavailable, the daemon polls every 1.5s.

Tab changes reach the bar in about half a second. Most of that is deliberate
debounce.

## Requirements

- [tabctl](https://github.com/slastra/tabctl) 2.2.0 or newer, with its browser
  extension installed. Run `tabctl list` to confirm it works. Both halves must
  be 2.2.0: the strip reads favicons through `ListTabsWithIcons`, and the
  extension supplies `favIconUrl`. Run `tabctl status` to see both versions.
- A bar to draw it: waybar, Quickshell, or anything that can watch a file.
- Hyprland. Workspace and window queries use its IPC sockets.
- [resvg](https://github.com/linebender/resvg), to rasterise SVG favicons.
  Optional: without it SVG icons fall back to the browser chip and everything
  else still works. Go's image decoders cover the raster formats in-process.
  On Arch, `pacman -S resvg`.

## Install

```sh
go install github.com/slastra/tabstrip@latest
```

Or clone the repository and run `go build`.

## Usage

```
tabstrip daemon       run persistently and keep the snapshot current
tabstrip snapshot     one-shot refresh (debugging; default)
tabstrip goto N       activate the Nth displayed tab
tabstrip next         cycle to the next tab
tabstrip prev         cycle to the previous tab
tabstrip list         every tab in every browser window, as JSON
tabstrip activate ID  activate a tab anywhere by its "<browser>.<win>.<tab>"
                      id, and follow it to its workspace
```

Only one daemon runs at a time. A new one terminates the old one through the
pidfile, so restarting the bar or the session never leaves two writing the
snapshot.

`list` is the global view. The snapshot is deliberately limited to the current
workspace, because the strip shows what you can see. `list` covers every
browser window on every workspace, for building a tab switcher, and `activate`
takes you to whichever window owns the tab.

## Output formats

The snapshot at `$XDG_RUNTIME_DIR/waybar-fftabs.json` holds the chips in
display order:

```json
{
  "tabs": [
    {
      "id": "firefox.220.18",
      "label": "2 slastra/tabstrip: Browser tab strip for…",
      "icon": "/home/you/.cache/tabctl/favicons/0b7c33a8e1510a8a-processed.png",
      "active": true
    }
  ]
}
```

- `label` is the cleaned tab title with its slot number in front. It is
  HTML-escaped, because waybar renders it as Pango markup.
- `icon` already reflects focus. It points at the dimmed variant for tabs in
  windows you are not in, so a bar should draw it as given and not dim it
  again.
- `active` marks the chip to highlight.
- An empty `tabs` array means the strip should render nothing.

`tabstrip list` prints a bare array with one object per tab:

```json
[
  {
    "id": "firefox.1.1",
    "title": "Inbox - Mail",
    "icon": "/home/you/.cache/tabctl/favicons/0210caa4d6f4380c-processed.png",
    "windowId": 1,
    "active": false,
    "browser": "Firefox"
  }
]
```

Here `title` is the raw tab title and `icon` is always the bright chip. Pass
`id` to `tabstrip activate`.

## Running the daemon

Start the daemon once per session. The simplest place is the compositor's
autostart, which keeps it independent of whichever bar you run:

```lua
-- hyprland.lua
hl.on("hyprland.start", function()
	hl.exec_cmd("tabstrip daemon &")
end)
```

With a `hyprland.conf`, use `exec-once = tabstrip daemon`.

On startup the daemon prints one line, `{"text": ""}`, so it can also run as a
hidden waybar module. Launched any other way, that line is harmless. The daemon
tolerates a missing mediator: if tabctl is not up yet at login, the strip
renders empty and fills in once the mediator appears.

## Waybar wiring

Either start the daemon from autostart, or let waybar start it from a hidden
driver module. An `image` module renders the chips.

```jsonc
"custom/fftabsdrv": {
    "exec": "tabstrip daemon",
    "return-type": "json",
    "format": "{}",
    "tooltip": false
},
"image#fftabs": {
    "exec": "jq -c '[.tabs | to_entries[] | {path: .value.icon, marker: (if .value.active then \"active\" else \"inactive\" end), tooltip: .value.label, \"on-click\": (\"tabstrip goto \" + ((.key+1)|tostring))}]' $XDG_RUNTIME_DIR/waybar-fftabs.json",
    "signal": 8,
    "on-scroll-up": "tabstrip next",
    "on-scroll-down": "tabstrip prev"
}
```

The `image` module takes the chip count from the array, so per-tab slot modules
are not needed. Waybar's image module has no CSS class states. The daemon
therefore writes a dimmed copy beside each chip and selects between them.

Brightness marks the window you are in. Every tab of the focused browser window
is bright and every other window on the workspace is dimmed, so the strip shows
at a glance which chips belong to the window in front of you. When focus is on
something that is not a browser, the daemon falls back to marking each window's
active tab.

A text-only strip is still possible with `custom/` slot modules. Read
`.tabs[N].label` in each module and set a class from `.tabs[N].active`. Those
modules cannot show favicons.

## Quickshell wiring

Quickshell does not need the signal. A `FileView` with `watchChanges` sees the
daemon's rename and reloads. This is a minimal strip. Start the daemon from
autostart, as above.

```qml
import Quickshell
import Quickshell.Io
import QtQuick

Row {
    id: strip
    spacing: 8
    property var tabs: []

    FileView {
        path: Quickshell.env("XDG_RUNTIME_DIR") + "/waybar-fftabs.json"
        watchChanges: true
        onFileChanged: reload()
        onLoaded: {
            try {
                strip.tabs = JSON.parse(text()).tabs ?? []
            } catch (e) {
                // read landed mid-write; the next change event retries
            }
        }
    }

    Repeater {
        model: strip.tabs

        Image {
            required property var modelData
            required property int index
            source: "file://" + modelData.icon
            width: 24; height: 24

            MouseArea {
                anchors.fill: parent
                onClicked: Quickshell.execDetached(["tabstrip", "goto", String(index + 1)])
                onWheel: wheel => Quickshell.execDetached(
                    ["tabstrip", wheel.angleDelta.y > 0 ? "next" : "prev"])
            }
        }
    }
}
```

Tooltips come from `modelData.label`, and `modelData.active` drives any
highlight. A tab switcher runs `tabstrip list` through a `Process`, parses its
stdout, and passes the chosen `id` to `tabstrip activate`.

## Favicons

Chips use the favicon the browser already resolved. tabstrip does not guess one
from the domain and does not call a third-party icon service. Most favicons
arrive as inline `data:` URIs, so the common case makes no network request.

Decoding runs in-process. PNG, JPEG and GIF use the standard library. SVG is
rasterised at 256px and downsampled. ICO is parsed directly, because ICO
directory headers often disagree with their payloads. One favicon declared
256x256 entries that held 16x16 PNGs. tabstrip therefore ignores the declared
sizes, decodes every entry, and keeps the largest real one.

A tab with no usable favicon falls back to its browser icon, so rows stay
aligned. tabstrip records a failure for six hours instead of retrying it on
every tab event.

The daemon owns the cache and writes chips as tabs appear. Other tools can read
the same files. Each chip is named `<sha256(favIconUrl)[:16]>-processed.png`
and is 64x64 with rounded corners.

## Configuration

There is no config file. The two values worth changing are environment
variables, set wherever the daemon is started.

| Variable | Default | Purpose |
| --- | --- | --- |
| `TABSTRIP_MAX_TABS` | 16 | chips to render before truncating |
| `TABSTRIP_ICON_DIR` | `$XDG_CACHE_HOME/tabctl/favicons` | favicon cache location |

```sh
env TABSTRIP_MAX_TABS=24 tabstrip daemon
```

`goto`, `next` and `prev` rewrite the snapshot after acting, and `list` reads
chips from the cache, so they need the same values as the daemon. Set the
variables in the session environment rather than on the daemon's command line
alone. Otherwise a click briefly re-renders the strip with the defaults.

How many chips fit depends on your screen width, so raise the cap if the strip
stops short of the tabs you have open. Tabs past the cap are dropped silently.

## Files

| Path | Purpose |
| --- | --- |
| `$XDG_RUNTIME_DIR/waybar-fftabs.json` | snapshot the chips render from |
| `$XDG_RUNTIME_DIR/waybar-fftabs.pid` | daemon pidfile, newer instances replace older ones |
| `$XDG_CACHE_HOME/tabctl/favicons/` | favicon chips, written by the daemon, readable by other tools |

The runtime files keep their `waybar-` names from when waybar was the only
host. Bars depend on those paths, so they stay.

## License

MIT
