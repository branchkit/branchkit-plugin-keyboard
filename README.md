# BranchKit Keyboard & Mouse

Presses keys, clicks and scrolls for you, and names every key, for
[BranchKit](https://branchkit.dev). MIT licensed.

This plugin owns `input.*`. When another plugin dispatches `input.shortcut` or
`input.type`, this process executes it — so it is the integration target for
anything that needs to press a key, click, scroll, or touch the clipboard.

## What it provides

**Actions** (`input.*`): `type`, `key`, `key_by_name`, `shortcut`,
`shortcut_by_name`, `raw_key`, `click`, `scroll`, `move`, `mouse_down`,
`mouse_up`, `clipboard`. The `key*` and `shortcut*` families support `hold` and
`repeat` modes.

**Collections**: provides `keys` and `modifiers` (spoken key and modifier
names) and `layout_characters` (what each key types on the active layout).

**Settings tabs**: Keys.

Global hotkeys are not this plugin's: the platform owns the hotkey table.
Plugins contribute hotkeys under `collection_data["_platform.bindings"]`, and
you edit them in Settings → Keybinds. Hotkeys used to be configured here; if
edits saved that way are still in `plugin.keyboard.overrides`, the plugin
says so at start.

Requires the `input` privilege; on macOS, the Accessibility permission too.

## Reading this as an example

This is a **reference implementation, not a tutorial.** It is a real shipped
plugin, so it carries the things real plugins carry: workarounds for OS
behavior, comments about bugs that took a day to find, and decisions that only
make sense against a specific failure. Read it to see how the platform is
actually used at scale.

For idiom — the shape a new plugin should start from — read
[branchkit-plugin-helloworld-go](https://github.com/branchkit/branchkit-plugin-helloworld-go)
or scaffold one with `branchkit-cli dev init`. Those are curated to teach. This
is not.

## Build

```bash
cd src && go build -o ../keyboard-plugin .
```

Install into a running BranchKit:

```bash
branchkit-cli plugin install . --build
```

## Platform documentation

```bash
branchkit-cli docs sync
grep -rl "writers" "$(branchkit-cli docs path)"
```
