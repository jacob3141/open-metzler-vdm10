// Reads registers of the AR0237 sensor (bus 0, address 0x18).
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/ioctl.h>
#include <unistd.h>

#define I2C_SLAVE_FORCE 0x0706

int main(int argc, char **argv)
{
	int i, fd = open("/dev/i2c-0", O_RDWR);

	if (fd < 0 || ioctl(fd, I2C_SLAVE_FORCE, 0x18) < 0) {
		perror("i2c");
		return 1;
	}
	for (i = 1; i < argc; i++) {
		unsigned int r = strtoul(argv[i], NULL, 16);
		unsigned char reg[2] = {r >> 8, r}, val[2] = {0, 0};
		if (write(fd, reg, 2) != 2 || read(fd, val, 2) != 2)
			printf("%04x: error\n", r);
		else
			printf("%04x: %02x%02x\n", r, val[0], val[1]);
	}
	return 0;
}
