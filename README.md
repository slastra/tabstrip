# tabstrip

A browser tab strip for [waybar](https://github.com/Alexays/Waybar), fed by
[tabctl](https://github.com/slastra/tabctl). It shows the tabs of every browser
window on the current workspace as clickable chips. Each chip carries the site
favicon. Click a chip to activate the tab. Scroll to cycle.

The strip is scoped to the workspace, not to the focused window. Every chip is
therefore a tab you can currently see, and the strip stays populated while a
terminal or an editor has focus. Windows are ordered by screen position. Tabs
are ordered by tab-bar position.

Built for Hyprland. One Go binary, plus `resvg` for SVG favicons.

## How it works

```
browser extension → tabctl-mediator → D-Bus → tabstrip daemon → snapshot JSON → waybar
```

The daemon listens for tabctl's `TabsUpdated` D-Bus signal and for Hyprland
window events. It then rewrites the snapshot file and signals waybar with
`SIGRTMIN+8`. If the D-Bus signal is unavailable, the daemon polls every 1.5s.

Tab changes reach the bar in about half a second. Most of that is deliberate
debounce.

## Requirements

- [tabctl](https://github.com/slastra/tabctl) 2.2.0 or newer, with its browser
  extension installed. Run `tabctl list` to confirm it works. Both halves must
  be 2.2.0: the strip reads favicons through `ListTabsWithIcons`, and the
  extension supplies `favIconUrl`. Run `tabctl status` to see both versions.
- waybar
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
tabstrip daemon    run persistently, spawned by a hidden waybar module
tabstrip snapshot  one-shot refresh (debugging; default)
tabstrip goto N       activate the Nth displayed tab
tabstrip next         cycle to the next tab
tabstrip prev         cycle to the previous tab
tabstrip list         all tabs across every browser window, as JSON —
                      id, title, chip path, windowId, active, browser.
                      Unlike the daemon's snapshot (deliberately filtered
                      to the focused workspace), this is the global view,
                      for building a tab switcher.
tabstrip activate ID  activate a tab anywhere by its "<browser>.<win>.<tab>"
                      id, routed to the mediator that owns it
```

## Waybar wiring

A hidden driver module runs the daemon. An `image` module renders the chips.

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
variables, set on the waybar `exec` line that already starts the daemon.

| Variable | Default | Purpose |
| --- | --- | --- |
| `TABSTRIP_MAX_TABS` | 16 | chips to render before truncating |
| `TABSTRIP_ICON_DIR` | `$XDG_CACHE_HOME/tabctl/favicons` | favicon cache location |

```jsonc
"custom/fftabsdrv": { "exec": "TABSTRIP_MAX_TABS=24 tabstrip daemon", ... }
```

How many chips fit depends on your screen width, so raise the cap if the strip
stops short of the tabs you have open. Tabs past the cap are dropped silently.

## Files

| Path | Purpose |
| --- | --- |
| `$XDG_RUNTIME_DIR/waybar-fftabs.json` | snapshot the chips render from |
| `$XDG_RUNTIME_DIR/waybar-fftabs.pid` | daemon pidfile, newer instances replace older ones |
| `$XDG_CACHE_HOME/tabctl/favicons/` | favicon chips, written by the daemon, readable by other tools |

## License

MIT
