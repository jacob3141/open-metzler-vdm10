// avol reads or sets the volumes of the inner codec while mediad runs:
// avol                 prints input and output volume
// avol in <n> out <m>  sets them (input [-78, 80], output [-121, 6])
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <unistd.h>
#include "acodec.h"

int main(int argc, char **argv)
{
	int fd = open("/dev/acodec", O_RDWR), i;
	int v;

	if (fd < 0) {
		perror("acodec");
		return 1;
	}
	for (i = 1; i + 1 < argc; i += 2) {
		v = atoi(argv[i + 1]);
		if (ioctl(fd, strcmp(argv[i], "in") == 0 ? ACODEC_SET_INPUT_VOL : ACODEC_SET_OUTPUT_VOL, &v) != 0)
			perror(argv[i]);
	}
	if (ioctl(fd, ACODEC_GET_INPUT_VOL, &v) == 0)
		printf("in %d\n", v);
	if (ioctl(fd, ACODEC_GET_OUTPUT_VOL, &v) == 0)
		printf("out %d\n", v);
	close(fd);
	return 0;
}
