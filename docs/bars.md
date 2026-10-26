# Bar wiring

How to host the strip in waybar and in Quickshell. The [README](../README.md#usage) covers starting the daemon.

## Waybar

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

## Quickshell

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
