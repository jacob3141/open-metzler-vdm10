// Translates ioctls of libmpi V1.0.4.0 for the V1.0.2.0 kernel modules of the door station.
#include <errno.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define IOC_NR(c) ((c) & 0xff)
#define IOC_TYPE(c) (((c) >> 8) & 0xff)
#define IOC_SIZE(c) (((c) >> 16) & 0x3fff)
#define IOC_WITH_SIZE(c, s) (((c) & ~(0x3fffUL << 16)) | ((unsigned long)(s) << 16))

static long raw_ioctl(long fd, unsigned long cmd, long arg)
{
	register long r0 __asm__("r0") = fd;
	register long r1 __asm__("r1") = (long)cmd;
	register long r2 __asm__("r2") = arg;
	register long r7 __asm__("r7") = 54;
	__asm__ volatile("svc 0" : "+r"(r0) : "r"(r1), "r"(r2), "r"(r7) : "memory");
	return r0;
}

#define NEW_VB_CONF 452
#define OLD_VB_CONF 388

static long translate(long fd, unsigned long cmd, long arg)
{
	if (IOC_SIZE(cmd) == NEW_VB_CONF) {
		unsigned char old[OLD_VB_CONF];
		const unsigned char *in = (const unsigned char *)arg;
		int i;
		memcpy(old, in, 4);
		for (i = 0; i < 16; i++)
			memcpy(old + 4 + i * 24, in + 4 + i * 28, 24);
		return raw_ioctl(fd, IOC_WITH_SIZE(cmd, OLD_VB_CONF), (long)old);
	}
	if (IOC_TYPE(cmd) == 'B' && IOC_SIZE(cmd) == 456) {
		unsigned char old[392];
		const unsigned char *in = (const unsigned char *)arg;
		int i;
		memcpy(old, in, 8);
		for (i = 0; i < 16; i++)
			memcpy(old + 8 + i * 24, in + 8 + i * 28, 24);
		return raw_ioctl(fd, IOC_WITH_SIZE(cmd, 392), (long)old);
	}
	if (IOC_TYPE(cmd) == 'B' && IOC_NR(cmd) == 11 && IOC_SIZE(cmd) == 20) {
		unsigned char buf[20];
		long r;
		memcpy(buf, (void *)arg, 16);
		memset(buf + 16, 0, 4);
		r = raw_ioctl(fd, IOC_WITH_SIZE(cmd, 16), (long)buf);
		memcpy((void *)arg, buf, 20);
		return r;
	}
	return raw_ioctl(fd, cmd, arg);
}

int ioctl(int fd, unsigned long cmd, ...)
{
	va_list ap;
	long arg, r;
	va_start(ap, cmd);
	arg = va_arg(ap, long);
	va_end(ap);
	// The sensor library hardcodes 0x10, the AR0237 of the door station is at 0x18.
	if (cmd == 0x0706 && arg == 0x10)
		arg = 0x18;
	r = translate(fd, cmd, arg);
	if (r < 0 && r >= -4095) {
		if (getenv("IOCTL_TRACE"))
			fprintf(stderr, "ioctl fd=%d type=0x%02lx nr=%lu size=%lu -> %ld\n", fd, IOC_TYPE(cmd), IOC_NR(cmd), IOC_SIZE(cmd), r);
		errno = -r;
		return -1;
	}
	return r;
}
