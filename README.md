# crtctl

[![CI](https://github.com/ImmortallKIng/crtctl/actions/workflows/ci.yml/badge.svg)](https://github.com/ImmortallKIng/crtctl/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/ImmortallKIng/crtctl)](https://github.com/ImmortallKIng/crtctl/releases/latest)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

**CRT monitors on modern Linux, done right.** It does what CRU (Custom Resolution Utility)
does on Windows, and also detects the monitor and the adapter for you, checks every mode
for safety, and lets you try any resolution in one command.

**English** · [Русский](README.ru.md) · [User guide](docs/GUIDE.md) · [Руководство](docs/GUIDE.ru.md)

```console
$ crtctl modes DP-2
DP-2 — HP Ergo 1600 (D2845A), 21" (HWP 2845)
Limits: horizontal 30–93.75 kHz, vertical 50–140 Hz, pixel clock up to 179.1 MHz

RESOLUTION  MAX HZ  KHZ    MHZ     TYPE  LIMITED BY
640x480     139.53  73.11  62.00   CVT   monitor vertical
800x600     139.84  91.45  99.50   CVT   monitor vertical
1024x768    113.91  93.75  130.50  CVT   monitor horizontal
1280x1024   86.89   93.75  163.50  CVT   monitor horizontal
1400x1050   83.89   92.70  175.75  CVT   monitor horizontal
1600x1200   65.89   82.29  177.75  CVT   adapter
```

## Why

Modern graphics cards have no analog output, so a CRT is connected through a DP→VGA or
HDMI→VGA adapter. The adapter hides the monitor from the system: you get 60 Hz, a few
resolutions, and sometimes a black screen. Getting 85–140 Hz back usually means
hand-writing EDID files, kernel parameters and initramfs hooks, and every distribution
does that differently.

`crtctl` does all of it for you:

- **Plug in and run `crtctl setup`.** It finds the CRT, recognises the adapter and its
  bandwidth, works out the safe limits, builds an EDID with the best modes, lets you check
  them, and installs everything so it survives reboots.
- **Try anything.** `crtctl try DP-2 1000x500@75` shows any mode right away and goes back
  by itself if you do not confirm, so a black screen fixes itself.
- **Every mode is checked** against the monitor (horizontal and vertical frequency) and the
  adapter (pixel clock). The table above shows the real maximum for each resolution and
  what limits it.
- **Works on popular distributions:** Fedora, Ubuntu, Debian, Mint, Pop!_OS, Arch, Manjaro,
  openSUSE, Silverblue/Bazzite, NixOS (prints the config), and anything with systemd.
- **AMD, Intel and NVIDIA.** KDE, GNOME, X11 desktops, Sway and Hyprland.
- **One static binary**, no dependencies. Interface in English, Русский, Español, Deutsch,
  Français.

## Install

Download the binary from [Releases](https://github.com/ImmortallKIng/crtctl/releases/latest):

```bash
curl -fLo crtctl https://github.com/ImmortallKIng/crtctl/releases/latest/download/crtctl-linux-amd64
sudo install -m755 crtctl /usr/local/bin/crtctl
```

(`crtctl-linux-arm64` for ARM boards. Checksums are in `SHA256SUMS`.)

Or build it with Go 1.22 or newer:

```bash
git clone https://github.com/ImmortallKIng/crtctl
cd crtctl
make
sudo make install          # → /usr/local/bin/crtctl
```

## Quick start

```bash
crtctl setup --dry-run     # see what it would do; changes nothing
crtctl setup               # do it; asks for sudo and asks before every step
```

Then play:

```bash
crtctl modes               # the best refresh rate for every resolution
crtctl try 1000x500@75     # look at any mode; reverts unless you press Enter
crtctl doctor              # what is wrong and the exact command that fixes it
```

If you have more than one monitor, give the output name (`DP-2`, `HDMI-A-1`, …), which
`crtctl info` shows.

The [user guide](docs/GUIDE.md) walks through everything step by step: adapters, the
setup wizard, experiments, monitor profiles, NVIDIA, Secure Boot, troubleshooting, and how
to recover from a black screen.

## Commands

| Command | What it does | root |
|---|---|---|
| `setup [OUTPUT]` | detect everything and set it up step by step | asks by itself |
| `try [OUTPUT] WxH@Hz` | show any mode right now, with automatic undo | no |
| `info [OUTPUT]` | monitor, EDID, profile, limits, adapter, current mode | optional |
| `doctor [OUTPUT]` | problems and the command that fixes each one | optional |
| `modes [OUTPUT]` | highest safe refresh rate for every resolution | no |
| `calc [OUTPUT] WxH@Hz` | exact DMT / CVT / GTF timings and the limit check | no |
| `edid read\|show\|build` | read an EDID over DDC, decode it, build your own | no |
| `apply OUTPUT FILE` | install an EDID: until reboot (`--test`) or permanently | yes |
| `reprobe OUTPUT` | make the driver re-read the EDID without replugging | yes |
| `link show\|force\|remove` | keep the DisplayPort link at full speed (amdgpu) | yes |
| `remove OUTPUT` | undo everything crtctl installed | yes |
| `kde list\|add\|best\|remove\|set` | custom modes in KDE Plasma 6.6+ | no |
| `profiles [ID]` | monitor profiles; `--new ID` makes one from a datasheet | no |

`crtctl help COMMAND` lists every option. The language follows your locale; change it with
`--lang en|ru|es|de|fr` or `CRTCTL_LANG`.

## Supported systems

| | |
|---|---|
| **Distributions** | dracut, mkinitcpio, initramfs-tools, rpm-ostree; grubby, GRUB, systemd-boot, kernelstub; a systemd service as a fallback; NixOS gets printed config |
| **Drivers** | amdgpu, i915/xe, nouveau, radeon; NVIDIA with `nvidia-drm.modeset=1` (on X11 crtctl also configures the X driver) |
| **Adapters** | DP→VGA (limit read from the adapter's registers), HDMI→VGA and DVI-D→VGA (165 MHz), native VGA and DVI-I |
| **Desktops for `try`** | KDE Plasma, any X11 desktop, Sway, Hyprland; GNOME and others through a temporary EDID |

The full table of what works where is in the [guide](docs/GUIDE.md#11-what-works-where).

## Is it safe?

A CRT can be damaged by frequencies far outside its range, and some old monitors have no
protection. crtctl is careful:

- it refuses modes outside the monitor and adapter limits unless you add `--force`;
- `try` and `kde set` go back by themselves unless you press Enter;
- `setup` installs nothing unless you answer "yes" within 60 seconds after looking at the
  new modes;
- the installed EDID is tied to the output, and crtctl warns you when another monitor is
  plugged into it;
- `crtctl remove OUTPUT` undoes everything, and `/etc/default/grub` and boot entries are
  backed up before the first change.

## Status

Version 0.3. What has been tested:

- **On real hardware:** Fedora 44, AMD RX 9070 XT (amdgpu), KDE Plasma Wayland, an
  unbranded DP→VGA adapter and an HP Ergo 1600.
- **With the real tools of each distribution** (in containers): Ubuntu 24.04, Debian 13,
  Arch, Fedora 43 and openSUSE Tumbleweed. The EDID ends up inside the real initramfs, the
  kernel parameters are right, and `remove` cleans everything up.
- **Only with tests so far:** NVIDIA, Intel, HDMI→VGA adapters, X11, Sway, Hyprland,
  GNOME, Silverblue, Pop!_OS, systemd-boot.

If you have any of these, please
[open an issue](https://github.com/ImmortallKIng/crtctl/issues/new/choose) and paste the
output of `crtctl doctor`, whether it works or not. Reports of working setups help as much
as bug reports.

Not supported: interlaced modes (15 kHz arcade monitors, TVs) and DP link forcing on drivers
other than amdgpu.

## Development

```bash
make test        # go vet and all tests
make release     # static binaries for amd64 and arm64 in dist/
```

The code is plain Go with no dependencies outside the standard library. The packages under
`internal/` handle EDID (`edid`), timing formulas (`timing`), DDC and DisplayPort registers
(`ddc`, `dpcd`), KMS (`drm`), initramfs and boot loaders (`boot`), desktop backends
(`display`), installation (`system`) and translations (`i18n`). Every user-visible string
goes through `i18n`, and the tests fail if a translation is missing.

To publish a release, set `version` in `main.go`, then push a matching tag (`v0.3.0`).
GitHub Actions builds the binaries and attaches them to the release.

## License

[GPL-3.0](LICENSE). Copyright © 2026 ImmortallKIng.
