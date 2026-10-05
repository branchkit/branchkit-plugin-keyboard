# BranchKit Keyboard & Mouse

Presses keys, clicks and scrolls for you, and names every key. A plugin for
[BranchKit](https://github.com/branchkit), an accessibility plugin platform
for the desktop. MIT licensed.

This plugin owns the `input.*` actions. When another plugin's command
dispatches `input.shortcut_by_name` or `input.type`, this process executes it,
so it is the integration target for anything that needs to press a key, move
the caret, click, scroll, or use the clipboard. It also publishes the spoken
names of keys and modifiers, so a command can capture "command shift t".

BranchKit is pre-launch: the app is not publicly released yet.

## What you can say

Nothing directly: this plugin declares no phrases of its own. Other plugins'
commands dispatch its actions; the bundled voice plugin's `press <modifiers>
<key>`, `undo`, `copy that`, `scroll down` and `go end`, for example, all land
here. Any command can also be bound to a key in Settings → Keybinds.

## Actions

| Action | Does |
|---|---|
| `input.type` | Type text at the cursor |
| `input.key_by_name` | Press a key by name (`return`, `f5`), with optional modifiers; `strategy: text` types the key's character instead |
| `input.key` | Press a key by raw keycode |
| `input.shortcut_by_name` | Press a key by name with modifiers (`primary`, `shift`, …) |
| `input.shortcut` | Press a raw keycode with modifiers |
| `input.navigate` | Move the caret to `document_start`, `document_end`, `line_start` or `line_end`, using that OS's own shortcut |
| `input.raw_key` | Send one key event: `click`, `press` or `release` |
| `input.click` | Click `left`, `right` or `middle` |
| `input.mouse_down` / `input.mouse_up` | Press or release a mouse button |
| `input.scroll` | Scroll `up`, `down`, `left` or `right`, by `line` or `pixel` |
| `input.move` | Move the pointer to a screen position |
| `input.clipboard` | `copy`, `paste`, or `set` the clipboard to given text |

`key`, `key_by_name`, `shortcut` and `shortcut_by_name` also have `hold` and
`repeat` modes: bound to a held trigger, the key stays down for as long as the
trigger does, or repeats at the system's key-repeat delay and rate.

## Collections

- `keys` and `modifiers`: spoken key and modifier names (`page down`,
  `backspace`, `command`, `alt`), seeded from `data/keys.json` and
  `data/modifiers.json`. Both feed the matcher as named entities, so other
  plugins' commands can capture `<keys>` and `<modifiers>`.
- `layout_characters`: what each key types on the active keyboard layout,
  re-read when the layout changes.
- `plugin.keyboard.overrides`: legacy, read only (see below).

Key names themselves come from the platform's `_platform.key_names` registry,
which this plugin reads and enriches; it does not own them.

## Settings

**Keys** tab: every key name, its keycode, and the character it types on your
current layout.

Global hotkeys are not this plugin's: the platform owns the hotkey table, you
edit it in Settings → Keybinds, and plugins contribute defaults under
`collection_data["_platform.bindings"]` in their manifest. Hotkeys used to be
configured here; if edits saved that way are still in
`plugin.keyboard.overrides`, the plugin says so at start, so they can be
re-created in Settings → Keybinds.

## Permissions

| Privilege | Why |
|---|---|
| `input` | Every action here synthesises keyboard, mouse or clipboard input; it also reads the keyboard layout and key-repeat timing |

On macOS, BranchKit itself also needs the Accessibility permission to send
input. No network: the manifest declares no hosts, so the sandbox gives it
none.

## Platform support

Every action works on macOS, Linux and Windows; the platform implements the
input underneath on each. Where an OS has its own convention the plugin
follows it: `input.navigate` uses Command and an arrow on macOS, and Home and
End (with Control for the document) on Linux and Windows. Hold-to-repeat
classifies modifiers by key name, not keycode, because keycodes differ per
OS.

## Reading this as an example

This is a **reference implementation, not a tutorial.** It is a real shipped
plugin, so it carries the things real plugins carry: workarounds for OS
behavior, comments about bugs that took a day to find, and decisions that only
make sense against a specific failure. Read it to see how the platform is
actually used at scale.

Worth copying: each action has its own typed handler, registered with a
`Handle<Action>` function that
[branchkit-gen](https://github.com/branchkit/branchkit-gen) generates from
`plugin.json` into `src/actions_gen.go`, and every platform call goes through
the SDK's typed wrappers (`plugin.InputPressKey(...)`,
`plugin.InputTypeText(...)`).

For idiom, the shape a new plugin should start from, read
[branchkit-plugin-helloworld-go](https://github.com/branchkit/branchkit-plugin-helloworld-go)
or scaffold one with `branchkit-cli dev init`. The teaching plugin is
[snippets](https://github.com/branchkit/branchkit-plugin-snippets).

## Build

Go 1.24, [plugin-sdk-go](https://github.com/branchkit/plugin-sdk-go). The
settings tab is a [templ](https://templ.guide) template; the generated Go is
committed, so `templ` is needed only when you edit `src/keys.templ`.

```bash
cd src && go build -o ../keyboard-plugin . && go test ./...
```

`branchkit-cli dev build` runs the manifest's own `dev.build` recipe
(`templ generate`, then `go build`). Install into a running BranchKit:

```bash
branchkit-cli plugin install . --build
```

## License

MIT. See [LICENSE](LICENSE).
