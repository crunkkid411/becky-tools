// diag_hw - pinpoint why av_hwdevice_ctx_init fails on the supplied device.
#include <windows.h>
#include <d3d11.h>
#include <cstdio>
extern "C" {
#include <libavutil/hwcontext.h>
#include <libavutil/hwcontext_d3d11va.h>
}

int main() {
    // 1) FFmpeg creates its own device
    AVBufferRef* hw1 = nullptr;
    int r = av_hwdevice_ctx_create(&hw1, AV_HWDEVICE_TYPE_D3D11VA, nullptr, nullptr, 0);
    char buf[128];
    av_strerror(r, buf, sizeof buf);
    printf("av_hwdevice_ctx_create(default): %d (%s)\n", r, r == 0 ? "OK" : buf);
    if (hw1) av_buffer_unref(&hw1);

    // 2) our own device + manual QIs
    ID3D11Device* dev = nullptr; ID3D11DeviceContext* ctx = nullptr;
    D3D_FEATURE_LEVEL fl;
    HRESULT hr = D3D11CreateDevice(nullptr, D3D_DRIVER_TYPE_HARDWARE, nullptr,
                                   D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
                                   nullptr, 0, D3D11_SDK_VERSION, &dev, &fl, &ctx);
    printf("D3D11CreateDevice: hr=0x%08lx fl=0x%x\n", (unsigned long)hr, fl);
    if (FAILED(hr)) return 1;

    ID3D11VideoDevice* vd = nullptr;
    hr = dev->QueryInterface(__uuidof(ID3D11VideoDevice), (void**)&vd);
    printf("QI ID3D11VideoDevice: hr=0x%08lx\n", (unsigned long)hr);

    ID3D11VideoContext* vc = nullptr;
    hr = ctx->QueryInterface(__uuidof(ID3D11VideoContext), (void**)&vc);
    printf("QI ID3D11VideoContext: hr=0x%08lx\n", (unsigned long)hr);

    // 3) supplied-device init exactly like the probe does it
    AVBufferRef* hw2 = av_hwdevice_ctx_alloc(AV_HWDEVICE_TYPE_D3D11VA);
    AVHWDeviceContext* hctx = (AVHWDeviceContext*)hw2->data;
    AVD3D11VADeviceContext* d3d = (AVD3D11VADeviceContext*)hctx->hwctx;
    dev->AddRef();
    d3d->device = dev;
    r = av_hwdevice_ctx_init(hw2);
    av_strerror(r, buf, sizeof buf);
    printf("av_hwdevice_ctx_init(supplied): %d (%s)\n", r, r == 0 ? "OK" : buf);
    printf("  after init: device_context=%p video_device=%p video_context=%p\n",
           (void*)d3d->device_context, (void*)d3d->video_device, (void*)d3d->video_context);
    return 0;
}
