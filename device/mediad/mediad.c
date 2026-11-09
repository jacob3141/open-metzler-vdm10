// mediad: video pipeline of the door station (AR0237/HiSPi -> ISP -> VI -> VPSS -> H.264).
// Writes frames to stdout as [length u32 LE][flags u8, bit 0 = keyframe][Annex B data].
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

#define W 1920
#define H 1080
#define FPS 30

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
		unsigned char hdr[5];
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
		hdr[0] = len;
		hdr[1] = len >> 8;
		hdr[2] = len >> 16;
		hdr[3] = len >> 24;
		hdr[4] = key;
		write_all(out, hdr, sizeof(hdr));
		for (i = 0; i < st.u32PackCount; i++)
			write_all(out, packs[i].pu8Addr + packs[i].u32Offset, packs[i].u32Len - packs[i].u32Offset);
		HI_MPI_VENC_ReleaseStream(0, &st);
	}
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
	stream(out);
	return 0;
}
