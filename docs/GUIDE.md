# crtctl user guide

**English** · [Русский](GUIDE.ru.md) · [README](../README.md)

This guide takes you from a CRT plugged into an adapter to a monitor running at 85–140 Hz,
and then covers experiments, manual control, special cases and troubleshooting. The sample
output comes from a real system (an HP Ergo 1600 on a DP→VGA adapter), shortened where long.

- [1. Before you start](#1-before-you-start)
- [2. Install crtctl](#2-install-crtctl)
- [3. First setup: `crtctl setup`](#3-first-setup-crtctl-setup)
- [4. Experiments: `crtctl try`](#4-experiments-crtctl-try)
- [5. What is possible: `modes` and `calc`](#5-what-is-possible-modes-and-calc)
- [6. Monitor profiles](#6-monitor-profiles)
- [7. Manual control: EDID step by step](#7-manual-control-edid-step-by-step)
- [8. KDE Plasma custom modes](#8-kde-plasma-custom-modes)
- [9. DP→VGA adapter goes black: link forcing](#9-dpvga-adapter-goes-black-link-forcing)
- [10. Special cases: NVIDIA, Secure Boot, immutable systems, NixOS](#10-special-cases)
- [11. What works where](#11-what-works-where)
- [12. Troubleshooting](#12-troubleshooting)
- [13. Black screen after a reboot: recovery](#13-black-screen-after-a-reboot-recovery)
- [14. Undo everything](#14-undo-everything)
- [15. What crtctl changes on the system](#15-what-crtctl-changes-on-the-system)
- [Glossary](#glossary)

---

## 1. Before you start

### What you need

- A CRT monitor and its model name (written on the back). The datasheet helps but is not
  required.
- A way to connect it:

  | Connection | Notes |
  |---|---|
  | **DP→VGA adapter** | The best choice for a modern card. Good adapters reach 200–360 MHz pixel clock, cheap ones 160–180 MHz. crtctl reads the adapter's limit from its registers. |
  | **HDMI→VGA adapter** | Usually limited to 165 MHz (HDMI single link). Fine for 1024×768@100 or 1280×1024@75. |
  | **DVI-I or native VGA** | Older cards; the card's own DAC, usually 400 MHz. Nothing limits you but the monitor. |
  | **HDMI→AV (composite/SCART) converter** | That is for TVs, not CRT monitors. crtctl recognises it and leaves it alone. |

- Linux with a kernel from the last few years, and `sudo` rights for the permanent setup.

### Words used in this guide

You only need three numbers to understand CRT modes:

- **Horizontal frequency (kHz):** how many lines per second the monitor draws. This is the
  real limit of a CRT; a 21" monitor typically does 30–95 kHz, a 17" one 30–70 kHz.
- **Vertical refresh (Hz):** how many frames per second. Below 60 Hz a CRT flickers
  visibly, 85 Hz is comfortable, and many monitors go up to 120–160 Hz.
- **Pixel clock (MHz):** how many pixels per second. This is what the **adapter** limits.

A mode fits when all three fit. The more lines (a higher resolution), the lower the refresh
rate the same horizontal frequency allows. That is why one monitor does 800×600@140 but only
1600×1200@66.

The **output** (or connector) is the port on the graphics card: `DP-2`, `HDMI-A-1`, `VGA-1`.
`crtctl info` shows them all.

## 2. Install crtctl

The easiest way is the ready binary from
[Releases](https://github.com/ImmortallKIng/crtctl/releases/latest):

```bash
curl -fLo crtctl https://github.com/ImmortallKIng/crtctl/releases/latest/download/crtctl-linux-amd64
sudo install -m755 crtctl /usr/local/bin/crtctl
crtctl --version
```

It is one static file with no dependencies, so it runs on any distribution. For ARM boards
take `crtctl-linux-arm64`.

To build it from source you need Go 1.22 or newer (`golang` package):

```bash
git clone https://github.com/ImmortallKIng/crtctl
cd crtctl
make
sudo make install
```

Optional programs that crtctl uses when they are present: `kscreen-doctor` (KDE),
`xrandr` (X11), `swaymsg`, `hyprctl`, `edid-decode`. You do not need to install anything
extra; crtctl tells you when something is missing.

**Language.** The interface follows your system language (English, Russian, Spanish, German,
French). To change it: `crtctl --lang de info`, or `export CRTCTL_LANG=fr`.

## 3. First setup: `crtctl setup`

Plug the monitor in and turn it on. Then first see what crtctl would do, without changing
anything:

```bash
crtctl setup --dry-run
```

If that looks right, run it for real:

```bash
crtctl setup
```

You do not need to type `sudo`: crtctl asks for your password when it needs it. It asks
before every step, and Enter accepts the suggested answer (shown in capitals: `[Y/n]`).

### Step 1: finding the monitor

```text
1. Looking for the CRT monitor...
   Output: DP-2 (amdgpu)
   Monitor: HP Ergo 1600 (D2845A), 21" (HWP 2845)
   Adapter: DP 1.1 → VGA, 2 lanes × HBR 2.7 Gbit/s, downspread, no vendor ID; limit about 179.1 MHz
```

crtctl looks at every connected output and picks the one with an analog monitor or a VGA
adapter. If there are several, it asks which one. To choose yourself: `crtctl setup DP-2`.

For a DisplayPort adapter it reads the adapter's registers (DPCD) and works out its
bandwidth. For HDMI→VGA it assumes 165 MHz.

### Step 2: the limits

```text
2. Working out the safe limits...
   ✓ Profile hp-ergo-1600 (matched by EDID)
   Limits: horizontal 30–93.75 kHz, vertical 50–140 Hz, pixel clock up to 179.1 MHz
```

The limits come from, in order:

1. a profile you name with `--profile ID`;
2. a profile that matches the monitor's EDID (built-in or yours, see
   [section 6](#6-monitor-profiles));
3. the frequency ranges the monitor reports about itself;
4. the modes the monitor lists in its EDID (always safe, but usually conservative).

If the monitor is not in the list, crtctl **creates a profile for it** from what the monitor
reports, and tells you the file name. If you have the datasheet, you can raise the limits in
that file later and run `setup` again.

If the monitor reports nothing at all (very old models, or adapters that do not pass DDC
through), crtctl asks you to pick a typical profile ("17-inch CRT, 30–70 kHz") or to type
the maximum horizontal frequency from the datasheet.

### Step 3: the modes

```text
3. Choosing the modes...
 ! 1280x1024     85.02 Hz   91.15 kHz  157.50 MHz  DTD DMT 0x25   ✓
   640x480      139.53 Hz   73.11 kHz   62.00 MHz  DTD CVT        ✓
   800x600      139.84 Hz   91.45 kHz   99.50 MHz  DTD CVT        ✓
   1024x768     113.91 Hz   93.75 kHz  130.50 MHz  DTD CVT        ✓
   ...
   1600x1200     65.89 Hz   82.29 kHz  177.75 MHz  DTD CVT        ✓
Use these modes? [Y/n]
```

For every standard resolution crtctl takes the highest refresh rate that fits, plus the usual
VESA modes at 60, 75 and 85 Hz. The mode marked `!` becomes the default: the largest mode at
85 Hz or more that the monitor itself declares, or else the largest resolution that holds
85 Hz with a margin. Rates below 60 Hz are left out.

If you want different modes, answer `n` and see [section 7](#7-manual-control-edid-step-by-step).

### Step 4: the DisplayPort link (DP adapters only)

```text
4. DisplayPort link
   Many DP→VGA adapters go black on low resolutions because the driver slows the link down. Keeping the link at the adapter's maximum avoids that and is harmless.
Keep the DP link at the maximum? [Y/n]
```

Say yes. It is harmless, and it prevents the most common black-screen problem with cheap
adapters. Details are in [section 9](#9-dpvga-adapter-goes-black-link-forcing).

### Step 5: the check

```text
5. Checking before installing...
   ✓ The new modes are active until reboot (amdgpu re-detection (trigger_hotplug=1)).
   Open the display settings and try a few of them, or in another terminal: crtctl try DP-2 1024x768@85
Does everything look right? Install it permanently? [y/N, 60 s]
```

The new modes are now loaded, but only until reboot. Open the display settings, pick a few
modes and look at the picture. Then come back and type `y`.

**If the screen goes black or the monitor shows "out of range", just wait.** Without a "yes"
within 60 seconds, crtctl puts the previous EDID back and installs nothing.

### Step 6: permanent install

crtctl puts the EDID file where the kernel will find it at boot, adds it to the initramfs and
adds the `drm.edid_firmware=` kernel parameter. It recognises your initramfs tool and boot
loader by itself (see [what works where](#11-what-works-where)).

```text
✓ Done: DP-2 is set up. The modes stay after a reboot.
  Experiment with any mode: crtctl try DP-2 1000x500@75
  Check later: crtctl doctor
  Undo: sudo crtctl remove DP-2
```

Reboot once to check that the modes are still there, then choose the mode you like in the
display settings.

### Useful options

| Option | Meaning |
|---|---|
| `--dry-run` | only show what would be done |
| `--yes` | accept every suggested answer; the check still waits for your "yes" (add `--no-test` for a fully unattended run) |
| `--no-test` | skip the check (when you already know the modes work) |
| `--profile ID` | use this monitor profile |
| `--hmax 85 --vmax 120` | override the limits by hand |
| `--pclk 200` | override the adapter limit |
| `--method service` | install through a systemd service instead of a kernel parameter |
| `--current-kernel` | rebuild the initramfs only for the running kernel (faster) |

## 4. Experiments: `crtctl try`

`try` shows any mode right now, without installing anything:

```bash
crtctl try 1000x500@75
crtctl try DP-2 640x480@120
crtctl try DP-2 1280x960@85 --seconds 30
```

```text
Trying 1000x500      74.68 Hz   39.21 kHz   50.50 MHz (2:1), CVT
Switching through KDE (kscreen-doctor)...
✓ The monitor now gets: 1000x500      74.68 Hz   39.21 kHz   50.50 MHz  CVT
Press Enter within 15 seconds to keep it; otherwise the previous mode comes back.
```

- The mode is checked against the limits first. If it does not fit, crtctl refuses and says
  why. `--force` skips the check; use it only if you know the monitor can take it.
- After switching, crtctl reads the mode back from the kernel, so you see what the monitor
  really gets.
- **If you do not press Enter, the previous mode comes back** after 15 seconds (or
  `--seconds N`). A black screen fixes itself.
- A kept mode lasts until you change it or log out. To keep it for good, put it in the EDID:
  `crtctl edid build DP-2 1000x500@75 --best`, then `sudo crtctl apply` (see section 7).

### Exact timings

By default crtctl uses the VESA DMT timings when the mode is a standard one, and CVT
otherwise. You can choose:

```bash
crtctl try 1024x768@100 --kind gtf
crtctl try --modeline "157.5 1280 1344 1504 1728 1024 1025 1028 1072 +hsync +vsync"
```

A modeline is the same format as in `xrandr` and old `xorg.conf` files: pixel clock in MHz,
four horizontal numbers, four vertical numbers, sync polarity.

### Desktops

| Desktop | How `try` switches | Notes |
|---|---|---|
| KDE Plasma (Wayland) | `kscreen-doctor` | KDE builds modes only with CVT. When you ask for DMT or GTF, crtctl says so. |
| Any desktop on X11 | `xrandr` | exact timings |
| Sway | `swaymsg` | exact timings |
| Hyprland | `hyprctl` | exact timings |
| GNOME on Wayland and others | temporary EDID | `sudo crtctl try DP-2 1000x500@75 --via edid` |

On GNOME there is no way to add a mode from outside, so crtctl loads a temporary EDID that
contains it. The mode then appears in Settings → Displays; choose it there. When you are done,
press Enter in the terminal and the previous EDID comes back. This needs `sudo` and an open
debugfs (see [Secure Boot](#secure-boot-and-kernel-lockdown)).

Run `try` **without** `sudo` everywhere else: it has to talk to your desktop session.

## 5. What is possible: `modes` and `calc`

`modes` answers "how high can I go at each resolution?":

```text
$ crtctl modes DP-2
RESOLUTION  MAX HZ  KHZ    MHZ     TYPE  LIMITED BY          NOTES
640x480     139.53  73.11  62.00   CVT   monitor vertical
1024x768    113.91  93.75  130.50  CVT   monitor horizontal
1280x1024   86.89   93.75  163.50  CVT   monitor horizontal
1600x1200   65.89   82.29  177.75  CVT   adapter
1792x1344   52.93   73.47  177.50  CVT   adapter             below 60 Hz: visible flicker
1856x1392   —                                                does not fit at any refresh rate
```

The **LIMITED BY** column tells you what to upgrade:

- `monitor vertical` / `monitor horizontal`: the monitor is the limit. Nothing to gain.
- `adapter`: a faster adapter would give more. In the example a 180 MHz adapter holds
  1600×1200 at 66 Hz; a 230 MHz one would allow 85 Hz.

Options: `--wide` adds widescreen resolutions, `--kind gtf` uses another formula, `--kernel`
checks every mode the kernel offers right now.

`calc` shows the exact timings of one mode and checks it:

```text
$ crtctl calc DP-2 1000x500@75 --all
1000x500      74.68 Hz   39.21 kHz   50.50 MHz  [CVT]
  Modeline "1000x500_74.68" 50.50 1000 1048 1144 1288 500 503 513 525 -hsync +vsync
  ✓ within limits
1000x500      74.75 Hz   38.79 kHz   45.00 MHz  [CVT-RB]
  ⚠ reduced blanking is meant for LCDs; a CRT may not complete the beam retrace in time
```

Without an output name, `calc` just computes, with no checks.

## 6. Monitor profiles

A profile is the monitor's frequency ranges from its datasheet. crtctl has a few built in:

```text
$ crtctl profiles
ID                MONITOR                            KHZ       HZ
generic-crt-15in  Typical 15-inch CRT (30–54 kHz)    30–54     50–120
generic-crt-17in  Typical 17-inch CRT (30–70 kHz)    30–70     50–160
generic-crt-19in  Typical 19-inch CRT (30–96 kHz)    30–96     50–160
generic-crt-21in  Typical 21-inch CRT (30–110 kHz)   30–110    50–160
generic-vga-31k   Plain VGA monitor (31.5 kHz only)  31–32     50–75
hp-ergo-1600      HP Ergo 1600 (D2845A), 21"         30–93.75  50–140
lg-flatron-f700b  LG Flatron F700B, 17"              30–70     50–160
lg-flatron-f700p  LG Flatron F700P, 17"              30–96     50–160
```

You need your own profile when the monitor's EDID understates what it can do, or when it
has no EDID. Create a template:

```bash
crtctl profiles --new my-crt
```

It creates `~/.config/crtctl/monitors/my-crt.json`. Fill it in from the datasheet:

```json
{
  "id": "my-crt",
  "name": "My 19-inch CRT",
  "match": ["ABC 1234"],
  "hmin_khz": 30,
  "hmax_khz": 96,
  "vmin_hz": 50,
  "vmax_hz": 160,
  "max_res": "1600x1200@75",
  "notes": "Values from the datasheet"
}
```

| Field | Meaning |
|---|---|
| `hmin_khz`, `hmax_khz` | horizontal frequency range from the datasheet |
| `vmin_hz`, `vmax_hz` | vertical refresh range |
| `match` | vendor and model code: the first two words of the `Monitor:` line in `crtctl info` (for example `HWP 2845`). With it, crtctl picks the profile automatically. Leave it out if the monitor has no EDID and use `--profile my-crt`. |
| `bandwidth_mhz` | optional: the video amplifier bandwidth. Above it the picture gets soft, but nothing breaks, so it is not a hard limit. |
| `max_res`, `notes` | for you; shown by `crtctl profiles my-crt` |

Your profiles override built-in ones with the same `id`. Profiles created by `setup` live in
`/etc/crtctl/monitors/`.

**Use the datasheet, not a guess.** Going above the horizontal frequency limit can damage old
monitors that have no protection circuit.

If your monitor is not in the list and you have its datasheet, please send the profile in a
pull request.

## 7. Manual control: EDID step by step

`setup` does all of this for you. Use the individual commands if you want your own set of modes.

**Background.** The EDID is a small block of data in which the monitor describes itself and
lists its modes. Through an adapter the system often gets a poor EDID, or none. crtctl builds a
new one and makes the kernel use it instead of the real one.

```bash
# 1. Read the real EDID from the monitor (for a backup or to look at)
crtctl edid read DP-2                     # → edid-DP-2.bin

# 2. Decode an EDID: a file, or what the kernel uses for an output
crtctl edid show DP-2
crtctl edid show edid-DP-2.bin

# 3. Build your own. The first mode becomes the default one.
crtctl edid build DP-2 1280x1024@85 1000x500@75 --best    # → crtctl-DP-2.bin

# 4. Load it until reboot and look at the modes
sudo crtctl apply DP-2 crtctl-DP-2.bin --test

# 5. Install it permanently
sudo crtctl apply DP-2 crtctl-DP-2.bin
```

`edid build` copies the vendor, model and serial number from the monitor's real EDID, so the
desktop keeps recognising it and keeps its settings. It checks every mode against the limits
(`--force` builds anyway). An EDID holds up to 20 custom modes plus the standard VESA modes.

| `edid build` option | Meaning |
|---|---|
| `--best` | add the best refresh rate for every 4:3 and 5:4 resolution |
| `--kind cvt\|gtf` | timing formula for non-standard modes |
| `--name "My CRT"` | monitor name, up to 13 characters |
| `--base FILE` | copy the identity from this EDID file instead of the monitor |
| `--no-range` | do not store the frequency ranges |
| `-o FILE` | output file name |

`apply` checks that the EDID was built for the monitor that is connected now, and refuses
otherwise (`--force` installs anyway).

**`reprobe`** makes the driver read the EDID again, as if you replugged the cable:
`sudo crtctl reprobe DP-2`. `--hard` also allows a software replug (the screen blinks).

## 8. KDE Plasma custom modes

KDE Plasma 6.6 and newer can add modes by itself, with no EDID and no root. They last until
you remove them.

```bash
crtctl kde list DP-2              # the modes KDE has, checked against the limits
crtctl kde add DP-2 1152x864@100  # add a mode
crtctl kde best DP-2              # add the best rate for every 4:3/5:4 resolution
crtctl kde set DP-2 1152x864@100  # switch to it (goes back unless you press Enter)
crtctl kde remove DP-2 61 62      # remove modes by their number from "kde list"
crtctl kde remove DP-2 all
```

KDE computes these modes with CVT. For exact VESA timings, use the EDID way.

## 9. DP→VGA adapter goes black: link forcing

**Symptom:** high resolutions work, but at 640×480 or 800×600 the screen goes black, or the
picture disappears after the monitor wakes up.

**Cause:** for light modes the driver saves power by slowing down the DisplayPort link (fewer
lanes, a lower rate). Many cheap adapters cannot follow that.

**Fix** (amdgpu only):

```bash
sudo crtctl link show DP-2                  # what the adapter can do and what is in use now
sudo crtctl link force DP-2 --persistent    # keep the maximum, at every boot too
sudo crtctl link remove DP-2                # undo
```

`--lanes 2 --rate hbr` sets a configuration by hand. `--persistent` installs the systemd
service `crtctl-link-DP-2.service`. `setup` offers this step by itself.

## 10. Special cases

### NVIDIA

- The driver must run with kernel modesetting: the `nvidia-drm.modeset=1` kernel parameter.
  Recent drivers turn it on by default. `crtctl doctor` checks it.
- NVIDIA does not link outputs to their DDC bus in the system, so crtctl finds the bus itself.
- **X11:** the NVIDIA X driver reads the EDID by itself and ignores the kernel's replacement.
  `apply` and `setup` therefore also write
  `/etc/X11/xorg.conf.d/90-crtctl-nvidia.conf` with the `CustomEDID` option. Log out and back
  in after that. If you already have an NVIDIA `Device` section in your own config, crtctl
  does not touch it and prints the lines to add.
- **Wayland:** works like on other drivers.
- Driver 580.105.08 has a bug: it ignores custom EDIDs. Update to 580.119.02 or newer.
  `doctor` warns about it.
- DP link forcing is not available.

### Intel

i915 and xe work like amdgpu, except for DP link forcing.

### Secure Boot and kernel lockdown

With Secure Boot the kernel usually runs in **lockdown** mode and closes debugfs. Then:

- the **permanent install still works** (it uses a kernel parameter);
- the check in `setup`, `apply --test`, `try --via edid`, `reprobe` and `link` do not work.
  `setup` tells you so, and the modes appear after a reboot.

`crtctl doctor` shows whether lockdown is on.

### Immutable systems: Silverblue, Kinoite, Bazzite

`/usr` is read-only there, so the EDID goes to `/etc/crtctl/firmware`, and crtctl uses
`rpm-ostree initramfs-etc` and `rpm-ostree kargs`. A reboot is needed, as for any rpm-ostree
change.

### NixOS

crtctl does not change NixOS boot settings. It prints the lines to add to
`configuration.nix`, then you run `nixos-rebuild switch`.

### No systemd or an unknown boot loader

When crtctl does not recognise the boot loader, it uses a systemd service that loads the EDID
at boot (`--method service`; needs debugfs). Without systemd (Void, Artix, Alpine) it prints
the steps to do by hand.

## 11. What works where

**Distributions**

| Distribution | initramfs | kernel parameters |
|---|---|---|
| Fedora, RHEL, Alma, Rocky | dracut | grubby |
| Fedora Silverblue / Kinoite, Bazzite | rpm-ostree initramfs-etc | rpm-ostree kargs |
| Ubuntu, Debian, Mint, Zorin, elementary | initramfs-tools | GRUB drop-in `/etc/default/grub.d/99-crtctl.cfg` |
| Pop!_OS | initramfs-tools | kernelstub |
| Arch, Manjaro, EndeavourOS, CachyOS | mkinitcpio or dracut | GRUB or systemd-boot |
| openSUSE | dracut | GRUB 2 |
| anything else with systemd | — | boot service (`--method service`) |
| NixOS | — | prints the `configuration.nix` lines |

**Graphics drivers**

| Driver | EDID replacement | instant check (debugfs) | DP link forcing |
|---|---|---|---|
| amdgpu | ✓ | ✓ | ✓ |
| i915 / xe (Intel), nouveau, radeon | ✓ | ✓ | — |
| NVIDIA (proprietary and open) | ✓ with `nvidia-drm.modeset=1`; on X11 also `CustomEDID` | ✓ on Wayland | — |

**Adapters: how the pixel clock limit is found**

| Connection | Detected by | Limit |
|---|---|---|
| DP→VGA with readable registers | DisplayPort registers (DPCD) | the adapter's DAC limit if it reports one, otherwise the link bandwidth minus 0.5% for downspread |
| DP→VGA without data (e.g. NVIDIA) | analog EDID on a DP output | 165 MHz to be careful; override with `--pclk` |
| HDMI→VGA, DVI-D→VGA | analog EDID on a digital output | 165 MHz |
| native VGA, DVI-I | output type | the card's DAC, usually 400 MHz |
| HDMI→AV converter (TV) | digital EDID | not a CRT input; no limits applied |

## 12. Troubleshooting

Start with:

```bash
crtctl doctor          # without root
sudo crtctl doctor     # also checks the adapter and debugfs
```

It lists problems and the exact command that fixes each one.

**The monitor shows "out of range" or "frequency over".** The mode is above what the monitor
can take. If this happened in `try` or during the `setup` check, wait; it reverts by itself.
If it happens with an installed EDID, the profile limits are too high: lower `hmax_khz` in the
profile and run `setup` again.

**The screen is black only at low resolutions.** It is the DP adapter; see
[section 9](#9-dpvga-adapter-goes-black-link-forcing).

**The new modes do not appear after `setup`.** Reboot once. If they are still missing, run
`crtctl doctor`: it shows whether the kernel loaded the EDID and why not.

**Only 60 Hz and a few resolutions.** The adapter does not pass the monitor's EDID through, or
the EDID is minimal. Run `crtctl setup`; it will ask for a profile.

**`try` says "KDE builds modes with CVT only".** That is a KDE limitation. The CVT mode is
nearly the same; for exact DMT timings use `sudo crtctl try ... --via edid`.

**"name the connector, several are connected".** crtctl could not tell which monitor you mean
(none or more than one of them looks like a CRT). Add the output name:
`crtctl try DP-2 800x600@120`.

**The picture is soft at high resolutions.** That is the monitor's video bandwidth or the
adapter's DAC. Take a slightly lower resolution or refresh rate.

**I swapped the monitor on the same port.** An installed EDID belongs to the port. `doctor`
warns when another monitor is connected. Run `crtctl setup` again, or `sudo crtctl remove DP-2`.

**The image is shifted or the wrong size.** That is normal for a new mode on a CRT. Adjust it
with the monitor's own buttons; most monitors remember the setting for each mode.

**Reporting a bug.** [Open an issue](https://github.com/ImmortallKIng/crtctl/issues/new/choose)
with the output of `crtctl doctor`, `sudo crtctl doctor` and `crtctl info`.

## 13. Black screen after a reboot: recovery

This should not happen, because `setup` installs only modes you confirmed. But if the screen
is black after a reboot:

1. **Use another output, if you have one.** The EDID replacement applies only to the CRT's
   port, so another monitor or the TV works normally. Log in there and run
   `sudo crtctl remove DP-2`.
2. **Remove the kernel parameter for one boot.** In the boot menu (GRUB: press `e`;
   systemd-boot: press `e` as well), find `drm.edid_firmware=...` on the kernel line, delete it
   and boot (GRUB: Ctrl+X). The system starts with the monitor's own EDID. Then run
   `sudo crtctl remove DP-2`.
3. **Service method:** add `systemd.mask=crtctl-edid.service` to the kernel line the same way.
4. **Over the network:** if SSH is enabled, log in from another computer and run
   `sudo crtctl remove DP-2`, then reboot.

Before its first change crtctl saves `/etc/default/grub` and the boot entries with a
`.crtctl-backup` suffix.

## 14. Undo everything

```bash
sudo crtctl remove DP-2          # everything for this output
sudo crtctl remove DP-2 --edid   # only the EDID
sudo crtctl remove DP-2 --link   # only the DP link forcing
```

The picture does not change until the next reboot. Profiles you created stay; delete them
from `~/.config/crtctl/monitors/` and `/etc/crtctl/monitors/` if you want.

To remove crtctl itself: `sudo rm /usr/local/bin/crtctl` (or `sudo make uninstall`), and
`sudo rm -r /etc/crtctl` after `remove`.

## 15. What crtctl changes on the system

| File | Purpose |
|---|---|
| `/usr/lib/firmware/edid/crtctl-OUTPUT.bin` | the EDID (`/etc/crtctl/firmware` on immutable systems) |
| `/etc/dracut.conf.d/crtctl-edid.conf`, `/etc/mkinitcpio.conf.d/crtctl-edid.conf`, `/etc/initramfs-tools/hooks/crtctl-edid` | add the EDID to the initramfs |
| kernel parameter `drm.edid_firmware=OUTPUT:edid/crtctl-OUTPUT.bin` | tells the kernel which EDID to use for which output |
| `/etc/default/grub.d/99-crtctl.cfg` or `/etc/default/grub`, boot entries | where the kernel parameter is written; backed up as `.crtctl-backup` before the first change |
| `/etc/systemd/system/crtctl-edid.service`, `/etc/crtctl/apply-edid.sh` | the service install method |
| `/etc/systemd/system/crtctl-link-OUTPUT.service` | DP link forcing at boot |
| `/etc/X11/xorg.conf.d/90-crtctl-nvidia.conf` | NVIDIA on X11 only |
| `/etc/crtctl/state.json` | what is installed, the adapter's registers |
| `/etc/crtctl/monitors/`, `~/.config/crtctl/monitors/` | monitor profiles |

Nothing else is touched. `crtctl remove` undoes all of it for an output.

## Glossary

| Term | Meaning |
|---|---|
| **EDID** | data the monitor sends about itself: name, size, supported modes, frequency ranges |
| **DDC** | the wire (I²C) over which the EDID is read |
| **DPCD** | registers of a DisplayPort device; crtctl reads the adapter's limits from them |
| **DMT** | the VESA table of standard modes with exact timings (1024×768@85 etc.) |
| **CVT, GTF** | VESA formulas that compute timings for any resolution and refresh rate |
| **CVT-RB** | CVT with reduced blanking; meant for LCDs, often too short for a CRT |
| **Modeline** | a mode written as numbers: pixel clock and the horizontal and vertical timings |
| **Pixel clock** | pixels per second, in MHz; the adapter's limit |
| **Horizontal frequency** | lines per second, in kHz; the CRT's main limit |
| **Downspread** | a slight wobble of the DP link frequency to reduce interference; takes 0.5% of the bandwidth |
| **debugfs** | a kernel interface through which crtctl loads an EDID without a reboot |
| **initramfs** | the small file system the kernel boots from; the EDID has to be in it to work from the start |
| **KMS** | kernel mode setting: the kernel, not the X server, sets the video mode |
