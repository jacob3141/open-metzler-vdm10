// mediad: media pipeline of the door station. Video: AR0237/HiSPi -> ISP -> VI -> VPSS -> H.264.
// Audio: microphone -> FM2018 echo canceller -> inner codec -> AI, 16 kHz mono.
// Writes frames to stdout as [length u32 LE][flags u8][data]. Flag bit 0 marks a keyframe,
// bit 1 an audio frame: 20 ms of 16-bit little-endian PCM. Without bit 1 the data is Annex B H.264.
#include <fcntl.h>
#include <poll.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <unistd.h>

#include "hi_common.h"
#include "hi_comm_sys.h"
#include "hi_comm_vb.h"
#include "hi_comm_isp.h"
#include "hi_comm_vi.h"
#include "hi_comm_vpss.h"
#include "hi_comm_venc.h"
#include "hi_mipi.h"
#include "hi_sns_ctrl.h"
#include "hi_ae_comm.h"
#include "hi_awb_comm.h"
#include "hi_af_comm.h"
#include "mpi_sys.h"
#include "mpi_vb.h"
#include "mpi_isp.h"
#include "mpi_ae.h"
#include "mpi_awb.h"
#include "mpi_af.h"
#include "mpi_vi.h"
#include "mpi_vpss.h"
#include "mpi_venc.h"
#include "hi_comm_aio.h"
#include "mpi_ai.h"
#include "acodec.h"

#define W 1920
#define H 1080
#define FPS 30
#define AUDIO_RATE 16000
#define AUDIO_POINTS 320 // 20 ms
#define FLAG_KEY 1
#define FLAG_AUDIO 2
// ioctl of the vendor sound driver that switches the speaker amplifier on
#define DDM_SOUND_AMP_ON 0x401c5308

#define CHK(call)                                                  \
	do {                                                       \
		HI_S32 r_ = (call);                                \
		fprintf(stderr, "%-46s 0x%x\n", #call, r_);                 \
		if (r_ != HI_SUCCESS)                              \
			exit(1);                                   \
	} while (0)

extern void sensor_linear_1080p30_init_lvds(void);

// The ISP exposure control uses this symbol of the sensor library (0x20 there).
const unsigned char sensor_i2c_addr = 0x30;

// The OpenIPC library always calls the parallel variant, the door station uses HiSPi.
void sensor_linear_1080p30_init_dc_Sofia(void)
{
	fprintf(stderr, "Sensor: HiSPi initialisation\n");
	sensor_linear_1080p30_init_lvds();
}

static void *isp_thread(void *arg)
{
	HI_S32 r = HI_MPI_ISP_Run(0);
	fprintf(stderr, "ISP_Run returned: 0x%x\n", r);
	return NULL;
}

static void setup_mipi(void)
{
	static const unsigned short codes[4] = {0x3, 0x7, 0x1, 0x5};
	combo_dev_attr_t a;
	COMBO_DEV dev = 0;
	int fd, l, v, c;

	memset(&a, 0, sizeof(a));
	a.devno = 0;
	a.input_mode = INPUT_MODE_HISPI;
	a.lvds_attr.img_size.width = W;
	a.lvds_attr.img_size.height = H;
	a.lvds_attr.raw_data_type = RAW_DATA_12BIT;
	a.lvds_attr.wdr_mode = HI_WDR_MODE_NONE;
	a.lvds_attr.sync_mode = LVDS_SYNC_MODE_SOF;
	a.lvds_attr.data_endian = LVDS_ENDIAN_LITTLE;
	a.lvds_attr.sync_code_endian = LVDS_ENDIAN_LITTLE;
	for (l = 0; l < LVDS_LANE_NUM; l++) {
		a.lvds_attr.lane_id[l] = l;
		for (v = 0; v < WDR_VC_NUM; v++)
			for (c = 0; c < SYNC_CODE_NUM; c++)
				a.lvds_attr.sync_code[l][v][c] = codes[c];
	}
	fd = open("/dev/hi_mipi", O_RDWR);
	if (fd < 0) {
		perror("hi_mipi");
		exit(1);
	}
	CHK(ioctl(fd, HI_MIPI_RESET_MIPI, &dev));
	CHK(ioctl(fd, HI_MIPI_RESET_SENSOR, &dev));
	CHK(ioctl(fd, HI_MIPI_SET_DEV_ATTR, &a));
	CHK(ioctl(fd, HI_MIPI_UNRESET_MIPI, &dev));
	CHK(ioctl(fd, HI_MIPI_UNRESET_SENSOR, &dev));
	usleep(50000);
	close(fd);
}

static void setup_sys(void)
{
	VB_CONF_S vb;
	MPP_SYS_CONF_S sys;

	HI_MPI_SYS_Exit();
	HI_MPI_VB_Exit();
	memset(&vb, 0, sizeof(vb));
	vb.u32MaxPoolCnt = 128;
	vb.astCommPool[0].u32BlkSize = 3200 * 1024;
	vb.astCommPool[0].u32BlkCnt = 6;
	CHK(HI_MPI_VB_SetConf(&vb));
	CHK(HI_MPI_VB_Init());
	memset(&sys, 0, sizeof(sys));
	sys.u32AlignWidth = 64;
	CHK(HI_MPI_SYS_SetConf(&sys));
	CHK(HI_MPI_SYS_Init());
}

static void setup_isp(void)
{
	ALG_LIB_S lib;
	ISP_WDR_MODE_S wdr;
	ISP_PUB_ATTR_S pub;
	pthread_t t;

	CHK(sensor_register_callback());
	memset(&lib, 0, sizeof(lib));
	strcpy(lib.acLibName, HI_AE_LIB_NAME);
	CHK(HI_MPI_AE_Register(0, &lib));
	strcpy(lib.acLibName, HI_AWB_LIB_NAME);
	CHK(HI_MPI_AWB_Register(0, &lib));
	strcpy(lib.acLibName, HI_AF_LIB_NAME);
	CHK(HI_MPI_AF_Register(0, &lib));
	CHK(HI_MPI_ISP_MemInit(0));
	memset(&wdr, 0, sizeof(wdr));
	wdr.enWDRMode = WDR_MODE_NONE;
	CHK(HI_MPI_ISP_SetWDRMode(0, &wdr));
	memset(&pub, 0, sizeof(pub));
	pub.stWndRect.u32Width = W;
	pub.stWndRect.u32Height = H;
	pub.f32FrameRate = FPS;
	pub.enBayer = BAYER_GRBG;
	CHK(HI_MPI_ISP_SetPubAttr(0, &pub));
	CHK(HI_MPI_ISP_Init(0));
	CHK(pthread_create(&t, NULL, isp_thread, NULL));
	usleep(200000);
}

static void setup_vi(void)
{
	VI_DEV_ATTR_S d;
	VI_CHN_ATTR_S c;
	int i;

	memset(&d, 0, sizeof(d));
	d.enIntfMode = VI_MODE_HISPI;
	d.enWorkMode = VI_WORK_MODE_1Multiplex;
	d.au32CompMask[0] = 0xFFF00000;
	d.enScanMode = VI_SCAN_PROGRESSIVE;
	for (i = 0; i < VIU_DEV_AD_CHN_NUM; i++)
		d.s32AdChnId[i] = -1;
	d.enDataSeq = VI_INPUT_DATA_YUYV;
	d.enDataPath = VI_PATH_ISP;
	d.enInputDataType = VI_DATA_TYPE_RGB;
	d.stDevRect.u32Width = W;
	d.stDevRect.u32Height = H;
	CHK(HI_MPI_VI_SetDevAttr(0, &d));
	CHK(HI_MPI_VI_EnableDev(0));

	memset(&c, 0, sizeof(c));
	c.stCapRect.u32Width = W;
	c.stCapRect.u32Height = H;
	c.stDestSize.u32Width = W;
	c.stDestSize.u32Height = H;
	c.enCapSel = VI_CAPSEL_BOTH;
	c.enPixFormat = PIXEL_FORMAT_YUV_SEMIPLANAR_420;
	c.s32SrcFrameRate = -1;
	c.s32DstFrameRate = -1;
	CHK(HI_MPI_VI_SetChnAttr(0, &c));
	CHK(HI_MPI_VI_EnableChn(0));
}

static void setup_vpss(void)
{
	VPSS_GRP_ATTR_S g;
	VPSS_CHN_ATTR_S a;
	VPSS_CHN_MODE_S m;
	MPP_CHN_S src, dst;

	memset(&g, 0, sizeof(g));
	g.u32MaxW = W;
	g.u32MaxH = H;
	g.enPixFmt = PIXEL_FORMAT_YUV_SEMIPLANAR_420;
	g.bNrEn = HI_TRUE;
	g.enDieMode = VPSS_DIE_MODE_NODIE;
	CHK(HI_MPI_VPSS_CreateGrp(0, &g));
	CHK(HI_MPI_VPSS_StartGrp(0));
	memset(&a, 0, sizeof(a));
	a.s32SrcFrameRate = -1;
	a.s32DstFrameRate = -1;
	CHK(HI_MPI_VPSS_SetChnAttr(0, 0, &a));
	memset(&m, 0, sizeof(m));
	m.enChnMode = VPSS_CHN_MODE_USER;
	m.u32Width = W;
	m.u32Height = H;
	m.enPixelFormat = PIXEL_FORMAT_YUV_SEMIPLANAR_420;
	CHK(HI_MPI_VPSS_SetChnMode(0, 0, &m));
	CHK(HI_MPI_VPSS_EnableChn(0, 0));

	memset(&src, 0, sizeof(src));
	memset(&dst, 0, sizeof(dst));
	src.enModId = HI_ID_VIU;
	dst.enModId = HI_ID_VPSS;
	CHK(HI_MPI_SYS_Bind(&src, &dst));
}

static void setup_venc(void)
{
	VENC_CHN_ATTR_S a;
	MPP_CHN_S src, dst;

	memset(&a, 0, sizeof(a));
	a.stVeAttr.enType = PT_H264;
	a.stVeAttr.stAttrH264e.u32MaxPicWidth = W;
	a.stVeAttr.stAttrH264e.u32MaxPicHeight = H;
	a.stVeAttr.stAttrH264e.u32PicWidth = W;
	a.stVeAttr.stAttrH264e.u32PicHeight = H;
	a.stVeAttr.stAttrH264e.u32BufSize = W * H * 2;
	a.stVeAttr.stAttrH264e.u32Profile = 1;
	a.stVeAttr.stAttrH264e.bByFrame = HI_TRUE;
	a.stRcAttr.enRcMode = VENC_RC_MODE_H264CBR;
	a.stRcAttr.stAttrH264Cbr.u32Gop = FPS * 2;
	a.stRcAttr.stAttrH264Cbr.u32StatTime = 2;
	a.stRcAttr.stAttrH264Cbr.u32SrcFrmRate = FPS;
	a.stRcAttr.stAttrH264Cbr.fr32DstFrmRate = FPS;
	a.stRcAttr.stAttrH264Cbr.u32BitRate = 2048;
	a.stRcAttr.stAttrH264Cbr.u32FluctuateLevel = 1;
	a.stGopAttr.enGopMode = VENC_GOPMODE_NORMALP;
	a.stGopAttr.stNormalP.s32IPQpDelta = 2;
	CHK(HI_MPI_VENC_CreateChn(0, &a));
	CHK(HI_MPI_VENC_StartRecvPic(0));

	memset(&src, 0, sizeof(src));
	memset(&dst, 0, sizeof(dst));
	src.enModId = HI_ID_VPSS;
	dst.enModId = HI_ID_VENC;
	CHK(HI_MPI_SYS_Bind(&src, &dst));
}

static pthread_mutex_t out_mu = PTHREAD_MUTEX_INITIALIZER;

static void write_all(int fd, const void *buf, size_t n)
{
	const char *p = buf;

	while (n > 0) {
		ssize_t w = write(fd, p, n);
		if (w <= 0)
			_exit(1);
		p += w;
		n -= w;
	}
}

// write_header starts a frame; the caller holds out_mu until the data is written.
static void write_header(int out, HI_U32 len, int flags)
{
	unsigned char hdr[5] = {len, len >> 8, len >> 16, len >> 24, flags};

	write_all(out, hdr, sizeof(hdr));
}

static int is_key(const unsigned char *p, HI_U32 n)
{
	if (n > 4 && p[0] == 0 && p[1] == 0 && p[2] == 0 && p[3] == 1) {
		int t = p[4] & 0x1f;
		return t == 5 || t == 7;
	}
	return 0;
}

static void stream(int out)
{
	static VENC_PACK_S packs[16];
	struct pollfd pfd;

	pfd.fd = HI_MPI_VENC_GetFd(0);
	pfd.events = POLLIN;
	for (;;) {
		VENC_STREAM_S st;
		VENC_CHN_STAT_S stat;
		HI_U32 i, len = 0;
		int key = 0;

		if (poll(&pfd, 1, 2000) <= 0)
			_exit(2);
		if (HI_MPI_VENC_Query(0, &stat) != HI_SUCCESS || stat.u32CurPacks == 0)
			continue;
		memset(&st, 0, sizeof(st));
		st.pstPack = packs;
		st.u32PackCount = stat.u32CurPacks > 16 ? 16 : stat.u32CurPacks;
		if (HI_MPI_VENC_GetStream(0, &st, 1000) != HI_SUCCESS)
			continue;
		for (i = 0; i < st.u32PackCount; i++) {
			len += packs[i].u32Len - packs[i].u32Offset;
			key |= is_key(packs[i].pu8Addr + packs[i].u32Offset, packs[i].u32Len - packs[i].u32Offset);
		}
		pthread_mutex_lock(&out_mu);
		write_header(out, len, key ? FLAG_KEY : 0);
		for (i = 0; i < st.u32PackCount; i++)
			write_all(out, packs[i].pu8Addr + packs[i].u32Offset, packs[i].u32Len - packs[i].u32Offset);
		pthread_mutex_unlock(&out_mu);
		HI_MPI_VENC_ReleaseStream(0, &st);
	}
}

// setup_audio configures the codec like the vendor software: input IN1 (the output of the
// FM2018), and AI with u32ClkSel 0. With u32ClkSel 1 the microphone delivers only silence.
// The FM2018 itself is set up by DDM.ko at boot.
static int setup_audio(void)
{
	AIO_ATTR_S a;
	AI_CHN_PARAM_S p;
	unsigned int v;
	int fd = open("/dev/acodec", O_RDWR);

	if (fd < 0) {
		perror("acodec");
		return -1;
	}
	CHK(ioctl(fd, ACODEC_SOFT_RESET_CTRL));
	v = ACODEC_FS_16000;
	CHK(ioctl(fd, ACODEC_SET_I2S1_FS, &v));
	v = ACODEC_MIXER_IN1;
	CHK(ioctl(fd, ACODEC_SET_MIXER_MIC, &v));
	v = 60;
	CHK(ioctl(fd, ACODEC_SET_INPUT_VOL, &v));
	close(fd);

	fd = open("/dev/DDM/sound", O_RDWR);
	if (fd >= 0) {
		static unsigned int zero[7];
		if (ioctl(fd, DDM_SOUND_AMP_ON, zero) != 0)
			perror("amplifier");
		close(fd);
	}

	memset(&a, 0, sizeof(a));
	a.enSamplerate = AUDIO_RATE;
	a.enBitwidth = AUDIO_BIT_WIDTH_16;
	a.enWorkmode = AIO_MODE_I2S_MASTER;
	a.enSoundmode = AUDIO_SOUND_MODE_MONO;
	a.u32FrmNum = 30;
	a.u32PtNumPerFrm = AUDIO_POINTS;
	a.u32ChnCnt = 1;
	a.u32ClkSel = 0;
	HI_MPI_AI_DisableChn(0, 0);
	HI_MPI_AI_Disable(0);
	CHK(HI_MPI_AI_SetPubAttr(0, &a));
	CHK(HI_MPI_AI_Enable(0));
	CHK(HI_MPI_AI_EnableChn(0, 0));
	memset(&p, 0, sizeof(p));
	p.u32UsrFrmDepth = 30;
	CHK(HI_MPI_AI_SetChnParam(0, 0, &p));
	return 0;
}

static void *audio_thread(void *arg)
{
	int out = *(int *)arg;

	for (;;) {
		AUDIO_FRAME_S f;
		AEC_FRAME_S aec;

		if (HI_MPI_AI_GetFrame(0, 0, &f, &aec, 1000) != HI_SUCCESS)
			continue;
		pthread_mutex_lock(&out_mu);
		write_header(out, f.u32Len, FLAG_AUDIO);
		write_all(out, f.pVirAddr[0], f.u32Len);
		pthread_mutex_unlock(&out_mu);
		HI_MPI_AI_ReleaseFrame(0, 0, &f, &aec);
	}
	return NULL;
}

int main(void)
{
	// The vendor libraries write to stdout, so the video stream needs its own channel.
	int out = dup(1);

	dup2(2, 1);
	setvbuf(stdout, NULL, _IONBF, 0);
	setup_sys();
	setup_mipi();
	setup_isp();
	setup_vi();
	setup_vpss();
	setup_venc();
	if (getenv("MEDIAD_NO_AUDIO") == NULL && setup_audio() == 0) {
		pthread_t t;
		static int audio_out;

		audio_out = out;
		CHK(pthread_create(&t, NULL, audio_thread, &audio_out));
	}
	stream(out);
	return 0;
}
