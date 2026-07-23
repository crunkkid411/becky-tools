// diag_adapters - list DXGI adapters, create a device on each, QI video interfaces.
#include <windows.h>
#include <d3d11.h>
#include <dxgi.h>
#include <cstdio>

int main() {
    IDXGIFactory1* fac = nullptr;
    if (FAILED(CreateDXGIFactory1(__uuidof(IDXGIFactory1), (void**)&fac))) {
        printf("CreateDXGIFactory1 failed\n"); return 1;
    }
    for (UINT i = 0; ; i++) {
        IDXGIAdapter1* ad = nullptr;
        if (fac->EnumAdapters1(i, &ad) == DXGI_ERROR_NOT_FOUND) break;
        DXGI_ADAPTER_DESC1 d; ad->GetDesc1(&d);
        printf("adapter %u: %ls  vendor=0x%04x flags=0x%x vram=%zuMB\n",
               i, d.Description, d.VendorId, d.Flags, (size_t)(d.DedicatedVideoMemory >> 20));
        ID3D11Device* dev = nullptr; ID3D11DeviceContext* ctx = nullptr;
        D3D_FEATURE_LEVEL fl;
        HRESULT hr = D3D11CreateDevice(ad, D3D_DRIVER_TYPE_UNKNOWN, nullptr,
                                       D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
                                       nullptr, 0, D3D11_SDK_VERSION, &dev, &fl, &ctx);
        printf("  create(VIDEO_SUPPORT): hr=0x%08lx fl=0x%x\n", (unsigned long)hr, fl);
        if (SUCCEEDED(hr)) {
            ID3D11VideoDevice* vd = nullptr;
            HRESULT h2 = dev->QueryInterface(__uuidof(ID3D11VideoDevice), (void**)&vd);
            printf("  QI ID3D11VideoDevice: hr=0x%08lx\n", (unsigned long)h2);
            if (vd) vd->Release();
            ctx->Release(); dev->Release();
        } else {
            hr = D3D11CreateDevice(ad, D3D_DRIVER_TYPE_UNKNOWN, nullptr, 0,
                                   nullptr, 0, D3D11_SDK_VERSION, &dev, &fl, &ctx);
            printf("  create(no flags):     hr=0x%08lx fl=0x%x\n", (unsigned long)hr, fl);
            if (SUCCEEDED(hr)) {
                ID3D11VideoDevice* vd = nullptr;
                HRESULT h2 = dev->QueryInterface(__uuidof(ID3D11VideoDevice), (void**)&vd);
                printf("  QI ID3D11VideoDevice: hr=0x%08lx\n", (unsigned long)h2);
                if (vd) vd->Release();
                ctx->Release(); dev->Release();
            }
        }
        ad->Release();
    }
    fac->Release();
    return 0;
}
