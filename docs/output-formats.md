# Output formats

The JSON the snapshot file and `tabstrip list` hold, for anyone writing a bar or a switcher against them.

## Snapshot

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

## `tabstrip list`

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
