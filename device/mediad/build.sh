#!/bin/sh
# Builds programs against the uClibc of the door station. Usage: sh build.sh <name.c> <output> [libs...]
set -e
TC="$HOME/oi-tc/arm-openipc-linux-musleabi_sdk-buildroot/bin/arm-linux-gcc"
SDK="$HOME/oi-tc/cv300"
SRC=$1
OUT=$2
shift 2
LIBS=""
for l in "$@"; do LIBS="$LIBS -l:$l"; done
"$TC" -march=armv5te -mtune=arm926ej-s -mfloat-abi=soft -marm -Os -Wall -no-pie -fno-pie \
	-fno-stack-protector -D_GNU_SOURCE -DHI_ACODEC_TYPE_INNER -I"$SDK/inc" -nostdlib -nostartfiles \
	-Wl,--dynamic-linker=/lib/ld-uClibc.so.0 -Wl,--allow-shlib-undefined -Wl,-rpath,/home/app/oi \
	-L"$SDK/lib" -rdynamic "$(dirname "$0")/start.S" "$(dirname "$0")/ioctl_shim.c" "$SRC" -o "$OUT" $LIBS "$SDK/lib/libuClibc-0.9.33.2.so" -lgcc
ls -l "$OUT"
