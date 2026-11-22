// Audio self-test: record the microphone, play a 1 kHz tone, measure level and tone energy.
// Works on the door station with CLKSEL=0 MIXER=1. SHARED=1 runs it next to mediad.
// Settings by environment: CHNS, CLKSEL, MIXER, VOL, FRAMES, TONE=0, REC=<file of raw samples>.
#include <fcntl.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <unistd.h>

#include "hi_common.h"
#include "hi_comm_sys.h"
#include "hi_comm_vb.h"
#include "hi_comm_aio.h"
#include "acodec.h"
#include "mpi_sys.h"
#include "mpi_vb.h"
#include "mpi_ai.h"
#include "mpi_ao.h"

#define RATE 16000
#define PTS 320
#define CHK(call)                                          \
	do {                                               \
		HI_S32 r_ = (call);                        \
		printf("%-44s 0x%x\n", #call, r_);         \
		if (r_ != HI_SUCCESS)                      \
			exit(1);                           \
	} while (0)

static double goertzel(const short *s, int n, double f)
{
	double w = 2 * 3.14159265358979 * f / RATE, c = 2 * cos(w), a = 0, b = 0;
	int i;
	for (i = 0; i < n; i++) {
		double t = s[i] + c * a - b;
		b = a;
		a = t;
	}
	return sqrt(a * a + b * b - c * a * b) / n;
}

static void setup_codec(void)
{
	int fd = open("/dev/acodec", O_RDWR);
	unsigned int v;

	if (fd < 0) {
		perror("acodec");
		exit(1);
	}
	CHK(ioctl(fd, ACODEC_SOFT_RESET_CTRL));
	v = ACODEC_FS_16000;
	CHK(ioctl(fd, ACODEC_SET_I2S1_FS, &v));
	v = atoi(getenv("MIXER") ? getenv("MIXER") : "0");
	CHK(ioctl(fd, ACODEC_SET_MIXER_MIC, &v));
	v = atoi(getenv("VOL") ? getenv("VOL") : "40");
	CHK(ioctl(fd, ACODEC_SET_INPUT_VOL, &v));
	close(fd);

	fd = open("/dev/DDM/sound", O_RDWR);
	if (fd >= 0) {
		static unsigned int zero[7];
		CHK(ioctl(fd, 0x401c5308, zero));
		close(fd);
	}
}

int main(void)
{
	static short tone[PTS];
	VB_CONF_S vb;
	MPP_SYS_CONF_S sys;
	AIO_ATTR_S a;
	AI_CHN_PARAM_S p;
	AUDIO_FRAME_S out;
	double phase = 0;
	int i, c, shared = getenv("SHARED") != NULL;
	int chns = getenv("CHNS") ? atoi(getenv("CHNS")) : 1; // AI channels, each is measured
	int clksel = getenv("CLKSEL") ? atoi(getenv("CLKSEL")) : 1;
	int frames = getenv("FRAMES") ? atoi(getenv("FRAMES")) : 250;
	int tone_on = getenv("TONE") ? atoi(getenv("TONE")) : 1;
	int rec = getenv("REC") ? open(getenv("REC"), O_WRONLY | O_CREAT | O_TRUNC, 0644) : -1; // raw 16-bit samples of channel 0

	setvbuf(stdout, NULL, _IONBF, 0);
	HI_MPI_AO_DisableChn(0, 0);
	HI_MPI_AO_Disable(0);
	for (c = 0; c < 2; c++) { /* a test that crashed may have left channels enabled */
		printf("AI_EnableChn %d 0x%x\n", c, HI_MPI_AI_EnableChn(0, c));
		printf("AI_DisableChn %d 0x%x\n", c, HI_MPI_AI_DisableChn(0, c));
	}
	printf("AI_Disable 0x%x\n", HI_MPI_AI_Disable(0));
	if (!shared) { // with SHARED=1 the MPP system of the running mediad is used, its video keeps running
		HI_MPI_SYS_Exit();
		HI_MPI_VB_Exit();
		memset(&vb, 0, sizeof(vb));
		vb.u32MaxPoolCnt = 16;
		CHK(HI_MPI_VB_SetConf(&vb));
		CHK(HI_MPI_VB_Init());
		memset(&sys, 0, sizeof(sys));
		sys.u32AlignWidth = 64;
		CHK(HI_MPI_SYS_SetConf(&sys));
		CHK(HI_MPI_SYS_Init());
	}
	setup_codec();

	memset(&a, 0, sizeof(a));
	a.enSamplerate = RATE;
	a.enBitwidth = AUDIO_BIT_WIDTH_16;
	a.enWorkmode = AIO_MODE_I2S_MASTER;
	a.enSoundmode = AUDIO_SOUND_MODE_MONO;
	a.u32FrmNum = 30;
	a.u32PtNumPerFrm = PTS;
	a.u32ChnCnt = chns;
	a.u32ClkSel = clksel;
	CHK(HI_MPI_AI_SetPubAttr(0, &a));
	CHK(HI_MPI_AI_Enable(0));
	for (c = 0; c < chns; c++) {
		CHK(HI_MPI_AI_EnableChn(0, c));
		memset(&p, 0, sizeof(p));
		p.u32UsrFrmDepth = 30;
		CHK(HI_MPI_AI_SetChnParam(0, c, &p));
	}
	a.u32ChnCnt = 1;
	CHK(HI_MPI_AO_SetPubAttr(0, &a));
	CHK(HI_MPI_AO_Enable(0));
	CHK(HI_MPI_AO_EnableChn(0, 0));

	memset(&out, 0, sizeof(out));
	out.enBitwidth = AUDIO_BIT_WIDTH_16;
	out.enSoundmode = AUDIO_SOUND_MODE_MONO;
	out.pVirAddr[0] = tone;
	out.u32Len = sizeof(tone);

	printf("Frame  Phase  Chn    RMS   1kHz\n");
	for (i = 0; i < frames; i++) {
		AUDIO_FRAME_S in;
		AEC_FRAME_S aec;
		int playing = tone_on && i >= frames * 3 / 10 && i < frames * 7 / 10, j;
		HI_S32 r;

		for (j = 0; j < PTS; j++) {
			tone[j] = playing ? 4000 * sin(phase) : 0;
			phase += 2 * 3.14159265358979 * 1000 / RATE;
		}
		r = HI_MPI_AO_SendFrame(0, 0, &out, 1000);
		if (r != HI_SUCCESS)
			printf("AO_SendFrame 0x%x\n", r);
		for (c = 0; c < chns; c++) {
			r = HI_MPI_AI_GetFrame(0, c, &in, &aec, 1000);
			if (r != HI_SUCCESS) {
				printf("AI_GetFrame %d 0x%x\n", c, r);
				continue;
			}
			if (rec >= 0 && c == 0 && write(rec, in.pVirAddr[0], in.u32Len) < 0)
				perror("rec");
			if (i % 25 == 24) {
				const short *s = in.pVirAddr[0];
				int n = in.u32Len / 2;
				double sum = 0;
				for (j = 0; j < n; j++)
					sum += (double)s[j] * s[j];
				printf("%5d  %-5s  %3d  %6.0f  %5.0f\n", i, playing ? "TONE" : "quiet", c, sqrt(sum / n), goertzel(s, n, 1000));
			}
			HI_MPI_AI_ReleaseFrame(0, c, &in, &aec);
		}
	}
	HI_MPI_AO_DisableChn(0, 0);
	HI_MPI_AO_Disable(0);
	if (rec >= 0)
		close(rec);
	for (c = 0; c < chns; c++)
		HI_MPI_AI_DisableChn(0, c);
	HI_MPI_AI_Disable(0);
	if (!shared) {
		HI_MPI_SYS_Exit();
		HI_MPI_VB_Exit();
	}
	return 0;
}
