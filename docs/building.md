# Building

## doorbelld

```sh
cd device/doorbelld
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=5 go build -trimpath -ldflags='-s -w' -o doorbelld .
```

By default it starts `/home/app/oi/mediad`. Pass `-media ""` to run it without video.

## mediad

`mediad` needs a toolchain and vendor libraries that are not part of this repository. The build scripts expect them under `~/oi-tc` on a Linux machine.

### Toolchain

Download `toolchain.hisilicon-hi3516cv300.tgz` from the [OpenIPC toolchain release](https://github.com/OpenIPC/firmware/releases/tag/toolchain) and unpack it to `~/oi-tc`, so that this file exists:

```
~/oi-tc/arm-openipc-linux-musleabi_sdk-buildroot/bin/arm-linux-gcc
```

### Libraries and headers

Create `~/oi-tc/cv300/lib` and `~/oi-tc/cv300/inc` and fill them:

| Target | Source |
|---|---|
| `lib/*.so` | `general/package/hisilicon-osdrv-hi3516cv300/files/lib` in [OpenIPC/firmware](https://github.com/OpenIPC/firmware) |
| `lib/libsns_ar0237_i2c_dc.so` | `.../files/sensor` in the same repository |
| `inc/` | `kernel/include/hi3516cv300` in [OpenIPC/openhisilicon](https://github.com/OpenIPC/openhisilicon) |
| `lib/libuClibc-0.9.33.2.so`, `libpthread-0.9.33.2.so`, `libdl-0.9.33.2.so`, `libm-0.9.33.2.so` | `/lib` on the door station |

### Build

```sh
cd device/mediad
sh vbuild.sh mediad.c mediad
```

At runtime, `mediad` looks for its libraries in `/home/app/oi`.

### Why the build is unusual

- The toolchain targets musl, but the device runs uClibc. `build.sh` therefore links with `-nostdlib` against the device's uClibc and uses its own startup code (`start.S`). Avoid libc calls that pass `time_t` structures.
- The OpenIPC `libmpi` is V1.0.4.0, while the kernel modules on the device are V1.0.2.0. `ioctl_shim.c` converts the structures that changed between these versions.
