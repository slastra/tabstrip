# tabstrip

A browser tab strip for [waybar](https://github.com/Alexays/Waybar), fed by [tabctl](https://github.com/slastra/tabctl). Shows the focused Firefox window's tabs as clickable chips in a bar. Click to activate, scroll to cycle. When Firefox isn't focused the strip is empty, so a chip click can never activate a tab in a window other than the one you're looking at.

Built for Hyprland. One small Go binary, no runtime dependencies beyond tabctl itself.

## How it works

```
browser extension → tabctl-mediator → D-Bus → tabstrip daemon → snapshot JSON → waybar slots
```

The daemon listens for tabctl's `TabsUpdated` D-Bus signal (tabctl >= 2.1) and Hyprland's window events, then rewrites a snapshot file and pokes waybar with `SIGRTMIN+8`. Waybar renders up to 10 static `custom/` slot modules from that snapshot. On older tabctl versions without the signal, a 1.5s fallback poll covers changes.

Tab updates land in the bar roughly half a second after they happen in the browser, most of that being deliberate debounce.

## Requirements

- [tabctl](https://github.com/slastra/tabctl) with its browser extension installed and working (`tabctl list` shows your tabs)
- waybar
- Hyprland (window focus tracking uses its IPC sockets)

## Install

```sh
go install github.com/slastra/tabstrip@latest
```

Or clone and `go build`.

## Usage

```
tabstrip daemon    run persistently, spawned by a hidden waybar module
tabstrip snapshot  one-shot refresh (debugging; default)
tabstrip goto N    activate the Nth displayed tab
tabstrip next      cycle to the next tab
tabstrip prev      cycle to the previous tab
```

## Waybar wiring

A hidden driver module owns the daemon, and each visible slot renders one tab from the snapshot:

```jsonc
"custom/fftabsdrv": {
    "exec": "tabstrip daemon",
    "return-type": "json",
    "format": "{}",
    "tooltip": false
},
"custom/fftab1": {
    "exec": "jq -cr '.tabs[0] // empty | {text: .label, class: (if .active then \"active\" else \"\" end)}' $XDG_RUNTIME_DIR/waybar-fftabs.json",
    "return-type": "json",
    "signal": 8,
    "on-click": "tabstrip goto 1",
    "on-scroll-up": "tabstrip next",
    "on-scroll-down": "tabstrip prev"
},
// ...fftab2 through fftab10, same shape, next array index and goto number
```

Style the chips via `#custom-fftab1` etc. in `style.css`, with `.active` for the focused tab.

## Files

| Path | Purpose |
| --- | --- |
| `$XDG_RUNTIME_DIR/waybar-fftabs.json` | snapshot the slots render from |
| `$XDG_RUNTIME_DIR/waybar-fftabs.pid` | daemon pidfile, newer instances replace older ones |

## License

MIT
