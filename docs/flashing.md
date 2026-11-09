# Flashing the Metzler VDM10

This guide explains how to replace the stock cloud firmware with the open-source
replacement that comes with this repository.  The result: SSH access as root,
DHCP with a static fallback, no cloud processes, `doorbelld` and `mediad` running
in place of `hicore`.

---

## Hardware

| Item | Value |
|---|---|
| Model | Metzler VDM10-VM-POE (outdoor unit) |
| SoC | HiSilicon Hi3516CV300 |
| Sensor | Sony/On-Semi AR0237, HiSPi |
| Flash | 32 MB SPI-NOR (W25Q256) |
| Kernel | Linux 3.18.20, BusyBox 1.31.1, Dropbear 2018.76 |
| Stock firmware | V2.2.56 build 230608 |
| PHY | Realtek RTL8201F, RMII |

The device is sold under several labels.  The hardware is identical to some EZVIZ
and Hikvision door-station models with a Hi3516CV300 SoC.

---

## Flash layout

| MTD | Size | Mount | Contents |
|---|---|---|---|
| `mtd0` | 1.5 MB | `/home/config` (jffs2, rw) | Device config — survives reboots |
| `mtd1` | 14.1 MB | `/home/hik` (cramfs, ro) | Firmware bundle: kernel, ramdisk, app tarballs, `start.sh`, `dec` |

`/` is an ext2 ramdisk from `ramdisk.gz`.  `/home/app` is a tmpfs populated at boot.

The device IP is **not** set by the firmware — it comes from the U-Boot kernel
parameter `ip=192.0.0.64:…`.  After erasing the device config the factory address
is **192.0.0.64**.

---

## Tools you need

- Python 3 (Windows: `py -3`, Linux/Mac: `python3`)
- Scripts from `tools/` in this repository (see below)
- A TFTP server on your PC (the script provides one)
- A direct Ethernet cable between PC and device, **no switch**
- PC Ethernet port configured to `192.0.0.1/24`
  (Windows: "Use the following IP address" in network adapter settings)
- Windows Defender Firewall **off** during the flash, or a rule that allows
  UDP 69 inbound and all traffic to/from `192.0.0.64`
- **For first-time setup or recovery:** a USB-to-UART adapter (3.3 V),
  wired to the UART pads on the PCB (GND/TX/RX only — **do not connect VCC**),
  terminal at **115200 8N1**

---

## Obtaining the original firmware

The stock firmware image is included in `firmware/` and its SHA-256 is listed in
`firmware/SHA256SUMS`.  You can also download it directly from the manufacturer:

```
https://edelstahl-tuerklingel.de/dl/OUTDOOR_V2.2.56_build230608.dav
SHA-256: 7230016200b48076f2ca734b400a7f6e56523d07e73dcfe6d0312b71ba4ad10e
```

---

## Building the replacement firmware image

All scripts are in `tools/`.  Run them with `py -I` (Windows) or `python3 -I`
so that an accidentally planted module in the working directory cannot shadow a
standard library module.

### Step 1 — Unpack the original `.dav`

```sh
py -I tools/hikfw.py unpack firmware/OUTDOOR_V2.2.56_build230608.dav -o build/orig
```

This writes `build/orig/cramfs.img` and several other files.

### Step 2 — Replace `dec` inside the cramfs

`dec` is the binary that decrypts and runs `start.sh` (the main init script).
Replace it with the shell script `device/fake_dec.sh` from this repository:

```sh
py -I tools/cramfs_replace.py build/orig/cramfs.img dec device/fake_dec.sh build/new_cramfs.img
```

The script replaces the file in the cramfs image and recomputes the CRC over
exactly `fs_size` bytes (read from superblock offset 4; here 13,570,048).

### Step 3 — Splice the new cramfs back into the firmware bundle

```sh
cp build/orig/cramfs.img build/orig/cramfs.img.bak
cp build/new_cramfs.img build/orig/cramfs.img
py -I tools/hikfw.py repack build/orig -o build/digicap.dav
```

### Step 4 — Patch the firmware header

U-Boot rejects the image unless `total_size` at header offset 32 is set to
`0xFFFFFFFF`:

```sh
py -I tools/patch_hdr.py build/digicap.dav build/digicap.dav
```

### Step 5 — Verify

```sh
py -I tools/hikfw.py info build/digicap.dav
py -I tools/verify_dav.py build/digicap.dav build/orig/cramfs.img.bak device/fake_dec.sh
```

The `info` output must show `device_class = 0x3F1` and `total_size = 0xFFFFFFFF`.
`verify_dav.py` checks that only `dec` changed and that the cramfs CRC is correct.

---

## Flashing via U-Boot TFTP

This is the standard flash path.  You do not need UART if the device is already
running a previous version of this firmware; use UART for a first-time flash or
recovery.

### Setup

1. Connect the PC directly to the device with an Ethernet cable.
2. Set the PC's Ethernet adapter to `192.0.0.1 / 255.255.255.0`.
3. Put `build/digicap.dav` in a directory and start the TFTP server:

```sh
py -I tools/tftp_rw_server.py build 0.0.0.0
```

Leave this running in a separate terminal.

### Interrupt U-Boot

1. Connect UART (115200 8N1).
2. Power off the device completely (remove PoE / DC supply).
3. Power it on and **immediately press `u`** in the UART terminal when you see
   the first boot output.  You have about one second.
4. U-Boot drops into an upgrade menu.

### TFTP upgrade

In the U-Boot menu enter:

```
Server IP: 192.0.0.1
Device IP: 192.0.0.64
Filename: digicap.dav
```

When asked **"erase devcfg (y/n)?"** answer **`n`** unless you intentionally want
to wipe the device configuration.  Answering `y` removes `/home/config` and with
it any `local.sh` you placed there; the device then boots to the factory IP
`192.0.0.64` and you need to re-deploy `local.sh`.

U-Boot downloads and writes the image.  When it finishes, the device reboots.

---

## First boot after flashing

The device boots into the replacement firmware:

- Cloud processes (`hicore`, `gpl_process`, `sipServer`) are **not started**.
- All hardware drivers are loaded (camera, codec, audio, LED, IR, RTC, watchdog).
- `udhcpc` runs with hostname `tuerstation`.  If no lease arrives it keeps the
  kernel IP `192.0.0.64` as a fallback; if a lease expires the last address stays
  (no `deconfig`).

SSH is available on port 22, user `root`, key-only authentication.  The old
Dropbear version requires the `ssh-rsa` algorithm to be enabled explicitly:

```sh
ssh -i <your-key> \
    -o PubkeyAcceptedAlgorithms=+ssh-rsa \
    -o StrictHostKeyChecking=no \
    root@<device-ip>
```

Dropbear rejects direct command execution ("exec request failed").  Send commands
via stdin instead:

```sh
printf 'uname -a\n' | ssh -i <your-key> \
    -o PubkeyAcceptedAlgorithms=+ssh-rsa \
    root@<device-ip>
```

---

## Authorising your SSH key

The cramfs is read-only; the writable partition is `/home/config` (jffs2, `mtd0`).
Place your key there:

```sh
mkdir -p /home/config/.ssh
echo 'ssh-ed25519 AAAA… your-key-comment' >> /home/config/.ssh/authorized_keys
chmod 700 /home/config/.ssh
chmod 600 /home/config/.ssh/authorized_keys
```

Dropbear looks for `~root/.ssh/authorized_keys`; `root`'s home is `/root` in the
ramdisk.  Wire it up with a symlink from the ramdisk side, or add this line to
`/home/config/local.sh` (see next section) so it runs at every boot:

```sh
ln -sf /home/config/.ssh /root/.ssh
```

---

## `local.sh` — persistent customisation without reflashing

If `/home/config/local.sh` is executable, `start.sh` runs it in the background
near the end of the boot sequence.  This is the recommended way to make
persistent changes without reflashing.

Minimal template:

```sh
#!/bin/sh
# /home/config/local.sh

# Remove the busybox restricted shell and the auto-logout from /etc/profile
# so that UART logins land in a plain root shell.
sed -i '/^\/bin\/psh$/d; /^TMOUT=/d' /etc/profile

# Kill any psh that is already running on the console.
for pid in $(pgrep -x psh); do kill "$pid"; done

# Start doorbelld, restart it if it crashes.
while true; do
    /home/app/doorbelld -listen :8080
    sleep 2
done &
```

Copy it to the device:

```sh
scp -i <your-key> -O \
    -o PubkeyAcceptedAlgorithms=+ssh-rsa \
    local.sh root@<device-ip>:/home/config/local.sh
printf 'chmod +x /home/config/local.sh\n' | \
    ssh -i <your-key> -o PubkeyAcceptedAlgorithms=+ssh-rsa root@<device-ip>
```

(`-O` forces the legacy SCP protocol, which the old Dropbear supports.)

---

## Deploying `doorbelld` and `mediad`

The easiest path uses `device/deploy.py` from this repository.  Run it from Git
Bash on Windows (native `ssh.exe` rejects the key due to Windows file permission
checks; use OpenSSH from Git Bash):

```sh
# In Git Bash, not PowerShell or cmd.exe
export MSYS_NO_PATHCONV=1
python3 -I device/deploy.py flash
```

Modes:

| Mode | What it does |
|---|---|
| `ram` | Copies binaries to `/home/app`, starts them without writing flash |
| `flash` | Writes a new mtd1 image (only changed 64 KB blocks); permanent |
| `push` | Copies only `doorbelld` to `/home/config`, updates `local.sh` |

`flash` mode rewrites only the blocks that changed, verifies every block after
writing, and refuses to continue if a readback mismatch is found.  Unmount
`/home/hik` before reading back (`umount /home/hik`) to avoid getting a stale
cramfs block from the kernel cache.

---

## Recovery

If the device ends up in a boot loop or is otherwise unreachable, flash the
original firmware via the same TFTP procedure:

```sh
py -I tools/tftp_rw_server.py firmware 0.0.0.0
```

Then interrupt U-Boot, enter `192.0.0.1` as server and `digicap.dav` as filename
(the original `OUTDOOR_V2.2.56_build230608.dav` renamed to `digicap.dav`).
Answer **`n`** to "erase devcfg" unless you need a full factory reset.

---

## UART wiring

The UART pads are on the main PCB.  Connect only **GND**, **TX** (device → PC),
and **RX** (PC → device).  **Do not connect VCC** — the device is powered by PoE
or its DC input.

| Pad label | UART adapter pin |
|---|---|
| GND | GND |
| TX | RX |
| RX | TX |

Adapter voltage: **3.3 V**.  115200 8N1.

Anyone with physical access to the device has an unrestricted root shell via UART
regardless of the firmware.  U-Boot also allows booting a custom image over TFTP.
Treat physical access as equivalent to root access.

---

## Known limitations

- **Audio not working.** The MPP audio pipeline initialises successfully, but the
  microphone captures only noise.  The FM2018 codec (I2C) and the analog path are
  not initialised.  This is a known open issue.
- **Long cable.** On a cable run longer than roughly 5 m the PHY (RTL8201F) fails
  to complete autonegotiation and loses the link after a few seconds.  10 Mbit/s
  half-duplex gives a link but almost no receive traffic.  Check connectors,
  couplers, and the cable pairs if this occurs.
- **Physical button.** The button event (`/dev/event0`, `gpio-keys`, code 5) is
  wired up in `doorbelld` but has only been tested with synthetic input.  Real
  button presses have not been confirmed yet.
