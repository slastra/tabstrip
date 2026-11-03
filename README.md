# tabstrip

Browser tab strip for waybar and Quickshell, fed by tabctl over D-Bus.

A browser tab strip for Wayland status bars, fed by
[tabctl](https://github.com/slastra/tabctl). It shows the tabs of every browser
window on the current workspace as clickable chips. Each chip carries the site
favicon. Click a chip to activate the tab. Scroll to cycle.

## Install

### Requirements

Built for Hyprland. One Go binary, plus `resvg` for SVG favicons.

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
- Go 1.26 or newer to build it, per `go.mod`.

### From source

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

### Running the daemon

Start the daemon once per session. With a compositor that starts
`graphical-session.target` (Hyprland does, through `hyprland-session.target`),
the bundled unit ties it to the session and restarts it if it fails:

```sh
cp systemd/tabstrip.service ~/.config/systemd/user/   # edit ExecStart to your install path
systemctl --user enable --now tabstrip
```

Otherwise use the compositor's autostart, which keeps it independent of
whichever bar you run:

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

### Wiring a bar

[docs/bars.md](docs/bars.md) has a working waybar module pair and a minimal
Quickshell strip. [docs/output-formats.md](docs/output-formats.md) describes the
snapshot and `tabstrip list` JSON for anything else.

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

## How it works

tabstrip does the work and the bar only draws. The daemon writes a small JSON
snapshot, and the bar calls back into the same binary on click and scroll. Any
bar that can watch a file and run a command can host it. Wiring is given in
[docs/bars.md](docs/bars.md) for [waybar](https://github.com/Alexays/Waybar) and
[Quickshell](https://quickshell.org).

The strip is scoped to the workspace, not to the focused window. Every chip is
therefore a tab you can currently see, and the strip stays populated while a
terminal or an editor has focus. Windows are ordered by screen position. Tabs
are ordered by tab-bar position.

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

### Favicons

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

## Development

```sh
go build
go test ./...
```

## License

MIT © 2026 Shaun Lastra. See [LICENSE](LICENSE).
