//go:build windows

package main

import (
	"fmt"
	"image"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	d3d11   = windows.NewLazySystemDLL("d3d11.dll")
	combase = windows.NewLazySystemDLL("combase.dll")

	procD3D11CreateDevice   = d3d11.NewProc("D3D11CreateDevice")
	procCreateWinRTDevice   = d3d11.NewProc("CreateDirect3D11DeviceFromDXGIDevice")
	procRoInitialize        = combase.NewProc("RoInitialize")
	procRoGetActivationFactory = combase.NewProc("RoGetActivationFactory")
	procWindowsCreateString = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString = combase.NewProc("WindowsDeleteString")
)

var (
	iidItemInterop = mustGUID("3628E81B-3CAC-4C60-B7F4-23CE0E0C3356")
	iidItemStatics = mustGUID("A87EBEA5-457C-5788-AB47-0CF1D3637E74")
	iidCaptureItem = mustGUID("79C3F95B-31F7-4EC2-A464-632EF5D30760")
	iidPoolStatics2 = mustGUID("589B103F-6BBC-5DF5-A991-02E28B3B66D5")
	iidSession2    = mustGUID("2C39AE40-7D2E-5044-804E-8B6799D4CF9E")
	iidSession3    = mustGUID("F2CDD966-22AE-5EA1-9596-3A289344C3BE")
	iidClosable    = mustGUID("30D5A829-7FA4-4026-83BB-D75BAE4EA99E")
	iidDXGIDevice  = mustGUID("54EC77FA-1377-44E6-8C32-88FD5F44C84C")
	iidDxgiAccess  = mustGUID("A9B3D012-3DF2-4EE3-B8D1-8695F457D3C1")
	iidTexture2D   = mustGUID("6F15AAF2-D208-4E89-9AB4-489535D34F9C")
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString("{" + s + "}")
	if err != nil {
		panic(err)
	}
	return g
}

type d3d11Texture2DDesc struct {
	Width          uint32
	Height         uint32
	MipLevels      uint32
	ArraySize      uint32
	Format         uint32
	SampleCount    uint32
	SampleQuality  uint32
	Usage          uint32
	BindFlags      uint32
	CPUAccessFlags uint32
	MiscFlags      uint32
}

type d3d11Mapped struct {
	PData      uintptr
	RowPitch   uint32
	DepthPitch uint32
}

type wgcState struct {
	mu         sync.Mutex
	device     uintptr
	ctx        uintptr
	winrtDev   uintptr
	initErr    error
	key        uintptr
	kind       int
	item       uintptr
	pool       uintptr
	session    uintptr
	staging    uintptr
	stgW       int32
	stgH       int32
	stgFmt     int32
	last       *image.RGBA
	blankN     int
	skipWindow map[uintptr]bool
}

var wgcGlobal = &wgcState{skipWindow: map[uintptr]bool{}}

func wgcCaptureWindow(hwnd syscall.Handle) (image.Image, error) {
	return wgcGlobal.capture(uintptr(hwnd), 1)
}

func wgcCaptureMonitorCrop(hwnd syscall.Handle) (image.Image, error) {
	mon, _, _ := procMonitorFromWindow.Call(uintptr(hwnd), MONITOR_DEFAULTTONEAREST)
	if mon == 0 {
		return nil, fmt.Errorf("MonitorFromWindow failed")
	}
	img, err := wgcGlobal.capture(mon, 2)
	if err != nil || img == nil {
		return nil, err
	}
	rgba, ok := img.(*image.RGBA)
	if !ok {
		return img, nil
	}
	var wr RECT
	if ret, _, _ := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr))); ret == 0 {
		return img, nil
	}
	var mi monitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if ret, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); ret == 0 {
		return img, nil
	}
	return cropRGBA(rgba, int(wr.Left-mi.RcMonitor.Left), int(wr.Top-mi.RcMonitor.Top), int(wr.Right-wr.Left), int(wr.Bottom-wr.Top)), nil
}

func (s *wgcState) capture(key uintptr, kind int) (image.Image, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == 1 && s.skipWindow[key] {
		return nil, nil
	}
	if s.initErr != nil {
		return nil, s.initErr
	}
	if err := s.ensureDevice(); err != nil {
		s.initErr = err
		return nil, err
	}
	if s.pool == 0 || s.kind != kind || s.key != key {
		var err error
		if kind == 1 {
			err = s.openTarget(key, kind, true)
		} else {
			err = s.openTarget(key, kind, false)
		}
		if err != nil {
			return nil, err
		}
	}
	img, err := s.nextFrame()
	if err != nil || img == nil {
		return nil, err
	}
	if kind == 1 && isImageBlank(img) {
		s.blankN++
		if s.blankN >= 2 {
			s.skipWindow[key] = true
			s.closeTargetLocked()
		}
		return img, nil
	}
	s.blankN = 0
	return img, nil
}

func (s *wgcState) ensureDevice() error {
	if s.device != 0 {
		return nil
	}
	if err := procD3D11CreateDevice.Find(); err != nil {
		return fmt.Errorf("系统没有 D3D11CreateDevice")
	}
	if err := procCreateWinRTDevice.Find(); err != nil {
		return fmt.Errorf("系统不支持 Windows Graphics Capture")
	}
	if err := roInit(); err != nil {
		return err
	}
	var device, ctx uintptr
	hr, _, _ := procD3D11CreateDevice.Call(0, 1, 0, 0x20, 0, 0, 7, uintptr(unsafe.Pointer(&device)), 0, uintptr(unsafe.Pointer(&ctx)))
	if failed(hr) || device == 0 {
		hr, _, _ = procD3D11CreateDevice.Call(0, 5, 0, 0x20, 0, 0, 7, uintptr(unsafe.Pointer(&device)), 0, uintptr(unsafe.Pointer(&ctx)))
		if failed(hr) || device == 0 {
			return fmt.Errorf("D3D11CreateDevice 0x%08X", uint32(hr))
		}
	}
	dxgi, err := qi(device, iidDXGIDevice)
	if err != nil {
		release(ctx)
		release(device)
		return err
	}
	var inspect uintptr
	hr, _, _ = procCreateWinRTDevice.Call(dxgi, uintptr(unsafe.Pointer(&inspect)))
	release(dxgi)
	if failed(hr) || inspect == 0 {
		release(ctx)
		release(device)
		return fmt.Errorf("CreateDirect3D11DeviceFromDXGIDevice 0x%08X", uint32(hr))
	}
	s.device = device
	s.ctx = ctx
	s.winrtDev = inspect
	return nil
}

func (s *wgcState) openTarget(key uintptr, kind int, window bool) error {
	factory, err := activate("Windows.Graphics.Capture.GraphicsCaptureItem", iidItemInterop)
	if err != nil {
		base, err2 := activate("Windows.Graphics.Capture.GraphicsCaptureItem", iidItemStatics)
		if err2 != nil {
			return err
		}
		interop, err3 := qi(base, iidItemInterop)
		release(base)
		if err3 != nil {
			return fmt.Errorf("IGraphicsCaptureItemInterop: %w", err3)
		}
		factory = interop
	}
	defer release(factory)

	var item uintptr
	iid := iidCaptureItem
	var hr uintptr
	if window {
		hr = comCall(factory, 3, key, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&item)))
	} else {
		hr = comCall(factory, 4, key, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&item)))
	}
	if failed(hr) || item == 0 {
		return fmt.Errorf("CreateForWindow/Monitor 0x%08X", uint32(hr))
	}

	var size struct{ W, H int32 }
	hr = comCall(item, 7, uintptr(unsafe.Pointer(&size)))
	if failed(hr) || size.W < 2 || size.H < 2 || size.W > 8192 || size.H > 8192 {
		release(item)
		return fmt.Errorf("GraphicsCaptureItem.Size 0x%08X (%dx%d)", uint32(hr), size.W, size.H)
	}

	poolFactory, err := activate("Windows.Graphics.Capture.Direct3D11CaptureFramePool", iidPoolStatics2)
	if err != nil {
		release(item)
		return err
	}
	defer release(poolFactory)

	var pool uintptr
	packed := uintptr(uint64(uint32(size.W)) | uint64(uint32(size.H))<<32)
	hr = comCall(poolFactory, 6, s.winrtDev, 87, 2, packed, uintptr(unsafe.Pointer(&pool)))
	if failed(hr) || pool == 0 {
		release(item)
		return fmt.Errorf("CreateFreeThreaded 0x%08X", uint32(hr))
	}
	var session uintptr
	hr = comCall(pool, 10, item, uintptr(unsafe.Pointer(&session)))
	if failed(hr) || session == 0 {
		closeWinRT(pool)
		release(item)
		return fmt.Errorf("CreateCaptureSession 0x%08X", uint32(hr))
	}
	if sess2, err := qi(session, iidSession2); err == nil {
		comCall(sess2, 7, 0)
		release(sess2)
	}
	if sess3, err := qi(session, iidSession3); err == nil {
		comCall(sess3, 7, 0)
		release(sess3)
	}
	hr = comCall(session, 6)
	if failed(hr) {
		closeWinRT(session)
		closeWinRT(pool)
		release(item)
		return fmt.Errorf("StartCapture 0x%08X", uint32(hr))
	}
	s.closeTargetLocked()
	s.key = key
	s.kind = kind
	s.item = item
	s.pool = pool
	s.session = session
	s.blankN = 0
	s.last = nil
	return nil
}

func (s *wgcState) nextFrame() (*image.RGBA, error) {
	var frame uintptr
	for i := 0; i < 6; i++ {
		hr := comCall(s.pool, 7, uintptr(unsafe.Pointer(&frame)))
		if failed(hr) {
			s.closeTargetLocked()
			return nil, fmt.Errorf("TryGetNextFrame 0x%08X", uint32(hr))
		}
		if frame != 0 {
			break
		}
		if s.last != nil {
			return s.last, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	if frame == 0 {
		return nil, fmt.Errorf("Windows Graphics Capture 还没有帧")
	}
	defer closeWinRT(frame)

	var surface uintptr
	hr := comCall(frame, 6, uintptr(unsafe.Pointer(&surface)))
	if failed(hr) || surface == 0 {
		return nil, fmt.Errorf("get_Surface 0x%08X", uint32(hr))
	}
	defer release(surface)

	access, err := qi(surface, iidDxgiAccess)
	if err != nil {
		return nil, fmt.Errorf("IDirect3DDxgiInterfaceAccess: %w", err)
	}
	defer release(access)
	var tex uintptr
	iid := iidTexture2D
	hr = comCall(access, 3, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&tex)))
	if failed(hr) || tex == 0 {
		return nil, fmt.Errorf("GetInterface(ID3D11Texture2D) 0x%08X", uint32(hr))
	}
	defer release(tex)

	img, err := s.readTexture(tex)
	if err != nil {
		return nil, err
	}
	img = downscaleRGBA(img, maxCaptureSide)
	s.last = img
	return img, nil
}

func (s *wgcState) readTexture(tex uintptr) (*image.RGBA, error) {
	var desc d3d11Texture2DDesc
	comCall(tex, 10, uintptr(unsafe.Pointer(&desc)))
	if desc.Width == 0 || desc.Height == 0 || desc.Width > 8192 || desc.Height > 8192 {
		return nil, fmt.Errorf("unexpected texture %dx%d", desc.Width, desc.Height)
	}
	if s.staging == 0 || s.stgW != int32(desc.Width) || s.stgH != int32(desc.Height) || s.stgFmt != int32(desc.Format) {
		release(s.staging)
		s.staging = 0
		stgDesc := desc
		stgDesc.MipLevels = 1
		stgDesc.ArraySize = 1
		stgDesc.SampleCount = 1
		stgDesc.SampleQuality = 0
		stgDesc.Usage = 3
		stgDesc.BindFlags = 0
		stgDesc.CPUAccessFlags = 0x20000
		stgDesc.MiscFlags = 0
		var stg uintptr
		hr := comCall(s.device, 5, uintptr(unsafe.Pointer(&stgDesc)), 0, uintptr(unsafe.Pointer(&stg)))
		if failed(hr) || stg == 0 {
			return nil, fmt.Errorf("CreateTexture2D 0x%08X", uint32(hr))
		}
		s.staging = stg
		s.stgW = int32(desc.Width)
		s.stgH = int32(desc.Height)
		s.stgFmt = int32(desc.Format)
	}
	comCall(s.ctx, 47, s.staging, tex)
	var mapped d3d11Mapped
	hr := comCall(s.ctx, 14, s.staging, 0, 1, 0, uintptr(unsafe.Pointer(&mapped)))
	if failed(hr) || mapped.PData == 0 {
		return nil, fmt.Errorf("Map 0x%08X", uint32(hr))
	}
	defer comCall(s.ctx, 15, s.staging, 0)

	w := int(desc.Width)
	h := int(desc.Height)
	pitch := int(mapped.RowPitch)
	if pitch < w*4 {
		return nil, fmt.Errorf("row pitch %d too small", pitch)
	}
	src := unsafe.Slice((*byte)(unsafe.Pointer(mapped.PData)), pitch*h)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := src[y*pitch:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < w; x++ {
			i := x * 4
			dst[i] = row[i+2]
			dst[i+1] = row[i+1]
			dst[i+2] = row[i]
			dst[i+3] = 255
		}
	}
	return img, nil
}

func (s *wgcState) closeTargetLocked() {
	closeWinRT(s.session)
	closeWinRT(s.pool)
	release(s.item)
	release(s.staging)
	s.session, s.pool, s.item, s.staging = 0, 0, 0, 0
	s.kind, s.key = 0, 0
	s.stgW, s.stgH, s.stgFmt = 0, 0, 0
	s.last = nil
}

func roInit() error {
	hr, _, _ := procRoInitialize.Call(1)
	if hr == 0 || hr == 1 || uint32(hr) == 0x80010106 {
		return nil
	}
	if failed(hr) {
		return fmt.Errorf("RoInitialize 0x%08X", uint32(hr))
	}
	return nil
}

func activate(class string, iid windows.GUID) (uintptr, error) {
	u16, err := windows.UTF16FromString(class)
	if err != nil {
		return 0, err
	}
	var hs uintptr
	hr, _, _ := procWindowsCreateString.Call(uintptr(unsafe.Pointer(&u16[0])), uintptr(len(u16)-1), uintptr(unsafe.Pointer(&hs)))
	if failed(hr) {
		return 0, fmt.Errorf("WindowsCreateString 0x%08X", uint32(hr))
	}
	defer procWindowsDeleteString.Call(hs)
	var factory uintptr
	hr, _, _ = procRoGetActivationFactory.Call(hs, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&factory)))
	if failed(hr) || factory == 0 {
		return 0, fmt.Errorf("RoGetActivationFactory %s 0x%08X", class, uint32(hr))
	}
	return factory, nil
}

func comCall(obj uintptr, idx int, args ...uintptr) uintptr {
	if obj == 0 {
		return 0x80004003
	}
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	call := make([]uintptr, 1, 1+len(args))
	call[0] = obj
	call = append(call, args...)
	r, _, _ := syscall.SyscallN(fn, call...)
	return r
}

func qi(obj uintptr, iid windows.GUID) (uintptr, error) {
	var out uintptr
	hr := comCall(obj, 0, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&out)))
	if failed(hr) || out == 0 {
		return 0, fmt.Errorf("0x%08X", uint32(hr))
	}
	return out, nil
}

func release(obj uintptr) {
	if obj != 0 {
		comCall(obj, 2)
	}
}

func closeWinRT(obj uintptr) {
	if obj == 0 {
		return
	}
	if c, err := qi(obj, iidClosable); err == nil {
		comCall(c, 6)
		release(c)
	}
	release(obj)
}

func failed(hr uintptr) bool {
	return int32(hr) < 0
}

func cropRGBA(src *image.RGBA, x, y, w, h int) *image.RGBA {
	b := src.Bounds()
	if x < 0 {
		w += x
		x = 0
	}
	if y < 0 {
		h += y
		y = 0
	}
	if x+w > b.Dx() {
		w = b.Dx() - x
	}
	if y+h > b.Dy() {
		h = b.Dy() - y
	}
	if w < 2 || h < 2 {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for row := 0; row < h; row++ {
		si := (y+row)*src.Stride + x*4
		di := row * dst.Stride
		copy(dst.Pix[di:di+w*4], src.Pix[si:si+w*4])
	}
	return dst
}

func downscaleRGBA(src *image.RGBA, maxSide int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxSide && h <= maxSide {
		return src
	}
	scale := float64(maxSide) / float64(w)
	if hs := float64(maxSide) / float64(h); hs < scale {
		scale = hs
	}
	dw := int(float64(w) * scale)
	dh := int(float64(h) * scale)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy := y * h / dh
		for x := 0; x < dw; x++ {
			sx := x * w / dw
			si := sy*src.Stride + sx*4
			di := y*dst.Stride + x*4
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}
