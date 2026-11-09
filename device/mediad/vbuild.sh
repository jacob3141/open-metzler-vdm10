#!/bin/sh
# Builds a program against all MPP libraries. Usage: sh vbuild.sh <name.c> <output>
cd "$(dirname "$0")"
sh build.sh "$1" "$2" libsns_ar0237_i2c_dc.so lib_hiae.so lib_hiawb.so lib_hiaf.so lib_hidefog.so libisp.so \
	libmpi.so libive.so libupvqe.so libdnvqe.so libVoiceEngine.so libhive_common.so libhive_AEC.so libhive_AGC.so \
	libhive_ANR.so libhive_EQ.so libhive_GAIN.so libhive_HDR.so libhive_HPF.so libhive_MBC.so libhive_RES.so \
	libhive_RNR.so libpthread-0.9.33.2.so libdl-0.9.33.2.so libm-0.9.33.2.so
